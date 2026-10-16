package node

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethevent "github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/rpc"

	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-node/config"
	"github.com/ethereum-optimism/optimism/op-node/metrics"
	"github.com/ethereum-optimism/optimism/op-node/node/tracer"
	"github.com/ethereum-optimism/optimism/op-node/p2p"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/async"
	"github.com/ethereum-optimism/optimism/op-node/rollup/driver"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sequencing"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/clock"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/event"
	"github.com/ethereum-optimism/optimism/op-service/httputil"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	opsigner "github.com/ethereum-optimism/optimism/op-service/signer"
	"github.com/ethereum-optimism/optimism/op-service/sources"
	"github.com/prometheus/client_golang/prometheus"
)

var ErrAlreadyClosed = errors.New("node is already closed")

// L1Client is the interface that op-node uses to interact with L1.
// This allows wrapped or mocked clients to be used
type L1Client interface {
	L1BlockRefByLabel(ctx context.Context, label eth.BlockLabel) (eth.L1BlockRef, error)
	L1BlockRefByNumber(ctx context.Context, num uint64) (eth.L1BlockRef, error)
	L1BlockRefByHash(ctx context.Context, hash common.Hash) (eth.L1BlockRef, error)
	InfoByHash(ctx context.Context, hash common.Hash) (eth.BlockInfo, error)
	InfoByNumber(ctx context.Context, number uint64) (eth.BlockInfo, error)
	InfoByLabel(ctx context.Context, label eth.BlockLabel) (eth.BlockInfo, error)
	InfoAndTxsByHash(ctx context.Context, hash common.Hash) (eth.BlockInfo, types.Transactions, error)
	InfoAndTxsByNumber(ctx context.Context, number uint64) (eth.BlockInfo, types.Transactions, error)
	InfoAndTxsByLabel(ctx context.Context, label eth.BlockLabel) (eth.BlockInfo, types.Transactions, error)
	FetchReceipts(ctx context.Context, blockHash common.Hash) (eth.BlockInfo, optypes.Receipts, error)
	SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error)
	ChainID(ctx context.Context) (*big.Int, error)
	Close()
}

// L1Source provides the necessary L1 blockchain data for the node.
type L1Source interface {
	L1BlockRefByLabel(ctx context.Context, label eth.BlockLabel) (eth.L1BlockRef, error)
	L1BlockRefByNumber(ctx context.Context, num uint64) (eth.L1BlockRef, error)
	L1BlockRefByHash(ctx context.Context, hash common.Hash) (eth.L1BlockRef, error)
	SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error)
	InfoByHash(ctx context.Context, hash common.Hash) (eth.BlockInfo, error)
	InfoAndTxsByHash(ctx context.Context, hash common.Hash) (eth.BlockInfo, types.Transactions, error)
	FetchReceipts(ctx context.Context, blockHash common.Hash) (eth.BlockInfo, optypes.Receipts, error)
	Close()
}

type OpNode struct {
	// Retain the config to test for active features rather than test for runtime state.
	cfg        *config.Config
	log        log.Logger
	clock      clock.Clock
	appVersion string
	metrics    *metrics.Metrics

	l1HeadsSub              ethereum.Subscription // Subscription to get L1 heads (automatically re-subscribes on error)
	l1SafeSub               ethereum.Subscription // Subscription to get L1 safe blocks, a.k.a. justified data (polling)
	l1FinalizedSub          ethereum.Subscription // Subscription to get L1 safe blocks, a.k.a. justified data (polling)
	l1ReceiptsPrefetchGroup gosync.WaitGroup

	eventSys   event.System
	eventDrain driver.Drain

	l1Source  L1Source              // L1 Client to fetch data from
	l2Driver  *driver.Driver        // L2 Engine to Sync
	l2Source  *sources.EngineClient // L2 Execution Engine RPC bindings
	server    *oprpc.Server         // RPC server hosting the rollup-node API
	p2pNode   *p2p.NodeP2P          // P2P node functionality
	p2pMu     gosync.Mutex          // protects p2pNode
	p2pSigner p2p.Signer            // p2p gossip application messages will be signed with this signer

	pprofService *oppprof.Service
	metricsSrv   *httputil.HTTPServer

	// some resources cannot be stopped directly, like the p2p gossipsub router (not our design),
	// and depend on this ctx to be closed.
	resourcesCtx   context.Context
	resourcesClose context.CancelFunc

	closed atomic.Bool

	// cancels execution prematurely. This may be nil.
	cancel context.CancelCauseFunc

	tracer tracer.Tracer // used for testing PublishBlock and SignAndPublishL2Payload
}

