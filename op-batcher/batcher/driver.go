package batcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	_ "net/http/pprof"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	altda "github.com/ethereum-optimism/optimism/op-alt-da"
	"github.com/ethereum-optimism/optimism/op-batcher/metrics"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-service/bigs"
	"github.com/ethereum-optimism/optimism/op-service/dial"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/txmgr"
)

var (
	ErrBatcherNotRunning = errors.New("batcher is not running")
	emptyTxData          = txData{
		frames: []frameData{
			{
				data: []byte{},
			},
		},
	}
)

type txRef struct {
	id txID
}

func (r txRef) String() string {
	return r.id.String()
}

func (r txRef) TerminalString() string {
	return r.id.TerminalString()
}

type L1Client interface {
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error)
}

type L2Client interface {
	PayloadByNumber(ctx context.Context, number uint64) (*eth.ExecutionPayloadEnvelope, error)
}

type RollupClient interface {
	SyncStatus(ctx context.Context) (*eth.SyncStatus, error)
}

// DriverSetup is the collection of input/output interfaces and configuration that the driver operates on.
type DriverSetup struct {
	closeApp          context.CancelCauseFunc
	Log               log.Logger
	Metr              metrics.Metricer
	RollupConfig      *rollup.Config
	Config            BatcherConfig
	Txmgr             txmgr.TxManager
	L1Client          L1Client
	EndpointProvider  dial.L2EndpointProvider
	ChannelConfig     ChannelConfig
	AltDA             *altda.DAClient
	ChannelOutFactory ChannelOutFactory
}

// BatchSubmitter encapsulates a service responsible for submitting L2 tx
// batches to L1 for availability.
type BatchSubmitter struct {
	DriverSetup
	wg                               *sync.WaitGroup
	shutdownCtx, killCtx             context.Context
	cancelShutdownCtx, cancelKillCtx context.CancelFunc

	mutex   sync.Mutex
	running bool

	channelMgrMutex sync.Mutex // guards channelMgr and prevCurrentL1
	channelMgr      *channelManager
	prevCurrentL1   eth.L1BlockRef // cached CurrentL1 from the last syncStatus

	publishSignal chan pubInfo
}

// NewBatchSubmitter initializes the BatchSubmitter driver from a preconfigured DriverSetup
func NewBatchSubmitter(setup DriverSetup) *BatchSubmitter {
	state := NewChannelManager(setup.Log, setup.Metr, setup.ChannelConfig, setup.RollupConfig)
	if setup.ChannelOutFactory != nil {
		state.SetChannelOutFactory(setup.ChannelOutFactory)
	}

	return &BatchSubmitter{
		DriverSetup: setup,
		channelMgr:  state,
	}
}

func (l *BatchSubmitter) StartBatchSubmitting() error {
	l.Log.Info("Starting Batch Submitter")

	l.mutex.Lock()
	defer l.mutex.Unlock()

	if l.running {
		return errors.New("batcher is already running")
	}
	l.running = true

	l.shutdownCtx, l.cancelShutdownCtx = context.WithCancel(context.Background())
	l.killCtx, l.cancelKillCtx = context.WithCancel(context.Background())
	l.clearState(l.shutdownCtx)
	l.wg = &sync.WaitGroup{}

	if err := l.waitForL2Genesis(); err != nil {
		return fmt.Errorf("error waiting for L2 genesis: %w", err)
	}

	if l.Config.WaitNodeSync {
		err := l.waitNodeSync()
		if err != nil {
			return fmt.Errorf("error waiting for node sync: %w", err)
		}
	}

	receiptsCh := make(chan txmgr.TxReceipt[txRef])

	// Channel used to signal between the loops
	publishSignal := make(chan pubInfo, 1)
	l.publishSignal = publishSignal

	l.wg.Add(3)
	go l.receiptsLoop(l.wg, receiptsCh)                             // ranges over receiptsCh channel
	go l.publishingLoop(l.killCtx, l.wg, receiptsCh, publishSignal) // ranges over publishSignal, spawns routines which send on receiptsCh. Closes receiptsCh when done.
	go l.blockLoadingLoop(l.shutdownCtx, l.wg, publishSignal)       // sends on publishSignal and closes it when done

	l.Log.Info("Batch Submitter started")
	return nil
}

