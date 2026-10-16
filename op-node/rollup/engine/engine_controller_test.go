package engine

import (
	"context"
	"errors"
	"math/big"
	mrand "math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-node/metrics"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/event"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

type staleParentEngine struct {
	testutils.MockEngine
	buildAttempts int
}

func (e *staleParentEngine) ForkchoiceUpdate(context.Context, *eth.ForkchoiceState, *eth.PayloadAttributes) (*eth.ForkchoiceUpdatedResult, error) {
	e.buildAttempts++
	return nil, eth.InputError{Inner: errors.New("missing parent header"), Code: eth.InvalidPayloadAttributes}
}

func TestInvalidPayloadDropsHead(t *testing.T) {
	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), nil, testlog.Logger(t, 0), metrics.NoopMetrics, &rollup.Config{}, &sync.Config{}, &testutils.MockL1Source{}, emitter)

	payload := &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		BlockHash: common.Hash{0x01},
	}}

	emitter.ExpectOnce(PayloadInvalidEvent{})
	emitter.ExpectOnce(ForkchoiceUpdateEvent{})

	// Add an unsafe payload requests a forkchoice update via engine controller
	ec.AddUnsafePayload(context.Background(), payload)

	require.NotNil(t, ec.unsafePayloads.Peek())

	// Mark it invalid; it should be dropped if it matches the queue head
	ec.OnEvent(context.Background(), PayloadInvalidEvent{Envelope: payload})
	require.Nil(t, ec.unsafePayloads.Peek())
}

func TestBuildStartRejectsStaleSequencerParent(t *testing.T) {
	staleParent := eth.L2BlockRef{Hash: common.Hash{0x40}, Number: 40, Time: 80}
	currentUnsafe := eth.L2BlockRef{Hash: common.Hash{0x41}, Number: 41, Time: 82}
	safe := eth.L2BlockRef{Hash: common.Hash{0x38}, Number: 38, Time: 76}
	finalized := eth.L2BlockRef{Hash: common.Hash{0x01}, Number: 1, Time: 2}
	attributes := &derive.AttributesWithParent{
		Attributes: &eth.PayloadAttributes{
			Timestamp:    eth.Uint64Quantity(82),
			Transactions: []eth.Data{{optypes.DepositTxType}},
			NoTxPool:     true,
		},
		Parent: staleParent,
	}

	var emitted []event.Event
	emitter := event.EmitterFunc(func(_ context.Context, ev event.Event) {
		emitted = append(emitted, ev)
	})
	engine := &staleParentEngine{}
	ec := NewEngineController(context.Background(), engine, testlog.Logger(t, 0), metrics.NoopMetrics, &rollup.Config{}, &sync.Config{}, &testutils.MockL1Source{}, emitter)
	ec.SetUnsafeHead(currentUnsafe)
	ec.SetLocalSafeHead(safe)
	ec.SetFinalizedHead(finalized)

	ec.OnEvent(context.Background(), BuildStartEvent{Attributes: attributes})
	for _, ev := range emitted {
		if invalid, ok := ev.(BuildInvalidEvent); ok {
			ec.OnEvent(context.Background(), invalid)
		}
	}

	require.Zero(t, engine.buildAttempts, "must not ask the engine to build on an orphaned parent")
	require.Equal(t, []event.Event{ForkchoiceUpdateEvent{
		UnsafeL2Head:    currentUnsafe,
		SafeL2Head:      safe,
		FinalizedL2Head: finalized,
	}}, emitted)
}

type sealingEngine struct {
	testutils.MockEngine
	envelope    *eth.ExecutionPayloadEnvelope
	getPayloads int
	newPayloads int
}

func (e *sealingEngine) GetPayload(context.Context, eth.PayloadInfo) (*eth.ExecutionPayloadEnvelope, error) {
	e.getPayloads++
	return e.envelope, nil
}