// New creates a new OpNode instance.
// The provided ctx argument is for the span of initialization only;
// the node will immediately Stop(ctx) before finishing initialization if the context is canceled during initialization.
func New(ctx context.Context, cfg *config.Config, log log.Logger, appVersion string, m *metrics.Metrics, clk clock.Clock) (*OpNode, error) {
	return NewWithOverride(ctx, cfg, log, appVersion, m, clk, InitializationOverrides{})
}

// NewWithOverride creates a new OpNode instance with optional initialization overrides.
// This allows callers to override specific initialization steps, enabling resource sharing
// (e.g., shared L1Client across multiple nodes) without duplicating connections or caches.
// If override is nil or any of its fields are nil, the default initialization is used for those steps.
func NewWithOverride(ctx context.Context, cfg *config.Config, log log.Logger, appVersion string, m *metrics.Metrics, clk clock.Clock, override InitializationOverrides) (*OpNode, error) {
	if err := cfg.Check(); err != nil {
		return nil, err
	}

	n := &OpNode{
		cfg:        cfg,
		log:        log,
		clock:      clk,
		appVersion: appVersion,
		metrics:    m,
		cancel:     cfg.Cancel,
		tracer:     cfg.Tracer,
	}
	// not a context leak, gossipsub is closed with a context.
	n.resourcesCtx, n.resourcesClose = context.WithCancel(context.Background())

	err := n.init(ctx, cfg, override)
	if err != nil {
		log.Error("Error initializing the rollup node", "err", err)
		// ensure we always close the node resources if we fail to initialize the node.
		if closeErr := n.Stop(ctx); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	return n, nil
}

type InitializationOverrides struct {
	L1Source        L1Source
	RPCHandler      *oprpc.Handler
	MetricsRegistry func(*prometheus.Registry)
}

// init progressively creates and sets up all the components of the OpNode
// some later initialization steps depend on the node being partially initialized with other components,
// so order is important to ensure that all resources are available when needed.
func (n *OpNode) init(ctx context.Context, cfg *config.Config, overrides InitializationOverrides) error {
	n.log.Info("Initializing rollup node", "version", n.appVersion)

	var err error

	n.eventSys, n.eventDrain, err = initEventSystem(n)
	if err != nil {
		return fmt.Errorf("failed to init event system: %w", err)
	}

	if overrides.L1Source == nil {
		l1Source, err := initL1Source(ctx, cfg, n)
		if err != nil {
			return fmt.Errorf("failed to init L1 Source: %w", err)
		}
		n.l1Source = l1Source
	} else {
		n.l1Source = overrides.L1Source
	}

	n.l2Source, n.l2Driver, err = initL2(ctx, cfg, n)
	if err != nil {
		return fmt.Errorf("failed to init L2: %w", err)
	}

	n.l1HeadsSub, n.l1SafeSub, n.l1FinalizedSub, err = initL1Handlers(cfg, n)
	if err != nil {
		return fmt.Errorf("failed to init L1 Source: %w", err)
	}

	n.p2pSigner, err = initP2PSigner(ctx, cfg, n)
	if err != nil {
		return fmt.Errorf("failed to init the P2P signer: %w", err)
	}

	n.p2pNode, err = initP2P(cfg, n)
	if err != nil {
		return fmt.Errorf("failed to init the P2P stack: %w", err)
	}

	// Only expose the server at the end, ensuring all RPC backend components are initialized.
	if overrides.RPCHandler == nil {
		n.server, err = initRPCServer(cfg, n)
		if err != nil {
			return fmt.Errorf("failed to init the RPC server: %w", err)
		}
	} else {
		// the node registers to an existing RPC server's handler if provided
		// the node assumes the RPC server is already started
		n.server = nil
		err := registerAPIs(cfg, n, overrides.RPCHandler)
		if err != nil {
			// panic here is to match the behavior of oprcp.Server.AddAPI,
			// which wraps the Handler and panics if the API can't be added.
			panic(fmt.Errorf("invalid API: %w", err))
		}
	}

	// Expose metrics registry to provided registry if requested
	if overrides.MetricsRegistry != nil && n.metrics != nil {
		if reg := n.metrics.Registry(); reg != nil {
			overrides.MetricsRegistry(reg)
		}
	}

	n.metricsSrv, err = initMetricsServer(cfg, n)
	if err != nil {
		return fmt.Errorf("failed to init the metrics server: %w", err)
	}

	n.metrics.RecordInfo(n.appVersion)
	n.metrics.RecordUp()

	n.pprofService, err = initPProf(cfg, n)
	if err != nil {
		return fmt.Errorf("failed to init profiling: %w", err)
	}

	return nil
}

func initEventSystem(node *OpNode) (event.System, driver.Drain, error) {
	// This executor will be configurable in the future, for parallel event processing
	executor := event.NewGlobalSynchronous(node.resourcesCtx).WithMetrics(node.metrics)
	sys := event.NewSystem(node.log, executor)
	sys.AddTracer(event.NewMetricsTracer(node.metrics))
	sys.Register("node", event.DeriverFunc(node.onEvent))
	return sys, executor, nil
}

func initL1Source(ctx context.Context, cfg *config.Config, node *OpNode) (*sources.L1Client, error) {
	// Cache 3/2 worth of sequencing window of receipts and txs
	defaultCacheSize := int(cfg.Rollup.SeqWindowSize) * 3 / 2
	l1RPC, l1Cfg, err := cfg.L1.Setup(ctx, node.log, defaultCacheSize, node.metrics)
	if err != nil {
		return nil, fmt.Errorf("failed to get L1 RPC client: %w", err)
	}

	l1Source, err := sources.NewL1Client(l1RPC, node.log, node.metrics.L1SourceCache, l1Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create L1 source: %w", err)
	}

	if err := cfg.Rollup.ValidateL1Config(ctx, node.log, l1Source); err != nil {
		return nil, fmt.Errorf("failed to validate the L1 config: %w", err)
	}

	return l1Source, nil
}

func initL1Handlers(cfg *config.Config, node *OpNode) (ethereum.Subscription, ethereum.Subscription, ethereum.Subscription, error) {
	if node.l2Driver == nil {
		return nil, nil, nil, errors.New("l2 driver must be initialized")
	}
	onL1Head := func(ctx context.Context, sig eth.L1BlockRef) {
		if node.cfg.Tracer != nil {
			node.cfg.Tracer.OnNewL1Head(ctx, sig)
		}
		if cfg.Driver.SequencerEnabled {
			node.l1ReceiptsPrefetchGroup.Add(1)
			go func() {
				defer node.l1ReceiptsPrefetchGroup.Done()
				if _, _, err := node.l1Source.FetchReceipts(ctx, sig.Hash); err != nil {
					node.log.Warn("Failed to prefetch L1 receipts", "hash", sig.Hash, "err", err)
				} else {
					node.log.Debug("Successfully prefetched L1 receipts", "hash", sig.Hash)
				}
			}()
		}
		node.l2Driver.SyncDeriver.L1Tracker.OnL1Unsafe(sig)
		node.l2Driver.StatusTracker.OnL1Unsafe(sig)
		node.l2Driver.SyncDeriver.OnL1Unsafe(ctx)
	}
	onL1Safe := func(ctx context.Context, sig eth.L1BlockRef) {
		node.l2Driver.StatusTracker.OnL1Safe(sig)
	}
	onL1Finalized := func(ctx context.Context, sig eth.L1BlockRef) {
		node.l2Driver.StatusTracker.OnL1Finalized(sig)
		node.l2Driver.Finalizer.OnL1Finalized(sig)
		node.l2Driver.SyncDeriver.OnL1Finalized(ctx)
	}

	// Keep subscribed to the L1 heads, which keeps the L1 maintainer pointing to the best headers to sync
	l1HeadsSub := gethevent.ResubscribeErr(time.Second*10, func(ctx context.Context, err error) (gethevent.Subscription, error) {
		if err != nil {
			node.log.Warn("resubscribing after failed L1 subscription", "err", err)
		}
		return eth.WatchHeadChanges(ctx, node.l1Source, onL1Head)
	})
	go func() {
		err, ok := <-l1HeadsSub.Err()
		if !ok {
			return
		}
		node.log.Error("l1 heads subscription error", "err", err)
	}()

	// Poll for the safe L1 block and finalized block,
	// which only change once per epoch at most and may be delayed.
	l1SafeSub := eth.PollBlockChanges(node.log, node.l1Source, onL1Safe, eth.Safe,
		cfg.L1EpochPollInterval, time.Second*10)
	l1FinalizedSub := eth.PollBlockChanges(node.log, node.l1Source, onL1Finalized, eth.Finalized,
		cfg.L1EpochPollInterval, time.Second*10)

	return l1HeadsSub, l1SafeSub, l1FinalizedSub, nil
}

func initL2(ctx context.Context, cfg *config.Config, node *OpNode) (*sources.EngineClient, *driver.Driver, error) {
	rpcClient, rpcCfg, err := cfg.L2.Setup(ctx, node.log, &cfg.Rollup, node.metrics)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to setup L2 execution-engine RPC client: %w", err)
	}

	l2Source, err := sources.NewEngineClient(rpcClient, node.log, node.metrics.L2SourceCache, rpcCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Engine client: %w", err)
	}

	if err := cfg.Rollup.ValidateL2Config(ctx, node.log, l2Source, cfg.Sync.SyncMode == sync.ELSync); err != nil {
		return nil, nil, err
	}

	altDA := cfg.AltDA.NewDAClient()

	if cfg.Rollup.ChainOpConfig == nil {
		return nil, nil, fmt.Errorf("cfg.Rollup.ChainOpConfig is nil. Please see https://github.com/ethereum-optimism/optimism/releases/tag/op-node/v1.11.0: %w", err)
	}

	l2Driver := driver.NewDriver(node.eventSys, node.eventDrain, &cfg.Driver, &cfg.Rollup, cfg.L1ChainConfig, l2Source, node.l1Source,
		node, node.log, node.metrics, cfg.ConfigPersistence, &cfg.Sync, altDA)

	return l2Source, l2Driver, nil
}