// waitForL2Genesis waits for the L2 genesis time to be reached.
func (l *BatchSubmitter) waitForL2Genesis() error {
	genesisTime := time.Unix(int64(l.RollupConfig.Genesis.L2Time), 0)
	now := time.Now()
	if now.After(genesisTime) {
		return nil
	}

	l.Log.Info("Waiting for L2 genesis", "genesisTime", genesisTime)

	// Create a ticker that fires every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	genesisTrigger := time.After(time.Until(genesisTime))

	for {
		select {
		case <-ticker.C:
			remaining := time.Until(genesisTime)
			l.Log.Info("Waiting for L2 genesis", "remainingTime", remaining.Round(time.Second))
		case <-genesisTrigger:
			l.Log.Info("L2 genesis time reached")
			return nil
		case <-l.shutdownCtx.Done():
			return errors.New("batcher stopped")
		}
	}
}

func (l *BatchSubmitter) StopBatchSubmittingIfRunning(ctx context.Context) error {
	err := l.StopBatchSubmitting(ctx)
	if errors.Is(err, ErrBatcherNotRunning) {
		return nil
	}
	return err
}

// StopBatchSubmitting stops the batch-submitter loop, and force-kills if the provided ctx is done.
func (l *BatchSubmitter) StopBatchSubmitting(ctx context.Context) error {
	l.Log.Info("Stopping Batch Submitter")

	l.mutex.Lock()
	defer l.mutex.Unlock()

	if !l.running {
		return ErrBatcherNotRunning
	}
	l.running = false

	// go routine will call cancelKill() if the passed in ctx is ever Done
	cancelKill := l.cancelKillCtx
	wrapped, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-wrapped.Done()
		cancelKill()
	}()

	l.cancelShutdownCtx()
	l.wg.Wait()
	l.cancelKillCtx()

	l.Log.Info("Batch Submitter stopped")
	return nil
}

// Flush forces the batcher to submit any pending data immediately.
// This works by signaling the publishing loop to process any available data.
func (l *BatchSubmitter) Flush(ctx context.Context) error {
	if !l.running {
		return ErrBatcherNotRunning
	}

	l.Log.Info("Flushing Batch Submitter")
	l.tryPublishSignal(l.publishSignal, pubInfo{forcePublish: true})
	return nil
}

// loadBlocksIntoState loads the blocks between start and end (inclusive).
// If there is a reorg, it will return an error.
func (l *BatchSubmitter) loadBlocksIntoState(ctx context.Context, start, end uint64, publishSignal chan pubInfo) error {
	if end < start {
		return fmt.Errorf("start number is > end number %d,%d", start, end)
	}

	// we don't want to print it in the 1-block case as `loadBlockIntoState` already does
	if end > start {
		l.Log.Info("Loading range of multiple blocks into state", "start", start, "end", end)
	}

	var latestPayload *eth.ExecutionPayload
	// Add all blocks to "state"
	for i := start; i <= end; i++ {
		payload, err := l.loadBlockIntoState(ctx, i)
		if errors.Is(err, ErrReorg) {
			l.Log.Warn("Found L2 reorg", "block_number", i)
			return err
		} else if err != nil {
			l.Log.Warn("Failed to load block into state", "err", err)
			return err
		}
		latestPayload = payload

		if numBlocksLoaded := (i - start + 1); numBlocksLoaded%100 == 0 {
			// Every 100 blocks, signal the publishing loop to publish.
			// This allows the batcher to start publishing sooner in the
			// case of a large backlog of blocks to load.
			l.recordUnsafeDABytes()
			l.tryPublishSignal(publishSignal, pubInfo{ignoreMaxChannelDuration: i < end})
		}

	}

	l2ref, err := derive.PayloadToBlockRef(l.RollupConfig, latestPayload)
	if err != nil {
		l.Log.Warn("Invalid L2 block loaded into state", "err", err)
		return err
	}

	l.Metr.RecordL2BlocksLoaded(l2ref)
	return nil
}