func (e *sealingEngine) NewPayload(context.Context, *eth.ExecutionPayload, *common.Hash) (*eth.PayloadStatusV1, error) {
	e.newPayloads++
	return &eth.PayloadStatusV1{Status: eth.ExecutionValid}, nil
}

// TestSealBuildRejectsStaleParent covers the direct-call guard: a block sealed
// on a parent the chain has moved past must not be handed to the caller, which
// would commit and gossip a sibling of the current head.
func TestSealBuildRejectsStaleParent(t *testing.T) {
	cfg, parent, sealed, envelope := buildSimpleCfgAndPayload(t)
	movedOn := eth.L2BlockRef{Hash: common.Hash{0x41}, Number: sealed.Number, Time: sealed.Time}

	var emitted []event.Event
	emitter := event.EmitterFunc(func(_ context.Context, ev event.Event) { emitted = append(emitted, ev) })
	engine := &sealingEngine{envelope: envelope}
	ec := NewEngineController(context.Background(), engine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{}, &testutils.MockL1Source{}, emitter)
	ec.SetUnsafeHead(movedOn)

	result, err := ec.SealBuild(context.Background(), eth.PayloadInfo{ID: eth.PayloadID{0x01}}, time.Now())
	require.ErrorIs(t, err, ErrStaleBuild)
	require.Nil(t, result)
	require.Equal(t, 1, engine.getPayloads, "the job can only be checked once it is sealed")
	require.Len(t, emitted, 1, "a forkchoice update is requested so the caller can re-plan")
	require.IsType(t, ForkchoiceUpdateEvent{}, emitted[0])

	// On the matching head the same seal succeeds and stays event-free.
	emitted = nil
	ec.SetUnsafeHead(parent)
	result, err = ec.SealBuild(context.Background(), eth.PayloadInfo{ID: eth.PayloadID{0x01}}, time.Now())
	require.NoError(t, err)
	require.Equal(t, sealed.Hash, result.Ref.Hash)
	require.Empty(t, emitted, "the direct seal path emits no events on success")
}

// TestProcessPayloadRejectsStaleParent covers the direct-call guard: inserting a
// payload that no longer extends the unsafe head would reorg the head backwards.
func TestProcessPayloadRejectsStaleParent(t *testing.T) {
	cfg, _, sealed, envelope := buildSimpleCfgAndPayload(t)
	movedOn := eth.L2BlockRef{Hash: common.Hash{0x41}, Number: sealed.Number, Time: sealed.Time}

	var emitted []event.Event
	emitter := event.EmitterFunc(func(_ context.Context, ev event.Event) { emitted = append(emitted, ev) })
	engine := &sealingEngine{envelope: envelope}
	ec := NewEngineController(context.Background(), engine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{}, &testutils.MockL1Source{}, emitter)
	ec.SetUnsafeHead(movedOn)

	err := ec.ProcessPayload(context.Background(), envelope, sealed, time.Now())
	require.ErrorIs(t, err, ErrStaleBuild)
	require.Zero(t, engine.newPayloads, "the stale payload must not reach the engine")
	require.Equal(t, movedOn, ec.UnsafeL2Head(), "the head must not be reorged backwards")
	require.Len(t, emitted, 1)
	require.IsType(t, ForkchoiceUpdateEvent{}, emitted[0])
}

