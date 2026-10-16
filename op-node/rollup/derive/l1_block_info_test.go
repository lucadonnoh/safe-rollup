package derive

import (
	crand "crypto/rand"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"

	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
)

var (
	MockDepositContractAddr               = common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeef00000000")
	_                       eth.BlockInfo = (*testutils.MockBlockInfo)(nil)
)

type infoTest struct {
	name    string
	mkInfo  func(rng *rand.Rand) *testutils.MockBlockInfo
	mkL1Cfg func(rng *rand.Rand, l1Info eth.BlockInfo) eth.SystemConfig
	seqNr   func(rng *rand.Rand) uint64
}

func randomL1Cfg(rng *rand.Rand, l1Info eth.BlockInfo) eth.SystemConfig {
	return eth.SystemConfig{
		BatcherAddr: testutils.RandomAddress(rng),
		Overhead:    [32]byte{},
		Scalar:      [32]byte{},
		GasLimit:    1234567,
	}
}

func TestParseL1InfoDepositTxData(t *testing.T) {
	randomSeqNr := func(rng *rand.Rand) uint64 {
		return rng.Uint64()
	}
	// Go 1.18 will have native fuzzing for us to use, until then, we cover just the below cases
	cases := []infoTest{
		{"random", testutils.MakeBlockInfo(nil), randomL1Cfg, randomSeqNr},
		{"zero basefee", testutils.MakeBlockInfo(func(l *testutils.MockBlockInfo) {
			l.InfoBaseFee = new(big.Int)
		}), randomL1Cfg, randomSeqNr},
		{"zero time", testutils.MakeBlockInfo(func(l *testutils.MockBlockInfo) {
			l.InfoTime = 0
		}), randomL1Cfg, randomSeqNr},
		{"zero num", testutils.MakeBlockInfo(func(l *testutils.MockBlockInfo) {
			l.InfoNum = 0
		}), randomL1Cfg, randomSeqNr},
		{"zero seq", testutils.MakeBlockInfo(nil), randomL1Cfg, func(rng *rand.Rand) uint64 {
			return 0
		}},
		{"all zero", func(rng *rand.Rand) *testutils.MockBlockInfo {
			return &testutils.MockBlockInfo{InfoBaseFee: new(big.Int)}
		}, randomL1Cfg, func(rng *rand.Rand) uint64 {
			return 0
		}},
	}
	for i, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(1234 + i)))
			info := testCase.mkInfo(rng)
			l1Cfg := testCase.mkL1Cfg(rng, info)
			seqNr := testCase.seqNr(rng)
			depTx, err := L1InfoDeposit(params.MergedTestChainConfig, l1Cfg, seqNr, info)
			require.NoError(t, err)
			res, err := L1BlockInfoFromBytes(depTx.Data)
			require.NoError(t, err, "expected valid deposit info")
			assert.Equal(t, res.Number, info.NumberU64())
			assert.Equal(t, res.Time, info.Time())
			assert.True(t, res.BaseFee.Sign() >= 0)
			assert.Equal(t, res.BaseFee.Bytes(), info.BaseFee().Bytes())
			assert.Equal(t, res.BlockHash, info.Hash())
			assert.Equal(t, res.SequenceNumber, seqNr)
			assert.Equal(t, res.BatcherAddr, l1Cfg.BatcherAddr)
			scalars, err := l1Cfg.EcotoneScalars()
			require.NoError(t, err)
			assert.Equal(t, res.BaseFeeScalar, scalars.BaseFeeScalar)
			assert.Equal(t, res.BlobBaseFeeScalar, scalars.BlobBaseFeeScalar)
		})
	}
	t.Run("no data", func(t *testing.T) {
		_, err := L1BlockInfoFromBytes(nil)
		assert.Error(t, err)
	})
	t.Run("not enough data", func(t *testing.T) {
		_, err := L1BlockInfoFromBytes([]byte{1, 2, 3, 4})
		assert.Error(t, err)
	})
	t.Run("too much data", func(t *testing.T) {
		_, err := L1BlockInfoFromBytes(make([]byte, 4+32+32+32+32+32+1))
		assert.Error(t, err)
	})
	t.Run("invalid selector", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		info := testutils.MakeBlockInfo(nil)(rng)
		depTx, err := L1InfoDeposit(params.MergedTestChainConfig, randomL1Cfg(rng, info), randomSeqNr(rng), info)
		require.NoError(t, err)
		_, err = crand.Read(depTx.Data[0:4])
		require.NoError(t, err)
		_, err = L1BlockInfoFromBytes(depTx.Data)
		require.ErrorContains(t, err, "function signature")
	})
	t.Run("jovian", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		info := testutils.MakeBlockInfo(nil)(rng)
		depTx, err := L1InfoDeposit(params.MergedTestChainConfig, randomL1Cfg(rng, info), randomSeqNr(rng), info)
		require.NoError(t, err)
		require.False(t, depTx.IsSystemTransaction)
		require.Equal(t, depTx.Gas, uint64(RegolithSystemTxGas))
		require.Equal(t, L1InfoJovianLen, len(depTx.Data))
		dafgs, err := types.ExtractDAFootprintGasScalar(depTx.Data)
		require.NoError(t, err)
		// randomL1Cfg has scalar 0, which should be translated to the default value.
		require.Equal(t, uint16(DAFootprintGasScalarDefault), dafgs)
	})
}
