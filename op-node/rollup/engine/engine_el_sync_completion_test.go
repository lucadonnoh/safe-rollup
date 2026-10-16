// Tests for insertUnsafePayload in EL-sync mode: whether EL sync starts or is skipped, and the
// safe/finalized head decision at completion (offset retraction, preserving / trimming / clearing
// the safedb).
package engine

import (
	"context"
	"math/big"
	mrand "math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/optimism/op-node/metrics"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/event"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

type discardEmitter struct{}

func (discardEmitter) Emit(context.Context, event.Event) {}

// buildELSyncTipChain returns genesis L2 b0 .. b3 (tip) with BlockTime=2 and a valid payload for b3.
func buildELSyncTipChain(t *testing.T) (*rollup.Config, eth.L2BlockRef, eth.L2BlockRef, eth.L2BlockRef, eth.L2BlockRef, *eth.ExecutionPayloadEnvelope) {
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
		BlockTime:     2,
		SeqWindowSize: 2,
	}
	refA1 := eth.L2BlockRef{
		Hash:           common.Hash{'1'},
		Number:         1,
		ParentHash:     refA0.Hash,
		Time:           refA0.Time + cfg.BlockTime,
		L1Origin:       refA.ID(),
		SequenceNumber: 1,
	}
	refA2 := eth.L2BlockRef{
		Hash:           common.Hash{'2'},
		Number:         2,
		ParentHash:     refA1.Hash,
		Time:           refA1.Time + cfg.BlockTime,
		L1Origin:       refA.ID(),
		SequenceNumber: 2,
	}
	refA3 := eth.L2BlockRef{
		Hash:           common.Hash{'3'},
		Number:         3,
		ParentHash:     refA2.Hash,
		Time:           refA2.Time + cfg.BlockTime,
		L1Origin:       refA.ID(),
		SequenceNumber: 3,
	}
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
	l1Bytes, err := derive.L1InfoDepositBytes(params.SepoliaChainConfig, cfg.Genesis.SystemConfig, refA3.SequenceNumber, aL1Info)
	require.NoError(t, err)
	payload := &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		ParentHash:   refA3.ParentHash,
		BlockNumber:  eth.Uint64Quantity(refA3.Number),
		Timestamp:    eth.Uint64Quantity(refA3.Time),
		BlockHash:    refA3.Hash,
		Transactions: []eth.Data{l1Bytes},
	}}
	return cfg, refA0, refA1, refA2, refA3, payload
}

func TestInsertUnsafePayload_ELSync_offsetDerived(t *testing.T) {
	cfg, refA0, _, _, refA3, payload := buildELSyncTipChain(t)

	tests := []struct {
		name   string
		offset time.Duration
		want   eth.L2BlockRef // local safe + finalized
		stub   func(eng *testutils.MockEngine)
	}{
		{
			name:   "zero sets safe and finalized to tip",
			offset: 0,
			want:   refA3,
			stub: func(eng *testutils.MockEngine) {
				eng.ExpectL2BlockRefByLabel(eth.Finalized, refA0, nil)
				eng.ExpectNewPayload(payload.ExecutionPayload, nil, &eth.PayloadStatusV1{Status: eth.ExecutionValid}, nil)
				eng.ExpectForkchoiceUpdate(&eth.ForkchoiceState{
					HeadBlockHash:      refA3.Hash,
					SafeBlockHash:      refA3.Hash,
					FinalizedBlockHash: refA3.Hash,
				}, nil, &eth.ForkchoiceUpdatedResult{PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionValid}}, nil)
			},
		},
		{
			name:   "non-zero retracts safe and finalized by ceil(offset/BlockTime)",
			offset: 5 * time.Second,
			want:   refA0,
			stub: func(eng *testutils.MockEngine) {
				eng.ExpectL2BlockRefByLabel(eth.Finalized, refA0, nil)
				eng.ExpectNewPayload(payload.ExecutionPayload, nil, &eth.PayloadStatusV1{Status: eth.ExecutionValid}, nil)
				eng.ExpectL2BlockRefByNumber(0, refA0, nil)
				eng.ExpectForkchoiceUpdate(&eth.ForkchoiceState{
					HeadBlockHash:      refA3.Hash,
					SafeBlockHash:      refA0.Hash,
					FinalizedBlockHash: refA0.Hash,
				}, nil, &eth.ForkchoiceUpdatedResult{PayloadStatus: eth.PayloadStatusV1{Status: eth.ExecutionValid}}, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockEngine := &testutils.MockEngine{}
			tt.stub(mockEngine)
			ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.ELSync, OffsetELSafe: tt.offset}, &testutils.MockL1Source{}, discardEmitter{})

			err := ec.InsertUnsafePayload(context.Background(), payload, refA3)
			require.NoError(t, err)
			require.Equal(t, refA3, ec.unsafeHead)
			require.Equal(t, tt.want, ec.localSafeHead)
			require.Equal(t, tt.want, ec.FinalizedHead())
		})
	}
}

// Without SupportsPostFinalizationELSync, a non-genesis finalized head skips EL sync: the
// controller goes straight to CL sync and drops the payload without touching the engine.
func TestInsertUnsafePayload_ELSync_skipsWhenFinalized(t *testing.T) {
	cfg, _, refA1, refA2, refA3, payload := buildELSyncTipChain(t)

	mockEngine := &testutils.MockEngine{}
	// No NewPayload or ForkchoiceUpdate expectation: the mock fails the test on either call.
	mockEngine.ExpectL2BlockRefByLabel(eth.Finalized, refA1, nil)

	ec := NewEngineController(context.Background(), mockEngine, testlog.Logger(t, 0), metrics.NoopMetrics, cfg, &sync.Config{SyncMode: sync.ELSync}, &testutils.MockL1Source{}, discardEmitter{})
	ec.SetUnsafeHead(refA2)

	require.NoError(t, ec.InsertUnsafePayload(context.Background(), payload, refA3))
	mockEngine.AssertExpectations(t)

	require.Equal(t, syncStatusFinishedEL, ec.syncStatus)
	require.Equal(t, refA2, ec.unsafeHead)
}

// recordingCrossUpdateHandler captures the last cross-safe notification.
type recordingCrossUpdateHandler struct {
	crossSafe eth.L2BlockRef
	localSafe eth.L2BlockRef
	calls     int
}

func (r *recordingCrossUpdateHandler) OnCrossSafeUpdate(_ context.Context, crossSafe, localSafe eth.L2BlockRef) {
	r.crossSafe = crossSafe
	r.localSafe = localSafe
	r.calls++
}