// loadBlockIntoState fetches & stores a single block into `state`. It returns the payload it loaded.
func (l *BatchSubmitter) loadBlockIntoState(ctx context.Context, blockNumber uint64) (*eth.ExecutionPayload, error) {
	l2Client, err := l.EndpointProvider.PayloadSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting L2 client: %w", err)
	}

	cCtx, cancel := context.WithTimeout(ctx, l.Config.NetworkTimeout)
	defer cancel()

	envelope, err := l2Client.PayloadByNumber(cCtx, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("getting L2 block: %w", err)
	}
	payload := envelope.ExecutionPayload

	l.channelMgrMutex.Lock()
	defer l.channelMgrMutex.Unlock()
	if err := l.channelMgr.AddL2Block(payload); err != nil {
		return nil, fmt.Errorf("adding L2 block to state: %w", err)
	}

	l.Log.Info("Added L2 block to local state", "block", payload.ID(), "tx_count", len(payload.Transactions), "time", payload.Timestamp)
	return payload, nil
}

func (l *BatchSubmitter) getSyncStatus(ctx context.Context) (*eth.SyncStatus, error) {
	rollupClient, err := l.EndpointProvider.RollupClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting rollup client: %w", err)
	}

	var (
		syncStatus *eth.SyncStatus
		backoff    = time.Second
		maxBackoff = 30 * time.Second
	)
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	for {
		cCtx, cancel := context.WithTimeout(ctx, l.Config.NetworkTimeout)
		syncStatus, err = rollupClient.SyncStatus(cCtx)
		cancel()

		// Ensure that we have the sync status
		if err != nil {
			return nil, fmt.Errorf("failed to get sync status: %w", err)
		}

		// If we have a head, break out of the loop
		if syncStatus.HeadL1 != (eth.L1BlockRef{}) {
			break
		}

		// Empty sync status, implement backoff
		l.Log.Info("Received empty sync status, backing off", "backoff", backoff)
		select {
		case <-timer.C:
			backoff *= 2
			backoff = min(backoff, maxBackoff)
			// Reset timer to tick of the new backoff time again
			timer.Reset(backoff)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return syncStatus, nil
}

// The following things occur:
// New L2 block (reorg or not)
// L1 transaction is confirmed
//
// What the batcher does:
// Ensure that channels are created & submitted as frames for an L2 range
//
// Error conditions:
// Submitted batch, but it is not valid
// Missed L2 block somehow.

func (l *BatchSubmitter) recordUnsafeDABytes() {
	l.channelMgrMutex.Lock()
	defer l.channelMgrMutex.Unlock()
	l.Metr.RecordUnsafeDABytes(l.channelMgr.UnsafeDABytes())
}

// trySignal tries to send the provided value on the provided channel.
// It is not blocking, no signal will be sent if the channel is full.
func (l *BatchSubmitter) tryPublishSignal(c chan pubInfo, value pubInfo) {
	select {
	case c <- value:
	default:
		l.Log.Warn("publishSignal channel is full, skipping signal")
	}
}

// syncAndPrune computes actions to take based on the current sync status, prunes the channel manager state
// and returns blocks to load.
func (l *BatchSubmitter) syncAndPrune(syncStatus *eth.SyncStatus) *inclusiveBlockRange {
	l.channelMgrMutex.Lock()
	defer l.channelMgrMutex.Unlock()

	// Decide appropriate actions
	syncActions, outOfSync := computeSyncActions(*syncStatus, l.prevCurrentL1, l.channelMgr.blocks, l.channelMgr.channelQueue, l.Log)

	if outOfSync {
		// If the sequencer is out of sync
		// do nothing and wait to see if it has
		// got in sync on the next tick.
		l.Log.Warn("Sequencer is out of sync, retrying next tick.")
		return syncActions.blocksToLoad
	}

	l.prevCurrentL1 = syncStatus.CurrentL1

	// Manage existing state / garbage collection
	if syncActions.clearState != nil {
		l.channelMgr.Clear(*syncActions.clearState)
	} else {
		l.channelMgr.PruneSafeBlocks(syncActions.blocksToPrune)
		l.channelMgr.PruneChannels(syncActions.channelsToPrune)
	}
	return syncActions.blocksToLoad
}

// publishingLoop:
// -  waits for a signal that blocks have been loaded
// -  drives the creation of channels and frames
// -  sends transactions to the DA layer
func (l *BatchSubmitter) publishingLoop(ctx context.Context, wg *sync.WaitGroup, receiptsCh chan txmgr.TxReceipt[txRef], publishSignal chan pubInfo) {
	defer close(receiptsCh)
	defer wg.Done()

	daGroup := &errgroup.Group{}
	// errgroup with limit of 0 means no goroutine is able to run concurrently,
	// so we only set the limit if it is greater than 0.
	if l.Config.MaxConcurrentDARequests > 0 {
		daGroup.SetLimit(int(l.Config.MaxConcurrentDARequests))
	}
	txQueue := txmgr.NewQueue[txRef](ctx, l.Txmgr, l.Config.MaxPendingTransactions)

	for pi := range l.publishSignal {
		l.Log.Debug("publishing loop received signal", "force_publish", pi.forcePublish)
		l.publishStateToL1(ctx, txQueue, receiptsCh, daGroup, pi)
	}

	// First wait for all DA requests to finish to prevent new transactions being queued
	if err := daGroup.Wait(); err != nil {
		if !errors.Is(err, context.Canceled) {
			l.Log.Error("error waiting for DA requests to complete", "err", err)
		}
	}

	// We _must_ wait for all senders on receiptsCh to finish before we can close it.
	if err := txQueue.Wait(); err != nil {
		if !errors.Is(err, context.Canceled) {
			l.Log.Error("error waiting for transactions to complete", "err", err)
		}
	}
	l.Log.Info("publishingLoop returning")
}

// blockLoadingLoop
// -  polls the sequencer,
// -  prunes the channel manager state (i.e. safe blocks)
// -  loads unsafe blocks from the sequencer
func (l *BatchSubmitter) blockLoadingLoop(ctx context.Context, wg *sync.WaitGroup, publishSignal chan pubInfo) {
	ticker := time.NewTicker(l.Config.PollInterval)
	defer ticker.Stop()
	defer close(publishSignal)
	defer wg.Done()
	for {
		select {
		case <-ticker.C:
			syncStatus, err := l.getSyncStatus(ctx)
			if err != nil {
				l.Log.Warn("could not get sync status, retrying on next tick", "err", err)
				continue
			}

			blocksToLoad := l.syncAndPrune(syncStatus)

			if blocksToLoad != nil {
				// Get fresh unsafe blocks
				err := l.loadBlocksIntoState(ctx, blocksToLoad.start, blocksToLoad.end, publishSignal)
				switch {
				case errors.Is(err, ErrReorg):
					l.Log.Warn("error loading blocks, clearing state and waiting for node sync", "err", err)
					l.waitNodeSyncAndClearState()
					continue
				case err != nil:
					l.Log.Warn("error loading blocks, retrying on next tick", "err", err)
					continue
				default:
					l.recordUnsafeDABytes()
				}
			}
			l.tryPublishSignal(publishSignal, pubInfo{}) // always signal the publishing loop to ensure we periodically publish even if we aren't loading blocks
		case <-ctx.Done():
			l.Log.Info("blockLoadingLoop returning")
			return
		}
	}
}

// receiptsLoop handles transaction receipts from the DA layer
func (l *BatchSubmitter) receiptsLoop(wg *sync.WaitGroup, receiptsCh chan txmgr.TxReceipt[txRef]) {
	defer wg.Done()
	l.Log.Info("Starting receipts processing loop")
	for r := range receiptsCh {
		l.Log.Info("Handling receipt", "id", r.ID)
		l.handleReceipt(r)
	}
	l.Log.Info("receiptsLoop returning")
}

func (l *BatchSubmitter) shutdownOnCriticalError(err error) {
	l.Log.Error("Critical error detected, attempting batcher shut down", "err", err)
	if l.closeApp != nil {
		// Call closeApp to trigger process to exit (gracefully) if l.closeApp is set.
		l.closeApp(err)
	} else {
		l.Log.Warn("No closeApp function set, cannot shut down batcher on critical error", "err", err)
	}
}

func (l *BatchSubmitter) waitNodeSyncAndClearState() {
	// Wait for any in flight transactions
	// to be ingested by the node before
	// we start loading blocks again.
	err := l.waitNodeSync()
	if err != nil {
		l.Log.Warn("error waiting for node sync", "err", err)
	}
	l.clearState(l.shutdownCtx)
}

// waitNodeSync Check to see if there was a batcher tx sent recently that
// still needs more block confirmations before being considered finalized
func (l *BatchSubmitter) waitNodeSync() error {
	ctx := l.shutdownCtx
	rollupClient, err := l.EndpointProvider.RollupClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to get rollup client: %w", err)
	}

	cCtx, cancel := context.WithTimeout(ctx, l.Config.NetworkTimeout)
	defer cancel()

	l1Tip, err := l.l1Tip(cCtx)
	if err != nil {
		return fmt.Errorf("failed to retrieve l1 tip: %w", err)
	}

	l1TargetBlock := bigs.Uint64Strict(l1Tip.Number)
	if l.Config.CheckRecentTxsDepth != 0 {
		l.Log.Info("Checking for recently submitted batcher transactions on L1")
		recentBlock, found, err := eth.CheckRecentTxs(cCtx, l.L1Client, l.Config.CheckRecentTxsDepth, l.Txmgr.From())
		if err != nil {
			return fmt.Errorf("failed checking recent batcher txs: %w", err)
		}
		l.Log.Info("Checked for recently submitted batcher transactions on L1",
			"l1_head", eth.HeaderBlockID(l1Tip), "l1_recent", recentBlock, "found", found)
		l1TargetBlock = recentBlock
	}

	return dial.WaitRollupSync(l.shutdownCtx, l.Log, rollupClient, l1TargetBlock, time.Second*12)
}