// buildSimpleCfgAndPayload creates a minimal rollup config and a valid payload (A1) on top of A0.
func buildSimpleCfgAndPayload(t *testing.T) (*rollup.Config, eth.L2BlockRef, eth.L2BlockRef, *eth.ExecutionPayloadEnvelope) {
	t.Helper()
	rng := mrand.New(mrand.NewSource(1234))
	refA := testutils.RandomBlockRef(rng)

	refA0 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         0,
		ParentHash:     common.Hash{},
		Time:           refA.Time,
		L1Origin:       refA.ID(),
		SequenceNumber: 0,
	}

	cfg := &rollup.Config{
		Genesis: rollup.Genesis{
			L1:     refA.ID(),
			L2:     refA0.ID(),
			L2Time: refA0.Time,
			SystemConfig: eth.SystemConfig{
				BatcherAddr: common.Address{42},
				Overhead:    [32]byte{123},
				Scalar:      eth.EncodeScalar(eth.EcotoneScalars{BaseFeeScalar: 42}),
				GasLimit:    20_000_000,
			},
		},
		BlockTime:     1,
		SeqWindowSize: 2,
	}

	refA1 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         refA0.Number + 1,
		ParentHash:     refA0.Hash,
		Time:           refA0.Time + cfg.BlockTime,
		L1Origin:       refA.ID(),
		SequenceNumber: 1,
	}

	// Populate necessary L1 info fields
	aL1Info := &testutils.MockBlockInfo{
		InfoParentHash:  refA.ParentHash,
		InfoNum:         refA.Number,
		InfoTime:        refA.Time,
		InfoHash:        refA.Hash,
		InfoBaseFee:     big.NewInt(1),
		InfoBlobBaseFee: big.NewInt(1),
		InfoReceiptRoot: gethtypes.EmptyRootHash,
		InfoRoot:        testutils.RandomHash(rng),
		InfoGasUsed:     rng.Uint64(),
	}
	a1L1Info, err := derive.L1InfoDepositBytes(params.SepoliaChainConfig, cfg.Genesis.SystemConfig, refA1.SequenceNumber, aL1Info)
	require.NoError(t, err)

	payloadA1 := &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		ParentHash:   refA1.ParentHash,
		BlockNumber:  eth.Uint64Quantity(refA1.Number),
		Timestamp:    eth.Uint64Quantity(refA1.Time),
		BlockHash:    refA1.Hash,
		Transactions: []eth.Data{a1L1Info},
	}}
	return cfg, refA0, refA1, payloadA1
}

func TestOnUnsafePayload_EnqueueEmit(t *testing.T) {
	cfg, _, _, payloadA1 := buildSimpleCfgAndPayload(t)

	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), nil, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{}, &testutils.MockL1Source{}, emitter)

	emitter.ExpectOnce(PayloadInvalidEvent{})
	emitter.ExpectOnce(ForkchoiceUpdateEvent{})

	ec.AddUnsafePayload(context.Background(), payloadA1)

	got := ec.unsafePayloads.Peek()
	require.NotNil(t, got)
	require.Equal(t, payloadA1, got)
}

func TestOnForkchoiceUpdate_ProcessRetryAndPop(t *testing.T) {
	cfg, refA0, refA1, payloadA1 := buildSimpleCfgAndPayload(t)

	emitter := &testutils.MockEmitter{}
	mockEngine := &testutils.MockEngine{}
	cl := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.CLSync}, &testutils.MockL1Source{}, emitter)

	// queue payload A1
	emitter.ExpectOnceType("UnsafeUpdateEvent")
	emitter.ExpectOnceType("PayloadInvalidEvent")
	emitter.ExpectOnceType("ForkchoiceUpdateEvent")
	emitter.ExpectOnceType("ForkchoiceUpdateEvent")
	cl.AddUnsafePayload(context.Background(), payloadA1)

	// applicable forkchoice -> process once
	mockEngine.ExpectGetPayload(eth.PayloadID{}, payloadA1, nil)
	mockEngine.ExpectNewPayload(payloadA1.ExecutionPayload, nil, &eth.PayloadStatusV1{Status: eth.ExecutionValid}, nil)
	mockEngine.ExpectForkchoiceUpdate(&eth.ForkchoiceState{HeadBlockHash: refA1.Hash, SafeBlockHash: common.Hash{}, FinalizedBlockHash: common.Hash{}}, nil, &eth.ForkchoiceUpdatedResult{PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionValid}}, nil)
	cl.OnEvent(context.Background(), ForkchoiceUpdateEvent{UnsafeL2Head: refA0, SafeL2Head: refA0, FinalizedL2Head: refA0})
	require.NotNil(t, cl.unsafePayloads.Peek(), "should not pop yet")

	// same forkchoice -> retry
	cl.OnEvent(context.Background(), ForkchoiceUpdateEvent{UnsafeL2Head: refA0, SafeL2Head: refA0, FinalizedL2Head: refA0})
	require.NotNil(t, cl.unsafePayloads.Peek(), "still pending")

	// after applied (unsafe head == A1) -> pop
	cl.OnEvent(context.Background(), ForkchoiceUpdateEvent{UnsafeL2Head: refA1, SafeL2Head: refA0, FinalizedL2Head: refA0})
	require.Nil(t, cl.unsafePayloads.Peek())
}

