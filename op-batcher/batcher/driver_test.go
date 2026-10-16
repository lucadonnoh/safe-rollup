package batcher

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"github.com/ethereum-optimism/optimism/op-batcher/metrics"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-service/dial"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
	"github.com/ethereum-optimism/optimism/op-service/txmgr"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

type mockL2EndpointProvider struct {
	ethClient       *testutils.MockL2Client
	ethClientErr    error
	rollupClient    *testutils.MockRollupClient
	rollupClientErr error
}

func newEndpointProvider() *mockL2EndpointProvider {
	return &mockL2EndpointProvider{
		ethClient:    new(testutils.MockL2Client),
		rollupClient: new(testutils.MockRollupClient),
	}
}

func (p *mockL2EndpointProvider) PayloadSource(context.Context) (dial.PayloadSource, error) {
	return p.ethClient, p.ethClientErr
}

func (p *mockL2EndpointProvider) RollupClient(context.Context) (dial.RollupClientInterface, error) {
	return p.rollupClient, p.rollupClientErr
}

func (p *mockL2EndpointProvider) Close() {}

const genesisL1Origin = uint64(123)

func setup(t *testing.T, closeAppFn context.CancelCauseFunc) (*BatchSubmitter, *mockL2EndpointProvider) {
	ep := newEndpointProvider()

	cfg := defaultTestRollupConfig
	cfg.Genesis.L1.Number = genesisL1Origin

	if closeAppFn == nil {
		closeAppFn = func(cause error) {
			t.Fatalf("closeAppFn called, batcher hit a critical error: %v", cause)
		}
	}

	return NewBatchSubmitter(DriverSetup{
		closeApp:         closeAppFn,
		Log:              testlog.Logger(t, log.LevelDebug),
		Metr:             metrics.NoopMetrics,
		RollupConfig:     cfg,
		Config:           BatcherConfig{},
		ChannelConfig:    defaultTestChannelConfig(),
		EndpointProvider: ep,
	}), ep
}

func TestBatchSubmitter_SafeL1Origin(t *testing.T) {
	bs, ep := setup(t, nil)

	tests := []struct {
		name                   string
		currentSafeOrigin      uint64
		failsToFetchSyncStatus bool
		expectResult           uint64
		expectErr              bool
	}{
		{
			name:              "ExistingSafeL1Origin",
			currentSafeOrigin: 999,
			expectResult:      999,
		},
		{
			name:              "NoExistingSafeL1OriginUsesGenesis",
			currentSafeOrigin: 0,
			expectResult:      genesisL1Origin,
		},
		{
			name:                   "ErrorFetchingSyncStatus",
			failsToFetchSyncStatus: true,
			expectErr:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.failsToFetchSyncStatus {
				ep.rollupClient.ExpectSyncStatus(&eth.SyncStatus{}, errors.New("failed to fetch sync status"))
			} else {
				ep.rollupClient.ExpectSyncStatus(&eth.SyncStatus{
					LocalSafeL2: eth.L2BlockRef{
						L1Origin: eth.BlockID{
							Number: tt.currentSafeOrigin,
						},
					},
				}, nil)
			}

			id, err := bs.safeL1Origin(context.Background())

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectResult, id.Number)
			}
		})
	}
}

func TestBatchSubmitter_SafeL1Origin_FailsToResolveRollupClient(t *testing.T) {
	bs, ep := setup(t, nil)

	ep.rollupClientErr = errors.New("failed to resolve rollup client")

	_, err := bs.safeL1Origin(context.Background())
	require.Error(t, err)
}

type MockTxQueue struct {
	m sync.Map
}

func (q *MockTxQueue) Send(ref txRef, candidate txmgr.TxCandidate, receiptCh chan txmgr.TxReceipt[txRef]) {
	q.m.Store(ref.id.String(), candidate)
}

func (q *MockTxQueue) Load(id string) txmgr.TxCandidate {
	c, _ := q.m.Load(id)
	return c.(txmgr.TxCandidate)
}

// TestBatchSubmitter_LoadBlocksIntoState_DepositPayload loads a deposit-bearing
// payload through loadBlocksIntoState, covering the L1-info extraction from the
// payload's opaque first transaction (derive.PayloadToBlockRef): payload
// transactions carry raw encodings, so the deposit must decode without
// go-ethereum's typed-transaction support.
func TestBatchSubmitter_LoadBlocksIntoState_DepositPayload(t *testing.T) {
	const blockNumber = uint64(5)
	const blockTimestamp = uint64(1000)

	mkPayload := func(t *testing.T, firstTx []byte) *eth.ExecutionPayloadEnvelope {
		return &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
			BlockHash:    common.Hash{0x01},
			ParentHash:   common.Hash{0x02},
			BlockNumber:  hexutil.Uint64(blockNumber),
			Timestamp:    hexutil.Uint64(blockTimestamp),
			Transactions: []eth.Data{firstTx},
		}}
	}

	t.Run("l1 info deposit first tx", func(t *testing.T) {
		bs, ep := setup(t, nil)
		rng := rand.New(rand.NewSource(789))
		l1Info := testutils.RandomBlockInfo(rng)
		l1InfoTx, err := derive.L1InfoDepositBytes(params.MergedTestChainConfig, eth.SystemConfig{BatcherAddr: common.Address{0x42}}, 3, l1Info)
		require.NoError(t, err)
		ep.ethClient.ExpectPayloadByNumber(blockNumber, mkPayload(t, l1InfoTx), nil)

		require.NoError(t, bs.loadBlocksIntoState(context.Background(), blockNumber, blockNumber, nil))
		ep.ethClient.AssertExpectations(t)
	})

	t.Run("non-deposit first tx rejected", func(t *testing.T) {
		bs, ep := setup(t, nil)
		ep.ethClient.ExpectPayloadByNumber(blockNumber, mkPayload(t, []byte{0x02, 0xc0}), nil)

		err := bs.loadBlocksIntoState(context.Background(), blockNumber, blockNumber, nil)
		require.ErrorContains(t, err, "failed to decode L1 info deposit")
		ep.ethClient.AssertExpectations(t)
	})
}

// A txdata with more than one frame shuts the batcher down through closeApp and returns an error.
func TestBatchSubmitter_PublishInvariants(t *testing.T) {
	var causes []error
	bs, _ := setup(t, func(cause error) { causes = append(causes, cause) })

	err := bs.publishToAltDAAndL1(txData{frames: []frameData{{data: []byte{1}}, {data: []byte{2}}}}, nil, nil, nil)
	require.ErrorContains(t, err, "unexpected number of frames in calldata tx: 2")
	require.Len(t, causes, 1)
	require.ErrorIs(t, causes[0], err)
}