// publishStateToL1 queues up all pending TxData to be published to the L1, returning when there is no more data to
// queue for publishing or if there was an error queuing the data.
func (l *BatchSubmitter) publishStateToL1(ctx context.Context, queue *txmgr.Queue[txRef], receiptsCh chan txmgr.TxReceipt[txRef], daGroup *errgroup.Group, pi pubInfo) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// if the txmgr is closed, we stop the transaction sending
		if l.Txmgr.IsClosed() {
			l.Log.Info("Txmgr is closed, aborting state publishing")
			return
		}

		err := l.publishTxToL1(ctx, queue, receiptsCh, daGroup, pi)
		if err != nil {
			if err != io.EOF {
				l.Log.Error("Error publishing tx to l1", "err", err)
			}
			return
		}
	}
}

// clearState clears the state of the channel manager
func (l *BatchSubmitter) clearState(ctx context.Context) {
	l.Log.Info("Clearing state")
	defer l.Log.Info("State cleared")

	clearStateWithL1Origin := func() bool {
		l1SafeOrigin, err := l.safeL1Origin(ctx)
		if err != nil {
			l.Log.Warn("Failed to query L1 safe origin, will retry", "err", err)
			return false
		} else {
			l.Log.Info("Clearing state with safe L1 origin", "origin", l1SafeOrigin)
			l.channelMgrMutex.Lock()
			defer l.channelMgrMutex.Unlock()
			l.channelMgr.Clear(l1SafeOrigin)
			return true
		}
	}

	// Attempt to set the L1 safe origin and clear the state, if fetching fails -- fall through to an infinite retry
	if clearStateWithL1Origin() {
		return
	}

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			if clearStateWithL1Origin() {
				return
			}
		case <-ctx.Done():
			l.Log.Warn("Clearing state cancelled")
			l.channelMgrMutex.Lock()
			defer l.channelMgrMutex.Unlock()
			l.channelMgr.Clear(eth.BlockID{})
			return
		}
	}
}