func TestPeekUnsafePayload(t *testing.T) {
	cfg, _, _, payloadA1 := buildSimpleCfgAndPayload(t)

	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), nil, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.CLSync}, &testutils.MockL1Source{}, emitter)

	// empty -> zero
	_, ref := ec.PeekUnsafePayload()
	require.Equal(t, eth.L2BlockRef{}, ref)

	// queue -> returns derived ref
	_ = ec.unsafePayloads.Push(payloadA1)
	want, err := derive.PayloadToBlockRef(cfg, payloadA1.ExecutionPayload)
	require.NoError(t, err)

	_, ref = ec.PeekUnsafePayload()
	require.Equal(t, want, ref)
}

func TestPeekUnsafePayload_OnDeriveErrorReturnsZero(t *testing.T) {
	// missing L1-info in txs will cause derive error
	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), nil, testlog.Logger(t, 0), metrics.NoopMetrics, &rollup.Config{}, &sync.Config{SyncMode: sync.CLSync}, &testutils.MockL1Source{}, emitter)

	bad := &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{BlockNumber: 1, BlockHash: common.Hash{0xaa}}}
	_ = ec.unsafePayloads.Push(bad)
	_, ref := ec.PeekUnsafePayload()
	require.Equal(t, eth.L2BlockRef{}, ref)
}

func TestInvalidPayloadForNonHead_NoDrop(t *testing.T) {
	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), nil, testlog.Logger(t, 0), metrics.NoopMetrics, &rollup.Config{}, &sync.Config{SyncMode: sync.CLSync}, &testutils.MockL1Source{}, emitter)

	// Head payload (lower block number)
	head := &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		BlockNumber: 1,
		BlockHash:   common.Hash{0x01},
	}}
	// Non-head payload (higher block number)
	other := &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		BlockNumber: 2,
		BlockHash:   common.Hash{0x02},
	}}

	emitter.ExpectOnce(PayloadInvalidEvent{})
	emitter.ExpectOnce(ForkchoiceUpdateEvent{})
	ec.AddUnsafePayload(context.Background(), head)

	emitter.ExpectOnce(PayloadInvalidEvent{})
	emitter.ExpectOnce(ForkchoiceUpdateEvent{})
	ec.AddUnsafePayload(context.Background(), other)

	// Invalidate non-head should not drop head
	ec.OnEvent(context.Background(), PayloadInvalidEvent{Envelope: other})
	require.Equal(t, 2, ec.unsafePayloads.Len())
	require.Equal(t, head, ec.unsafePayloads.Peek())
}

// note: nil-envelope behavior is not tested to match current implementation

