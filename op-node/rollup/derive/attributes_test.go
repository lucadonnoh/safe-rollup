package derive

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum-optimism/optimism/op-core/predeploys"
	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func TestPreparePayloadAttributes(t *testing.T) {
	// test sysCfg, only init the necessary fields
	// Create a new config each time, otherwise modifications pollute other tests.
	mkCfg := func() *rollup.Config {
		return &rollup.Config{
			BlockTime:              2,
			L1ChainID:              big.NewInt(101),
			L2ChainID:              big.NewInt(102),
			DepositContractAddress: common.Address{0xbb},
			L1SystemConfigAddress:  common.Address{0xcc},
		}
	}

	testSysCfg := eth.SystemConfig{
		BatcherAddr: common.Address{42},
		Overhead:    [32]byte{},
		Scalar:      [32]byte{},
	}

	t.Run("inconsistent next height origin", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		l1Info := testutils.RandomBlockInfo(rng)
		l1Info.InfoNum = l2Parent.L1Origin.Number + 1
		epoch := l1Info.ID()
		l1Fetcher.ExpectFetchReceipts(epoch.Hash, l1Info, nil, nil)
		attrBuilder := NewFetchingAttributesBuilder(mkCfg(), params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		_, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.NotNil(t, err, "inconsistent L1 origin error expected")
		require.ErrorIs(t, err, ErrReset, "inconsistent L1 origin transition must be handled like a critical error with reorg")
	})
	t.Run("inconsistent equal height origin", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		l1Info := testutils.RandomBlockInfo(rng)
		l1Info.InfoNum = l2Parent.L1Origin.Number
		epoch := l1Info.ID()
		attrBuilder := NewFetchingAttributesBuilder(mkCfg(), params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		_, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.NotNil(t, err, "inconsistent L1 origin error expected")
		require.ErrorIs(t, err, ErrReset, "inconsistent L1 origin transition must be handled like a critical error with reorg")
	})
	t.Run("rpc fail Fetch", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		epoch := l2Parent.L1Origin
		epoch.Number += 1
		mockRPCErr := errors.New("mock rpc error")
		l1Fetcher.ExpectFetchReceipts(epoch.Hash, nil, nil, mockRPCErr)
		attrBuilder := NewFetchingAttributesBuilder(mkCfg(), params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		_, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.ErrorIs(t, err, mockRPCErr, "mock rpc error expected")
		require.ErrorIs(t, err, ErrTemporary, "rpc errors should not be critical, it is not necessary to reorg")
	})
	t.Run("rpc fail InfoByHash", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		epoch := l2Parent.L1Origin
		mockRPCErr := errors.New("mock rpc error")
		l1Fetcher.ExpectInfoByHash(epoch.Hash, nil, mockRPCErr)
		attrBuilder := NewFetchingAttributesBuilder(mkCfg(), params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		_, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.ErrorIs(t, err, mockRPCErr, "mock rpc error expected")
		require.ErrorIs(t, err, ErrTemporary, "rpc errors should not be critical, it is not necessary to reorg")
	})
	t.Run("next origin without deposits", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		cfg := mkCfg()
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		l1Info := testutils.RandomBlockInfo(rng)
		l1Info.InfoParentHash = l2Parent.L1Origin.Hash
		l1Info.InfoNum = l2Parent.L1Origin.Number + 1
		epoch := l1Info.ID()
		l1InfoTx, err := L1InfoDepositBytes(params.MergedTestChainConfig, testSysCfg, 0, l1Info)
		require.NoError(t, err)
		l1Fetcher.ExpectFetchReceipts(epoch.Hash, l1Info, nil, nil)
		attrBuilder := NewFetchingAttributesBuilder(cfg, params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		attrs, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.NoError(t, err)
		require.NotNil(t, attrs)
		require.Equal(t, testSysCfg.EIP1559Params, *attrs.EIP1559Params)
		require.Equal(t, l2Parent.Time+cfg.BlockTime, uint64(attrs.Timestamp))
		require.Equal(t, eth.Bytes32(l1Info.InfoMixDigest), attrs.PrevRandao)
		require.Equal(t, predeploys.SequencerFeeVaultAddr, attrs.SuggestedFeeRecipient)
		require.Equal(t, 1, len(attrs.Transactions))
		require.Equal(t, l1InfoTx, []byte(attrs.Transactions[0]))
		require.True(t, attrs.NoTxPool)
	})
	t.Run("next origin with deposits", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		cfg := mkCfg()
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		l1Info := testutils.RandomBlockInfo(rng)
		l1Info.InfoParentHash = l2Parent.L1Origin.Hash
		l1Info.InfoNum = l2Parent.L1Origin.Number + 1

		receipts, depositTxs, err := makeReceipts(rng, l1Info.InfoHash, cfg.DepositContractAddress, []receiptData{
			{goodReceipt: true, DepositLogs: []bool{true, false}},
			{goodReceipt: true, DepositLogs: []bool{true}},
			{goodReceipt: false, DepositLogs: []bool{true}},
			{goodReceipt: false, DepositLogs: []bool{false}},
		})
		require.NoError(t, err)
		usedDepositTxs, err := encodeDeposits(depositTxs)
		require.NoError(t, err)

		epoch := l1Info.ID()
		l1InfoTx, err := L1InfoDepositBytes(params.MergedTestChainConfig, testSysCfg, 0, l1Info)
		require.NoError(t, err)

		l2Txs := append(append(make([]eth.Data, 0), l1InfoTx), usedDepositTxs...)

		l1Fetcher.ExpectFetchReceipts(epoch.Hash, l1Info, optypes.FromGethReceipts(receipts), nil)
		attrBuilder := NewFetchingAttributesBuilder(cfg, params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		attrs, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.NoError(t, err)
		require.NotNil(t, attrs)
		require.Equal(t, l2Parent.Time+cfg.BlockTime, uint64(attrs.Timestamp))
		require.Equal(t, eth.Bytes32(l1Info.InfoMixDigest), attrs.PrevRandao)
		require.Equal(t, predeploys.SequencerFeeVaultAddr, attrs.SuggestedFeeRecipient)
		require.Equal(t, len(l2Txs), len(attrs.Transactions), "Expected txs to equal l1 info tx + user deposit txs")
		require.Equal(t, l2Txs, attrs.Transactions)
		require.True(t, attrs.NoTxPool)
	})
	t.Run("same origin again", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1234))
		cfg := mkCfg()
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		l1Info := testutils.RandomBlockInfo(rng)
		l1Info.InfoHash = l2Parent.L1Origin.Hash
		l1Info.InfoNum = l2Parent.L1Origin.Number

		epoch := l1Info.ID()
		l1InfoTx, err := L1InfoDepositBytes(params.MergedTestChainConfig, testSysCfg, l2Parent.SequenceNumber+1, l1Info)
		require.NoError(t, err)

		l1Fetcher.ExpectInfoByHash(epoch.Hash, l1Info, nil)
		attrBuilder := NewFetchingAttributesBuilder(cfg, params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		attrs, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.NoError(t, err)
		require.NotNil(t, attrs)
		require.Equal(t, l2Parent.Time+cfg.BlockTime, uint64(attrs.Timestamp))
		require.Equal(t, eth.Bytes32(l1Info.InfoMixDigest), attrs.PrevRandao)
		require.Equal(t, predeploys.SequencerFeeVaultAddr, attrs.SuggestedFeeRecipient)
		require.Equal(t, 1, len(attrs.Transactions))
		require.Equal(t, l1InfoTx, []byte(attrs.Transactions[0]))
		require.True(t, attrs.NoTxPool)
	})
	t.Run("holocene 1559 params", func(t *testing.T) {
		cfg := mkCfg()
		rng := rand.New(rand.NewSource(1234))
		l1Fetcher := &testutils.MockL1Source{}
		defer l1Fetcher.AssertExpectations(t)
		l2Parent := testutils.RandomL2BlockRef(rng)
		l1CfgFetcher := &testutils.MockL2Client{}
		eip1559Params := eth.Bytes8([]byte{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8})
		testSysCfg := eth.SystemConfig{
			BatcherAddr:   common.Address{42},
			Overhead:      [32]byte{},
			Scalar:        [32]byte{},
			EIP1559Params: eip1559Params,
		}
		l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
		defer l1CfgFetcher.AssertExpectations(t)
		l1Info := testutils.RandomBlockInfo(rng)
		l1Info.InfoParentHash = l2Parent.L1Origin.Hash
		l1Info.InfoNum = l2Parent.L1Origin.Number + 1
		epoch := l1Info.ID()
		l1InfoTx, err := L1InfoDepositBytes(params.MergedTestChainConfig, testSysCfg, 0, l1Info)
		require.NoError(t, err)
		l1Fetcher.ExpectFetchReceipts(epoch.Hash, l1Info, nil, nil)
		attrBuilder := NewFetchingAttributesBuilder(cfg, params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
		attrs, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
		require.NoError(t, err)
		require.Equal(t, eip1559Params, *attrs.EIP1559Params)
		require.Equal(t, l1InfoTx, []byte(attrs.Transactions[0]))
	})

	t.Run("jovian", func(t *testing.T) {
		t.Run("minimum base fee param", func(t *testing.T) {
			cfg := mkCfg()
			rng := rand.New(rand.NewSource(1234))
			l1Fetcher := &testutils.MockL1Source{}
			defer l1Fetcher.AssertExpectations(t)
			l2Parent := testutils.RandomL2BlockRef(rng)
			l1CfgFetcher := &testutils.MockL2Client{}
			eip1559Params := eth.Bytes8([]byte{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8})
			minBaseFee := uint64(1e9)
			testSysCfg := eth.SystemConfig{
				BatcherAddr:   common.Address{42},
				Overhead:      [32]byte{},
				Scalar:        [32]byte{},
				EIP1559Params: eip1559Params,
				MinBaseFee:    minBaseFee,
			}
			l1CfgFetcher.ExpectSystemConfigByL2Hash(l2Parent.Hash, testSysCfg, nil)
			defer l1CfgFetcher.AssertExpectations(t)
			l1Info := testutils.RandomBlockInfo(rng)
			l1Info.InfoParentHash = l2Parent.L1Origin.Hash
			l1Info.InfoNum = l2Parent.L1Origin.Number + 1
			epoch := l1Info.ID()
			l1InfoTx, err := L1InfoDepositBytes(params.MergedTestChainConfig, testSysCfg, 0, l1Info)
			require.NoError(t, err)
			l1Fetcher.ExpectFetchReceipts(epoch.Hash, l1Info, nil, nil)
			attrBuilder := NewFetchingAttributesBuilder(cfg, params.MergedTestChainConfig, l1Fetcher, l1CfgFetcher)
			attrs, err := attrBuilder.PreparePayloadAttributes(context.Background(), l2Parent, epoch)
			require.NoError(t, err)
			require.Equal(t, eip1559Params, *attrs.EIP1559Params)
			require.Equal(t, minBaseFee, *attrs.MinBaseFee)
			require.Equal(t, l1InfoTx, []byte(attrs.Transactions[0]))
		})
	})
}

func encodeDeposits(deposits []*optypes.DepositTx) (out []eth.Data, err error) {
	for i, tx := range deposits {
		opaqueTx, err := tx.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("bad deposit %d: %w", i, err)
		}
		out = append(out, opaqueTx)
	}
	return
}