func initRPCServer(cfg *config.Config, node *OpNode) (*oprpc.Server, error) {
	server := newRPCServer(&cfg.RPC, &cfg.Rollup,
		node.l2Source.L2Client, node.l2Driver,
		node.log, node.appVersion)
	if err := registerAPIs(cfg, node, server.Handler); err != nil {
		// panic here is to match the behavior of oprcp.Server.AddAPI,
		// which wraps the Handler and panics if the API can't be added.
		panic(fmt.Errorf("invalid API: %w", err))
	}
	node.log.Info("Starting JSON-RPC server")
	if err := server.Start(); err != nil {
		return nil, fmt.Errorf("unable to start RPC server: %w", err)
	}
	node.log.Info("Started JSON-RPC server", "addr", server.Endpoint())
	return server, nil
}

func registerAPIs(cfg *config.Config, node *OpNode, handler *oprpc.Handler) error {
	// Register the main optimism namespace API
	// The optimism namespace may already be registered
	api := NewNodeAPI(&cfg.Rollup, node.l2Source.L2Client, node.l2Driver, node.log)
	if err := handler.AddAPI(rpc.API{
		Namespace: "optimism",
		Service:   api,
	}); err != nil {
		return fmt.Errorf("failed to add Optimism API: %w", err)
	}

	if p2pNode := node.getP2PNodeIfEnabled(); p2pNode != nil {
		if err := handler.AddAPI(rpc.API{
			Namespace: p2p.NamespaceRPC,
			Service:   p2p.NewP2PAPIBackend(p2pNode, node.log),
		}); err != nil {
			return fmt.Errorf("failed to add P2P API: %w", err)
		}
		node.log.Info("P2P RPC enabled")
	}
	if cfg.RPC.EnableAdmin {
		if err := handler.AddAPI(rpc.API{
			Namespace: "admin",
			Service:   NewAdminAPI(node.l2Driver, node.log),
		}); err != nil {
			return fmt.Errorf("failed to add Admin API: %w", err)
		}
		node.log.Info("Admin RPC enabled")
	}
	return nil
}