// TestInitializeUnknowns_SetsLocalSafeHead_Regression is a regression test for a bug
// where initializeUnknowns would fail to properly initialize localSafeHead on restart.
//
// The bug occurred because:
// 1. SafeL2Head() returns localSafeHead when no supervisor/superAuthority is enabled
// 2. But SetSafeHead() was setting deprecatedSafeHead, not localSafeHead
// 3. So after loading the safe head from EL, localSafeHead remained zero
//
// Symptom: After restarting op-node + op-reth, safe head never advances.
// The logs showed: "Set initial local-safe block ref to match cross-safe" local_safe=0x0000...0:0
func TestInitializeUnknowns_SetsLocalSafeHead_Regression(t *testing.T) {
	cfg := &rollup.Config{
		Genesis: rollup.Genesis{
			L2Time: 1000,
		},
		BlockTime: 2,
	}

	// Simulate restart scenario: EL has persisted state, CL starts fresh
	unsafeRef := eth.L2BlockRef{Hash: common.Hash{0xaa}, Number: 100}
	safeRef := eth.L2BlockRef{Hash: common.Hash{0xbb}, Number: 80}
	finalizedRef := eth.L2BlockRef{Hash: common.Hash{0xcc}, Number: 50}

	mockEngine := &testutils.MockEngine{}
	emitter := &testutils.MockEmitter{}

	// No supervisor, no superAuthority - the standard non-interop case
	ec := NewEngineController(
		context.Background(),
		mockEngine,
		testlog.Logger(t, 0),
		metrics.NoopMetrics,
		cfg,
		&sync.Config{},
		&testutils.MockL1Source{},
		emitter,
	)

	// Verify initial state is zero (simulating fresh CL start)
	require.Equal(t, eth.L2BlockRef{}, ec.localSafeHead, "localSafeHead should start as zero")

	// Mock EL returning persisted state (simulating restart with existing EL data)
	mockEngine.ExpectL2BlockRefByLabel(eth.Unsafe, unsafeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Finalized, finalizedRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Safe, safeRef, nil)

	// Expect forkchoice update after initialization
	emitter.ExpectOnceType("ForkchoiceUpdateEvent")
	expectedFC := eth.ForkchoiceState{
		HeadBlockHash:      unsafeRef.Hash,
		SafeBlockHash:      safeRef.Hash, // Should use localSafeHead, not zero!
		FinalizedBlockHash: finalizedRef.Hash,
	}
	mockEngine.ExpectForkchoiceUpdate(&expectedFC, nil, &eth.ForkchoiceUpdatedResult{
		PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionValid},
	}, nil)

	// Run initialization via tryUpdateEngineInternal
	err := ec.tryUpdateEngineInternal(context.Background())
	require.NoError(t, err)

	// THE KEY ASSERTION: localSafeHead must be properly set from EL's safe label
	require.Equal(t, safeRef, ec.localSafeHead,
		"localSafeHead must be initialized from EL safe label, not left as zero")

	// Also verify SafeL2Head() returns the correct value (uses localSafeHead in non-supervisor mode)
	require.Equal(t, safeRef, ec.SafeL2Head(),
		"SafeL2Head() must return the initialized localSafeHead")

	mockEngine.AssertExpectations(t)
}