// publishTxToL1 submits a single state tx to the L1
func (l *BatchSubmitter) publishTxToL1(ctx context.Context, queue *txmgr.Queue[txRef], receiptsCh chan txmgr.TxReceipt[txRef], daGroup *errgroup.Group, pi pubInfo) error {
	// send all available transactions
	l1tip, err := l.l1Tip(ctx)
	if err != nil {
		l.Log.Error("Failed to query L1 tip", "err", err)
		return err
	}
	l.Metr.RecordLatestL1Block(eth.InfoToL1BlockRef(eth.HeaderBlockInfo(l1tip)))

	// Collect next transaction data. This pulls data out of the channel, so we need to make sure
	// to put it back if ever da or txmgr requests fail, by calling l.recordFailedDARequest/recordFailedTx.
	l.channelMgrMutex.Lock()
	txdata, err := l.channelMgr.TxData(eth.HeaderBlockID(l1tip), pi)
	l.channelMgrMutex.Unlock()

	if err == io.EOF {
		l.Log.Trace("No transaction data available")
		return err
	} else if err != nil {
		l.Log.Error("Unable to get tx data", "err", err)
		return err
	}

	return l.publishToAltDAAndL1(txdata, queue, receiptsCh, daGroup)
}

func (l *BatchSubmitter) safeL1Origin(ctx context.Context) (eth.BlockID, error) {
	c, err := l.EndpointProvider.RollupClient(ctx)
	if err != nil {
		log.Error("Failed to get rollup client", "err", err)
		return eth.BlockID{}, fmt.Errorf("safe l1 origin: error getting rollup client: %w", err)
	}

	cCtx, cancel := context.WithTimeout(ctx, l.Config.NetworkTimeout)
	defer cancel()

	status, err := c.SyncStatus(cCtx)
	if err != nil {
		log.Error("Failed to get sync status", "err", err)
		return eth.BlockID{}, fmt.Errorf("safe l1 origin: error getting sync status: %w", err)
	}

	// If the safe L2 block origin is 0, we are at the genesis block and should use the L1 origin from the rollup config.
	if status.LocalSafeL2.L1Origin.Number == 0 {
		return l.RollupConfig.Genesis.L1, nil
	}

	return status.LocalSafeL2.L1Origin, nil
}