func initMetricsServer(cfg *config.Config, node *OpNode) (*httputil.HTTPServer, error) {
	if !cfg.Metrics.Enabled {
		node.log.Info("metrics disabled")
		return nil, nil
	}
	node.log.Debug("starting metrics server", "addr", cfg.Metrics.ListenAddr, "port", cfg.Metrics.ListenPort)
	metricsSrv, err := node.metrics.StartServer(cfg.Metrics.ListenAddr, cfg.Metrics.ListenPort)
	if err != nil {
		return nil, fmt.Errorf("failed to start metrics server: %w", err)
	}
	node.log.Info("started metrics server", "addr", metricsSrv.Addr())
	return metricsSrv, nil
}

func initPProf(cfg *config.Config, node *OpNode) (*oppprof.Service, error) {
	pprofService := oppprof.New(
		cfg.Pprof.ListenEnabled,
		cfg.Pprof.ListenAddr,
		cfg.Pprof.ListenPort,
		cfg.Pprof.ProfileType,
		cfg.Pprof.ProfileDir,
		cfg.Pprof.ProfileFilename,
	)

	if err := pprofService.Start(); err != nil {
		return nil, fmt.Errorf("failed to start pprof service: %w", err)
	}

	return pprofService, nil
}

func (n *OpNode) p2pEnabled() bool {
	return n.cfg.P2PEnabled()
}