// TestInitializeUnknowns_ELSync_FinalizedNotFound is a regression test for #20649.
// During initial ELSync the engine may not yet have a finalized (or safe) block,
// because finality is a CL-driven concept and the verifier op-node hasn't issued
// a finalized FCU yet. Before the fix, the eth.Finalized lookup returning
// ethereum.NotFound made initializeUnknowns fail and tryUpdateEngineInternal
// return "Engine temporary error: ... finalized block not found" once per
// derivation tick — hundreds of warnings per failed run, crowding out the real
// signal. The fix treats NotFound on eth.Finalized as "no finalized block yet"
// in ELSync mode, mirroring the existing eth.Safe NotFound fallback. CLSync
// mode preserves the prior error behavior.
func TestInitializeUnknowns_ELSync_FinalizedNotFound(t *testing.T) {
	cfg := &rollup.Config{
		Genesis:   rollup.Genesis{L2Time: 1000},
		BlockTime: 2,
	}

	// Engine has produced an unsafe head from snap-sync, but has no finalized
	// or safe block yet — the standard mid-ELSync state.
	unsafeRef := eth.L2BlockRef{Hash: common.Hash{0xaa}, Number: 100}

	mockEngine := &testutils.MockEngine{}
	emitter := &testutils.MockEmitter{}

	ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.ELSync}, &testutils.MockL1Source{}, emitter)
	// Match the syncStatus the engine is in during snap-sync, so
	// isEngineInitialELSyncing() reports true and the FCU below omits the
	// FinalizedBlockHash.
	ec.syncStatus = syncStatusStartedEL

	mockEngine.ExpectL2BlockRefByLabel(eth.Unsafe, unsafeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Finalized, eth.L2BlockRef{}, ethereum.NotFound)
	mockEngine.ExpectL2BlockRefByLabel(eth.Safe, eth.L2BlockRef{}, ethereum.NotFound)

	// Expect a single FCU with the unsafe head and zero safe/finalized hashes
	// (FinalizedBlockHash is omitted because isEngineInitialELSyncing is true).
	emitter.ExpectOnceType("ForkchoiceUpdateEvent")
	expectedFC := eth.ForkchoiceState{
		HeadBlockHash: unsafeRef.Hash,
		SafeBlockHash: common.Hash{},
	}
	mockEngine.ExpectForkchoiceUpdate(&expectedFC, nil, &eth.ForkchoiceUpdatedResult{
		PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionSyncing},
	}, nil)

	err := ec.tryUpdateEngineInternal(context.Background())
	require.NoError(t, err, "ELSync + NotFound on finalized/safe should not error")

	require.Equal(t, unsafeRef, ec.unsafeHead, "unsafeHead should be populated")
	require.Equal(t, eth.L2BlockRef{}, ec.FinalizedHead(),
		"FinalizedHead must remain zero when engine reports NotFound during ELSync")
	require.Equal(t, eth.L2BlockRef{}, ec.localSafeHead,
		"localSafeHead must remain zero when both finalized and safe are NotFound")

	mockEngine.AssertExpectations(t)
}

// TestInitializeUnknowns_CLSync_FinalizedNotFound_StillErrors verifies the
// NotFound fallback is scoped to ELSync mode: in CLSync the engine is expected
// to have a valid finalized block after the initial reset, so propagating the
// error preserves the prior behavior.
func TestInitializeUnknowns_CLSync_FinalizedNotFound_StillErrors(t *testing.T) {
	cfg := &rollup.Config{
		Genesis:   rollup.Genesis{L2Time: 1000},
		BlockTime: 2,
	}

	unsafeRef := eth.L2BlockRef{Hash: common.Hash{0xaa}, Number: 100}

	mockEngine := &testutils.MockEngine{}
	emitter := &testutils.MockEmitter{}

	ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.CLSync}, &testutils.MockL1Source{}, emitter)

	mockEngine.ExpectL2BlockRefByLabel(eth.Unsafe, unsafeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Finalized, eth.L2BlockRef{}, ethereum.NotFound)

	err := ec.tryUpdateEngineInternal(context.Background())
	require.Error(t, err, "CLSync mode should still return error on NotFound")
	require.ErrorContains(t, err, "failed to load finalized head")

	mockEngine.AssertExpectations(t)
}