// publishToAltDAAndL1 posts the txdata to the DA Provider and then sends the commitment to L1.
// It returns an error, after starting the batcher's shutdown, if txdata violates a sanity check.
func (l *BatchSubmitter) publishToAltDAAndL1(txdata txData, queue *txmgr.Queue[txRef], receiptsCh chan txmgr.TxReceipt[txRef], daGroup *errgroup.Group) error {
	// sanity checks
	if nf := len(txdata.frames); nf != 1 {
		err := fmt.Errorf("unexpected number of frames in calldata tx: %d", nf)
		l.shutdownOnCriticalError(err)
		return err
	}

	// when posting txdata to an external DA Provider, we use a goroutine to avoid blocking the main loop
	// since it may take a while for the request to return.
	goroutineSpawned := daGroup.TryGo(func() error {
		// TODO: probably shouldn't be using the global shutdownCtx here, see https://go.dev/blog/context-and-structs
		// but publishTxToL1 receives l.killCtx as an argument, which currently is only canceled after waiting for the main loop
		// to exit, which would wait on this DA call to finish, which would take a long time.
		// So we prefer to mimic the behavior of txmgr and cancel all pending DA/txmgr requests when the batcher is stopped.
		comm, err := l.AltDA.SetInput(l.shutdownCtx, txdata.CallData())
		if err != nil {
			// Don't log context cancelled events because they are expected,
			// and can happen after tests complete which causes a panic.
			if errors.Is(err, context.Canceled) {
				l.recordFailedDARequest(txdata.ID(), nil)
			} else {
				l.Log.Error("Failed to post input to Alt DA", "error", err)
				// requeue frame if we fail to post to the DA Provider so it can be retried
				// note: this assumes that the da server caches requests, otherwise it might lead to resubmissions of the blobs
				l.recordFailedDARequest(txdata.ID(), err)
			}
			return nil
		}
		l.Log.Info("Set altda input", "commitment", comm, "tx", txdata.ID())
		l.sendTx(txdata, &txmgr.TxCandidate{To: &l.RollupConfig.BatchInboxAddress, TxData: comm.TxData()}, queue, receiptsCh)
		return nil
	})
	if !goroutineSpawned {
		// We couldn't start the goroutine because the errgroup.Group limit
		// is already reached. Since we can't send the txdata, we have to
		// return it for later processing. io.EOF ends this publishing round
		// without logging an error, so the loop waits for the next signal
		// instead of polling the L1 head until a DA slot frees up.
		l.recordFailedDARequest(txdata.ID(), nil)
		return io.EOF
	}
	return nil
}