func initP2P(cfg *config.Config, node *OpNode) (*p2p.NodeP2P, error) {
	node.p2pMu.Lock()
	defer node.p2pMu.Unlock()
	if node.p2pNode != nil {
		panic("p2p node already initialized")
	}
	if node.p2pEnabled() {
		if node.l2Driver.SyncDeriver == nil {
			panic("SyncDeriver must be initialized")
		}
		// embed syncDeriver and tracer(optional) to the blockReceiver to handle unsafe payloads via p2p
		rec := p2p.NewBlockReceiver(node.log, node.metrics, node.l2Driver.SyncDeriver, node.cfg.Tracer)
		p2pNode, err := p2p.NewNodeP2P(node.resourcesCtx, &cfg.Rollup, node.log, cfg.P2P, rec, node.l2Source, p2p.SequencerAddress(cfg.P2PSequencerAddress), node.metrics, node.clock)
		if err != nil {
			return nil, err
		}
		if p2pNode.Dv5Udp() != nil {
			go p2pNode.DiscoveryProcess(node.resourcesCtx, node.log, &cfg.Rollup, cfg.P2P.TargetPeers())
		}
		return p2pNode, nil
	}
	return nil, nil
}

func initP2PSigner(ctx context.Context, cfg *config.Config, node *OpNode) (p2p.Signer, error) {
	// the p2p signer setup is optional
	if cfg.P2PSigner == nil {
		return nil, nil
	}
	// p2pSigner may still be nil, the signer setup may not create any signer, the signer is optional
	p2pSigner, err := cfg.P2PSigner.SetupSigner(ctx)
	return p2pSigner, err
}

func (n *OpNode) Start(ctx context.Context) error {
	n.log.Info("Starting execution engine driver")
	// start driving engine: sync blocks by deriving them from L1 and driving them into the engine
	if err := n.l2Driver.Start(); err != nil {
		n.log.Error("Could not start a rollup node", "err", err)
		return err
	}
	log.Info("Rollup node started")
	return nil
}

// onEvent handles broadcast events.
// The OpNode itself is a deriver to catch system-critical events.
// Other event-handling should be encapsulated into standalone derivers.
func (n *OpNode) onEvent(ctx context.Context, ev event.Event) bool {
	switch x := ev.(type) {
	case rollup.CriticalErrorEvent:
		n.log.Error("Critical error", "err", x.Err)
		n.cancel(fmt.Errorf("critical error: %w", x.Err))
		return true
	default:
		return false
	}
}

func (n *OpNode) PublishBlock(ctx context.Context, signedEnvelope *opsigner.SignedExecutionPayloadEnvelope) error {
	if n.tracer != nil {
		n.tracer.OnPublishL2Payload(ctx, signedEnvelope.Envelope)
	}
	if p2pNode := n.getP2PNodeIfEnabled(); p2pNode != nil {
		n.log.Info("Publishing signed execution payload on p2p", "id", signedEnvelope.ID())
		return p2pNode.GossipOut().PublishSignedL2Payload(ctx, signedEnvelope)
	}
	return errors.New("P2P not enabled")
}

func (n *OpNode) SignAndPublishL2Payload(ctx context.Context, envelope *eth.ExecutionPayloadEnvelope) error {
	if n.tracer != nil {
		n.tracer.OnPublishL2Payload(ctx, envelope)
	}
	// publish to p2p, if we are running p2p at all
	if p2pNode := n.getP2PNodeIfEnabled(); p2pNode != nil {
		if n.p2pSigner == nil {
			// Permanent: a signer does not appear without a restart, so retrying
			// only holds the block at the head of the publish queue.
			return fmt.Errorf("%w: node has no p2p signer, payload %s cannot be published",
				async.ErrPermanentPublish, envelope.ID())
		}
		n.log.Info("Publishing signed execution payload on p2p", "id", envelope.ID())
		return p2pNode.GossipOut().SignAndPublishL2Payload(ctx, envelope, n.p2pSigner)
	}
	// if p2p is not enabled then we just don't publish the payload
	return nil
}