// TestConsolidation_BatchesFCUPerL1Block verifies that on the consolidation path,
// PromoteSafe does NOT trigger an FCU per L2 block. Instead, a single FCU is sent
// when DeriverL1StatusEvent fires (at L1 origin transitions).
func TestConsolidation_BatchesFCUPerL1Block(t *testing.T) {
	rng := mrand.New(mrand.NewSource(4321))
	l1A := testutils.RandomBlockRef(rng)
	l1B := testutils.RandomBlockRef(rng)
	l1B.Number = l1A.Number + 1
	l1B.ParentHash = l1A.Hash

	genesis := eth.L2BlockRef{
		Hash: testutils.RandomHash(rng), Number: 0,
		ParentHash: common.Hash{}, Time: l1A.Time,
		L1Origin: l1A.ID(), SequenceNumber: 0,
	}
	block1 := eth.L2BlockRef{
		Hash: testutils.RandomHash(rng), Number: 1,
		ParentHash: genesis.Hash, Time: l1A.Time + 2,
		L1Origin: l1A.ID(), SequenceNumber: 1,
	}
	block2 := eth.L2BlockRef{
		Hash: testutils.RandomHash(rng), Number: 2,
		ParentHash: block1.Hash, Time: l1A.Time + 4,
		L1Origin: l1A.ID(), SequenceNumber: 2,
	}
	block3 := eth.L2BlockRef{
		Hash: testutils.RandomHash(rng), Number: 3,
		ParentHash: block2.Hash, Time: l1A.Time + 6,
		L1Origin: l1A.ID(), SequenceNumber: 3,
	}

	cfg := &rollup.Config{}
	mockEngine := &testutils.MockEngine{}
	emitter := &testutils.MockEmitter{}

	ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{}, &testutils.MockL1Source{}, emitter)

	// Initial state: unsafe chain is ahead, safe at genesis.
	// Set all heads directly to avoid initializeUnknowns engine calls.
	ec.unsafeHead = block3
	ec.localSafeHead = genesis
	ec.pendingSafeHead = genesis
	ec.localFinalizedHead = genesis

	// Allow any emitted events
	emitter.Mock.On("Emit", mock.Anything).Maybe()

	// Simulate consolidation path: local-safe and cross-safe advance per-block
	// (as TryUpdateLocalSafe + LocalSafeUpdateEvent → PromoteSafe does).
	// PromoteSafe no longer calls tryUpdateEngine. tryUpdateEngine compares
	// against lastForkchoice — no FCU per block because only SafeBlockHash changes.
	for _, blk := range []eth.L2BlockRef{block1, block2, block3} {
		ec.SetLocalSafeHead(blk)
		ec.SetPendingSafeL2Head(blk)
		ec.PromoteSafe(context.Background(), blk, l1A)
	}

	// Verify: no ForkchoiceUpdate was called on the mock engine.
	// If FCU had been called, mockEngine would panic on an unexpected call.
	mockEngine.AssertExpectations(t)

	// Verify cross-safe advanced correctly despite no FCU
	require.Equal(t, block3.Hash, ec.SafeL2Head().Hash, "safe should advance per-block")
	require.Equal(t, block3.Hash, ec.localSafeHead.Hash, "local-safe should advance per-block")

	// Now simulate DeriverL1StatusEvent (L1 origin transition).
	// This should trigger exactly one FCU with the latest safe head.
	// In non-interop mode, SafeL2Head() returns localSafeHead.
	mockEngine.ExpectForkchoiceUpdate(
		&eth.ForkchoiceState{
			HeadBlockHash:      block3.Hash,
			SafeBlockHash:      block3.Hash, // localSafeHead = block3
			FinalizedBlockHash: genesis.Hash,
		}, nil,
		&eth.ForkchoiceUpdatedResult{PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionValid}}, nil,
	)

	ec.OnEvent(context.Background(), derive.DeriverL1StatusEvent{
		Origin: l1B,
	})

	// Verify exactly one FCU was sent
	mockEngine.AssertExpectations(t)
}

// recordingOriginResetter counts ResetOrigins calls. A non-nil resetter is what marks an
// EngineController as a sequencer (wired only when SequencerEnabled), which is the
// discriminator the follow-source divergence branch uses.
type recordingOriginResetter struct{ resets int }

func (r *recordingOriginResetter) ResetOrigins() { r.resets++ }