type TxSender[T any] interface {
	Send(id T, candidate txmgr.TxCandidate, receiptCh chan txmgr.TxReceipt[T])
}

// sendTx queues the commitment transaction. It blocks if the txmgr queue has reached its
// MaxPendingTransactions limit.
func (l *BatchSubmitter) sendTx(txdata txData, candidate *txmgr.TxCandidate, queue TxSender[txRef], receiptsCh chan txmgr.TxReceipt[txRef]) {
	queue.Send(txRef{id: txdata.ID()}, *candidate, receiptsCh)
}

func (l *BatchSubmitter) handleReceipt(r txmgr.TxReceipt[txRef]) {
	// Record TX Status
	if r.Err != nil {
		l.recordFailedTx(r.ID.id, r.Err)
	} else if r.Receipt != nil {
		l.recordConfirmedTx(r.ID.id, r.Receipt)
	}
	// Both r.Err and r.Receipt can be nil, in which case we do nothing.
}

func (l *BatchSubmitter) recordFailedDARequest(id txID, err error) {
	l.channelMgrMutex.Lock()
	defer l.channelMgrMutex.Unlock()
	if err != nil {
		l.Log.Warn("DA request failed", logFields(id, err)...)
	}
	l.channelMgr.TxFailed(id)
}

func (l *BatchSubmitter) recordFailedTx(id txID, err error) {
	l.channelMgrMutex.Lock()
	defer l.channelMgrMutex.Unlock()
	l.Log.Warn("Transaction failed to send", logFields(id, err)...)
	l.channelMgr.TxFailed(id)
}

func (l *BatchSubmitter) recordConfirmedTx(id txID, receipt *types.Receipt) {
	l.channelMgrMutex.Lock()
	defer l.channelMgrMutex.Unlock()
	l.Log.Info("Transaction confirmed", logFields(id, receipt)...)
	l1block := eth.ReceiptBlockID(receipt)
	l.channelMgr.TxConfirmed(id, l1block)
}

// l1Tip gets the current L1 tip header. The passed context is assumed to be a lifetime context,
// so it is internally wrapped with a network timeout.
func (l *BatchSubmitter) l1Tip(ctx context.Context) (*types.Header, error) {
	tctx, cancel := context.WithTimeout(ctx, l.Config.NetworkTimeout)
	defer cancel()
	head, err := l.L1Client.HeaderByNumber(tctx, nil)
	if err != nil {
		return nil, fmt.Errorf("getting latest L1 block: %w", err)
	}
	return head, nil
}

func logFields(xs ...any) (fs []any) {
	for _, x := range xs {
		switch v := x.(type) {
		case txID:
			fs = append(fs, "tx_id", v.String())
		case *types.Receipt:
			fs = append(fs, "tx", v.TxHash, "block", eth.ReceiptBlockID(v))
		case error:
			fs = append(fs, "err", v)
		default:
			fs = append(fs, "ERROR", fmt.Sprintf("logFields: unknown type: %T", x))
		}
	}
	return fs
}