// GossipTimestampThreshold reports the age past which peers reject a block. Zero
// when p2p is disabled, where publishing is a no-op anyway.
func (n *OpNode) GossipTimestampThreshold() time.Duration {
	if !n.p2pEnabled() {
		return 0
	}
	return n.cfg.P2P.GetGossipTimestampThreshold()
}

func (n *OpNode) P2P() p2p.Node {
	return n.getP2PNodeIfEnabled()
}

// Stop stops the node and closes all resources.
// If the provided ctx is expired, the node will accelerate the stop where possible, but still fully close.
func (n *OpNode) Stop(ctx context.Context) error {
	if n.closed.Load() {
		return ErrAlreadyClosed
	}

	var result error

	if n.server != nil {
		if err := n.server.Stop(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close RPC server: %w", err))
		}
	}

	// Stop sequencer and report last hash. l2Driver can be nil if we're cleaning up a failed init.
	if n.l2Driver != nil {
		latestHead, err := n.l2Driver.StopSequencer(ctx)
		switch {
		case errors.Is(err, sequencing.ErrSequencerNotEnabled):
		case errors.Is(err, driver.ErrSequencerAlreadyStopped):
			n.log.Info("stopping node: sequencer already stopped", "latestHead", latestHead)
		case err == nil:
			n.log.Info("stopped sequencer", "latestHead", latestHead)
		default:
			result = errors.Join(result, fmt.Errorf("error stopping sequencer: %w", err))
		}
	}

	n.p2pMu.Lock()
	if n.p2pNode != nil {
		if err := n.p2pNode.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close p2p node: %w", err))
		}
		// Prevent further use of p2p.
		n.p2pNode = nil
	}
	n.p2pMu.Unlock()

	if n.p2pSigner != nil {
		if err := n.p2pSigner.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close p2p signer: %w", err))
		}
	}

	if n.resourcesClose != nil {
		n.resourcesClose()
	}

	// stop L1 heads feed
	if n.l1HeadsSub != nil {
		n.l1HeadsSub.Unsubscribe()
	}
	n.l1ReceiptsPrefetchGroup.Wait()
	// stop polling for L1 safe-head changes
	if n.l1SafeSub != nil {
		n.l1SafeSub.Unsubscribe()
	}
	// stop polling for L1 finalized-head changes
	if n.l1FinalizedSub != nil {
		n.l1FinalizedSub.Unsubscribe()
	}

	// close L2 driver
	if n.l2Driver != nil {
		if err := n.l2Driver.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close L2 engine driver cleanly: %w", err))
		}
	}

	if n.eventSys != nil {
		n.eventSys.Stop()
	}

	// close L2 engine RPC client
	if n.l2Source != nil {
		n.l2Source.Close()
	}

	// close L1 data source
	if n.l1Source != nil {
		n.l1Source.Close()
	}

	if result == nil { // mark as closed if we successfully fully closed
		n.closed.Store(true)
	}

	// Close metrics and pprof
	if n.pprofService != nil {
		if err := n.pprofService.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close pprof server: %w", err))
		}
	}
	if n.metricsSrv != nil {
		if err := n.metricsSrv.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close metrics server: %w", err))
		}
	}

	return result
}

func (n *OpNode) Stopped() bool {
	return n.closed.Load()
}

func (n *OpNode) HTTPEndpoint() string {
	if n.server == nil {
		return ""
	}
	return fmt.Sprintf("http://%s", n.server.Endpoint())
}

func (n *OpNode) HTTPPort() (int, error) {
	return n.server.Port()
}

func (n *OpNode) getP2PNodeIfEnabled() *p2p.NodeP2P {
	if !n.p2pEnabled() {
		return nil
	}

	n.p2pMu.Lock()
	defer n.p2pMu.Unlock()
	return n.p2pNode
}

func (n *OpNode) SyncStatus() *eth.SyncStatus {
	if n.l2Driver == nil || n.l2Driver.StatusTracker == nil {
		return &eth.SyncStatus{}
	}
	return n.l2Driver.StatusTracker.SyncStatus()
}
