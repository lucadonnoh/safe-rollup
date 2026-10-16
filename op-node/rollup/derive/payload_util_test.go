package derive

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"

	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
)

// TestPayloadToBlockRef covers the L1-info extraction from a payload's opaque
// first transaction. Payload transactions carry raw encodings, so the L1-info
// deposit must decode without go-ethereum's typed-transaction support.
func TestPayloadToBlockRef(t *testing.T) {
	cfg := &rollup.Config{
		BlockTime:              2,
		L1ChainID:              big.NewInt(101),
		L2ChainID:              big.NewInt(102),
		DepositContractAddress: common.Address{0xbb},
		L1SystemConfigAddress:  common.Address{0xcc},
		Genesis: rollup.Genesis{
			L1: eth.BlockID{Hash: common.Hash{0xaa}, Number: 9},
			L2: eth.BlockID{Hash: common.Hash{0xbb}, Number: 90},
		},
	}
	const blockTimestamp = uint64(1000)
	const seqNumber = uint64(7)

	rng := rand.New(rand.NewSource(42))
	l1Info := testutils.RandomBlockInfo(rng)
	sysCfg := eth.SystemConfig{BatcherAddr: common.Address{0x42}}
	l1InfoTx, err := L1InfoDepositBytes(params.MergedTestChainConfig, sysCfg, seqNumber, l1Info)
	require.NoError(t, err)

	payload := &eth.ExecutionPayload{
		BlockHash:    common.Hash{0x01},
		ParentHash:   common.Hash{0x02},
		BlockNumber:  hexutil.Uint64(91),
		Timestamp:    hexutil.Uint64(blockTimestamp),
		Transactions: []eth.Data{l1InfoTx},
	}

	t.Run("deposit payload", func(t *testing.T) {
		ref, err := PayloadToBlockRef(cfg, payload)
		require.NoError(t, err)
		require.Equal(t, eth.L2BlockRef{
			Hash:           payload.BlockHash,
			Number:         uint64(payload.BlockNumber),
			ParentHash:     payload.ParentHash,
			Time:           blockTimestamp,
			L1Origin:       eth.BlockID{Hash: l1Info.Hash(), Number: l1Info.NumberU64()},
			SequenceNumber: seqNumber,
		}, ref)
	})

	t.Run("genesis payload", func(t *testing.T) {
		genesisPayload := &eth.ExecutionPayload{
			BlockHash:   cfg.Genesis.L2.Hash,
			BlockNumber: hexutil.Uint64(cfg.Genesis.L2.Number),
		}
		ref, err := PayloadToBlockRef(cfg, genesisPayload)
		require.NoError(t, err)
		require.Equal(t, cfg.Genesis.L1, ref.L1Origin)
		require.Zero(t, ref.SequenceNumber)

		genesisPayload.BlockHash = common.Hash{0xde, 0xad}
		_, err = PayloadToBlockRef(cfg, genesisPayload)
		require.ErrorContains(t, err, "expected L2 genesis hash")
	})

	t.Run("errors", func(t *testing.T) {
		mut := func(mut func(p *eth.ExecutionPayload)) *eth.ExecutionPayload {
			p := *payload
			mut(&p)
			return &p
		}
		for _, tc := range []struct {
			name    string
			payload *eth.ExecutionPayload
			errStr  string
		}{
			{"no txs", mut(func(p *eth.ExecutionPayload) { p.Transactions = nil }), "missing L1 info deposit"},
			{"empty first tx", mut(func(p *eth.ExecutionPayload) { p.Transactions = []eth.Data{{}} }), "failed to decode L1 info deposit"},
			{"non-deposit first tx", mut(func(p *eth.ExecutionPayload) { p.Transactions = []eth.Data{{0x02, 0xc0}} }), "failed to decode L1 info deposit"},
			{"truncated deposit", mut(func(p *eth.ExecutionPayload) { p.Transactions = []eth.Data{{optypes.DepositTxType}} }), "failed to decode L1 info deposit"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := PayloadToBlockRef(cfg, tc.payload)
				require.ErrorContains(t, err, tc.errStr)
			})
		}
	})
}