// TestTryUpdateEngine_SyncingInCLModeTriggersReset tests that when the EL returns SYNCING
// during a forkchoice update in CL-sync mode (e.g. after an EL restart), the engine controller
// triggers a reset to re-discover the EL's actual chain state.
func TestTryUpdateEngine_SyncingInCLModeTriggersReset(t *testing.T) {
	cfg := &rollup.Config{
		Genesis: rollup.Genesis{
			L2Time: 1000,
		},
		BlockTime: 2,
	}

	mockEngine := &testutils.MockEngine{}
	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.CLSync}, &testutils.MockL1Source{}, emitter)

	// Set up valid internal state so initializeUnknowns succeeds
	unsafeRef := eth.L2BlockRef{Hash: common.Hash{0xaa}, Number: 100}
	safeRef := eth.L2BlockRef{Hash: common.Hash{0xbb}, Number: 80}
	finalRef := eth.L2BlockRef{Hash: common.Hash{0xcc}, Number: 50}

	ec.unsafeHead = unsafeRef
	ec.SetLocalSafeHead(safeRef)
	ec.SetFinalizedHead(finalRef)

	// Mock initializeUnknowns calls
	mockEngine.ExpectL2BlockRefByLabel(eth.Unsafe, unsafeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Safe, safeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Finalized, finalRef, nil)

	// Mock ForkchoiceUpdate to return SYNCING (simulating EL restart)
	mockEngine.ExpectForkchoiceUpdate(
		&eth.ForkchoiceState{
			HeadBlockHash:      unsafeRef.Hash,
			SafeBlockHash:      safeRef.Hash,
			FinalizedBlockHash: finalRef.Hash,
		},
		nil,
		&eth.ForkchoiceUpdatedResult{
			PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionSyncing},
		},
		nil,
	)

	// Call tryUpdateEngineInternal - should return a reset error
	err := ec.tryUpdateEngineInternal(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, derive.ErrReset), "expected reset error, got: %v", err)
	require.Contains(t, err.Error(), "unexpected status")
}

// TestTryUpdateEngine_SyncingInELSyncModeIsAccepted tests that SYNCING is accepted
// in EL-sync mode (existing behavior preserved).
func TestTryUpdateEngine_SyncingInELSyncModeIsAccepted(t *testing.T) {
	cfg := &rollup.Config{
		Genesis: rollup.Genesis{
			L2Time: 1000,
		},
		BlockTime: 2,
	}

	mockEngine := &testutils.MockEngine{}
	emitter := &testutils.MockEmitter{}
	ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.ELSync}, &testutils.MockL1Source{}, emitter)

	// Set up valid internal state
	unsafeRef := eth.L2BlockRef{Hash: common.Hash{0xaa}, Number: 100}
	safeRef := eth.L2BlockRef{Hash: common.Hash{0xbb}, Number: 80}
	finalRef := eth.L2BlockRef{Hash: common.Hash{0xcc}, Number: 50}

	ec.unsafeHead = unsafeRef
	ec.SetLocalSafeHead(safeRef)
	ec.SetFinalizedHead(finalRef)

	// Mock initializeUnknowns calls
	mockEngine.ExpectL2BlockRefByLabel(eth.Unsafe, unsafeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Safe, safeRef, nil)
	mockEngine.ExpectL2BlockRefByLabel(eth.Finalized, finalRef, nil)

	// Mock ForkchoiceUpdate to return SYNCING
	mockEngine.ExpectForkchoiceUpdate(
		&eth.ForkchoiceState{
			HeadBlockHash: unsafeRef.Hash,
			SafeBlockHash: safeRef.Hash,
			// we omit FinalizedBlockHash because we expect it to NOT be set in EL sync mode
		},
		nil,
		&eth.ForkchoiceUpdatedResult{
			PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionSyncing},
		},
		nil,
	)

	// Call tryUpdateEngineInternal - should succeed in EL-sync mode
	err := ec.tryUpdateEngineInternal(context.Background())
	require.NoError(t, err)
}
