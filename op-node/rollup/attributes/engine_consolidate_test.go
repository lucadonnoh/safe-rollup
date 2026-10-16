package attributes

import (
	"math/rand" // nosemgrep
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/ethereum-optimism/optimism/op-core/eip1559"
	opparams "github.com/ethereum-optimism/optimism/op-core/params"
	"github.com/ethereum-optimism/optimism/op-core/predeploys"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
)

var defaultOpConfig = &opparams.OptimismConfig{
	EIP1559Elasticity:        6,
	EIP1559Denominator:       50,
	EIP1559DenominatorCanyon: ptr(uint64(250)),
}

func ptr[T any](t T) *T {
	return &t
}

type matchArgs struct {
	envelope   *eth.ExecutionPayloadEnvelope
	attrs      *eth.PayloadAttributes
	parentHash common.Hash
}

func jovianArgs() matchArgs {
	var (
		validParentHash       = common.HexToHash("0x123")
		validTimestamp        = eth.Uint64Quantity(50)
		validParentBeaconRoot = common.HexToHash("0x456")
		validPrevRandao       = eth.Bytes32(common.HexToHash("0x789"))
		validGasLimit         = eth.Uint64Quantity(1000)
		validFeeRecipient     = predeploys.SequencerFeeVaultAddr
		validTx               = testutils.RandomLegacyTxNotProtected(rand.New(rand.NewSource(42)))
		validTxData, _        = validTx.MarshalBinary()
		minBaseFee            = uint64(1e9)

		validJovianExtraData = eth.BytesMax32(eip1559.EncodeJovianExtraData(
			*defaultOpConfig.EIP1559DenominatorCanyon, defaultOpConfig.EIP1559Elasticity, minBaseFee))
		validJovianEIP1559Params = new(eth.Bytes8)
	)
	// Populate the EIP1559 params with the encoded values
	copy((*validJovianEIP1559Params)[:], eip1559.EncodeHolocene1559Params(
		*defaultOpConfig.EIP1559DenominatorCanyon, defaultOpConfig.EIP1559Elasticity))

	return matchArgs{
		envelope: &eth.ExecutionPayloadEnvelope{
			ParentBeaconBlockRoot: &validParentBeaconRoot,
			ExecutionPayload: &eth.ExecutionPayload{
				ParentHash:      validParentHash,
				Timestamp:       validTimestamp,
				PrevRandao:      validPrevRandao,
				GasLimit:        validGasLimit,
				Transactions:    []eth.Data{validTxData},
				Withdrawals:     &types.Withdrawals{},
				FeeRecipient:    validFeeRecipient,
				ExtraData:       validJovianExtraData,
				WithdrawalsRoot: &types.EmptyWithdrawalsHash,
			},
		},
		attrs: &eth.PayloadAttributes{
			Timestamp:             validTimestamp,
			PrevRandao:            validPrevRandao,
			GasLimit:              &validGasLimit,
			ParentBeaconBlockRoot: &validParentBeaconRoot,
			Transactions:          []eth.Data{validTxData},
			Withdrawals:           &types.Withdrawals{},
			SuggestedFeeRecipient: validFeeRecipient,
			EIP1559Params:         validJovianEIP1559Params,
			MinBaseFee:            &minBaseFee,
		},
		parentHash: validParentHash,
	}
}

func jovianArgsMinBaseFeeMissingFromAttributes() matchArgs {
	args := jovianArgs()
	args.attrs.MinBaseFee = nil
	return args
}

func jovianArgsMinBaseFeeMissingFromBlock() matchArgs {
	args := jovianArgs()
	// Holocene-style extraData: version byte 0 followed by the eip-1559 params, without minBaseFee.
	args.envelope.ExecutionPayload.ExtraData = eth.BytesMax32(append([]byte{0}, eip1559.EncodeHolocene1559Params(
		*defaultOpConfig.EIP1559DenominatorCanyon, defaultOpConfig.EIP1559Elasticity)...))
	return args
}

func jovianArgsInconsistentMinBaseFee() matchArgs {
	args := jovianArgs()
	args.attrs.MinBaseFee = ptr(uint64(2e9))
	return args
}

func noParentBeaconBlockRoot() matchArgs {
	args := jovianArgs()
	args.envelope.ParentBeaconBlockRoot = nil
	return args
}

func unexpectedParentBeaconBlockRoot() matchArgs {
	args := jovianArgs()
	args.attrs.ParentBeaconBlockRoot = nil
	return args
}

func mismatchParentBeaconBlockRoot() matchArgs {
	args := jovianArgs()
	h := common.HexToHash("0xabc")
	args.attrs.ParentBeaconBlockRoot = &h
	return args
}

func mismatchParentBeaconBlockRootPtr() matchArgs {
	args := jovianArgs()
	cpy := *args.attrs.ParentBeaconBlockRoot
	args.attrs.ParentBeaconBlockRoot = &cpy
	return args
}

func nilParentBeaconBlockRoots() matchArgs {
	args := jovianArgs()
	args.attrs.ParentBeaconBlockRoot = nil
	args.envelope.ParentBeaconBlockRoot = nil
	return args
}

func mismatchedParentHashArgs() matchArgs {
	args := jovianArgs()
	args.parentHash = common.HexToHash("0xabc")
	return args
}

func createMismatchedPrevRandao() matchArgs {
	args := jovianArgs()
	args.attrs.PrevRandao = eth.Bytes32(common.HexToHash("0xabc"))
	return args
}

func createMismatchedGasLimit() matchArgs {
	args := jovianArgs()
	val := eth.Uint64Quantity(2000)
	args.attrs.GasLimit = &val
	return args
}

func createNilGasLimit() matchArgs {
	args := jovianArgs()
	args.attrs.GasLimit = nil
	return args
}

func createMismatchedTimestamp() matchArgs {
	args := jovianArgs()
	args.attrs.Timestamp++
	return args
}

func createMismatchedTransactions() matchArgs {
	args := jovianArgs()
	args.attrs.Transactions = append(args.attrs.Transactions, args.attrs.Transactions[0])
	return args
}

func createMismatchedFeeRecipient() matchArgs {
	args := jovianArgs()
	args.attrs.SuggestedFeeRecipient = common.Address{0xde, 0xad}
	return args
}

func createMismatchedEIP1559Params() matchArgs {
	args := jovianArgs()
	// Create valid but mismatched EIP-1559 params (both denominator and elasticity must be non-zero or both zero)
	copy((*args.attrs.EIP1559Params)[:], eip1559.EncodeHolocene1559Params(999, 999))
	return args
}

func noWithdrawals() matchArgs {
	args := jovianArgs()
	args.attrs.Withdrawals = nil
	args.envelope.ExecutionPayload.Withdrawals = nil
	return args
}

func TestAttributesMatch(t *testing.T) {
	cfg := &rollup.Config{ChainOpConfig: defaultOpConfig}

	tests := []struct {
		args matchArgs
		err  string
		desc string
	}{
		{args: jovianArgs(), desc: "validJovianArgs"},
		{args: noWithdrawals(), err: ErrCanyonMustHaveWithdrawals.Error() + ": block", desc: "noWithdrawals"},
		{args: mismatchedParentHashArgs(), err: "parent hash field does not match", desc: "mismatchedParentHashArgs"},
		{args: createMismatchedTimestamp(), err: "timestamp field does not match", desc: "createMismatchedTimestamp"},
		{args: createMismatchedPrevRandao(), err: "random field does not match", desc: "createMismatchedPrevRandao"},
		{args: createMismatchedTransactions(), err: "transaction count does not match", desc: "createMismatchedTransactions"},
		{args: noParentBeaconBlockRoot(), err: "expected non-nil parent beacon block root", desc: "noParentBeaconBlockRoot"},
		{args: unexpectedParentBeaconBlockRoot(), err: "expected nil parent beacon block root but got non-nil", desc: "unexpectedParentBeaconBlockRoot"},
		{args: mismatchParentBeaconBlockRoot(), err: "parent beacon block root does not match", desc: "mismatchParentBeaconBlockRoot"},
		{args: mismatchParentBeaconBlockRootPtr(), desc: "mismatchParentBeaconBlockRootPtr"},
		{args: nilParentBeaconBlockRoots(), desc: "nilParentBeaconBlockRoots"},
		{args: createMismatchedGasLimit(), err: "gas limit does not match", desc: "createMismatchedGasLimit"},
		{args: createNilGasLimit(), err: "expected gaslimit in attributes to not be nil", desc: "createNilGasLimit"},
		{args: createMismatchedFeeRecipient(), err: "fee recipient data does not match", desc: "createMismatchedFeeRecipient"},
		{args: createMismatchedEIP1559Params(), err: "eip1559 parameters do not match", desc: "createMismatchedEIP1559Params"},
		{args: jovianArgsMinBaseFeeMissingFromAttributes(), err: "minBaseFee does not match", desc: "missingMinBaseFee"},
		{args: jovianArgsMinBaseFeeMissingFromBlock(), err: "invalid block extraData: Jovian extraData should be 17 bytes, got 9", desc: "missingMinBaseFeeInBlock"},
		{args: jovianArgsInconsistentMinBaseFee(), err: "minBaseFee does not match", desc: "inconsistentMinBaseFee"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			err := AttributesMatchBlock(cfg,
				test.args.attrs, test.args.parentHash, test.args.envelope,
				testlog.Logger(t, log.LevelInfo),
			)
			if test.err == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.err)
			}
		})
	}
}

func TestWithdrawalsMatch(t *testing.T) {
	emptyWithdrawals := make(types.Withdrawals, 0)
	cfg := &rollup.Config{ChainOpConfig: defaultOpConfig}
	nonEmpty := &types.Withdrawals{&types.Withdrawal{Index: 1}}

	tests := []struct {
		attrs *eth.PayloadAttributes
		block *eth.ExecutionPayload
		err   error
		desc  string
	}{
		{
			err:  ErrNilBlockOrAttributes,
			desc: "nil attributes/block",
		},
		{
			attrs: &eth.PayloadAttributes{},
			block: &eth.ExecutionPayload{Withdrawals: nonEmpty},
			err:   ErrCanyonMustHaveWithdrawals,
			desc:  "non-empty block withdrawals list",
		},
		{
			attrs: &eth.PayloadAttributes{Withdrawals: &emptyWithdrawals},
			block: &eth.ExecutionPayload{Withdrawals: &emptyWithdrawals},
			err:   ErrIsthmusMustHaveWithdrawalsRoot,
			desc:  "nil block withdrawalsRoot",
		},
		{
			attrs: &eth.PayloadAttributes{Withdrawals: nonEmpty},
			block: &eth.ExecutionPayload{WithdrawalsRoot: &common.Hash{}, Withdrawals: &emptyWithdrawals},
			err:   ErrCanyonMustHaveWithdrawals,
			desc:  "non-empty attr withdrawals list",
		},
		{
			attrs: &eth.PayloadAttributes{Withdrawals: &emptyWithdrawals},
			block: &eth.ExecutionPayload{WithdrawalsRoot: &common.Hash{}, Withdrawals: &emptyWithdrawals},
			desc:  "withdrawalsRoot and empty block/attr withdrawals list",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			err := checkWithdrawals(cfg, test.attrs, test.block)
			if test.err != nil {
				require.ErrorIs(t, err, test.err, "test: %s", test.desc)
			} else {
				require.NoError(t, err, "test: %s", test.desc)
			}
		})
	}
}

func TestCheckEIP1559ParamsMatch(t *testing.T) {
	params := eth.Bytes8{1, 2, 3, 4, 5, 6, 7, 8}
	paramsAlt := eth.Bytes8{1, 2, 3, 4, 5, 6, 7, 9}
	paramsInvalid := eth.Bytes8{0, 0, 0, 0, 5, 6, 7, 8}
	minBaseFee := uint64(7)
	jovianExtra := func(version byte, p []byte) eth.BytesMax32 {
		extra := append(eth.BytesMax32{version}, p...)
		return append(extra, 0, 0, 0, 0, 0, 0, 0, byte(minBaseFee))
	}
	defaultExtraData := eth.BytesMax32(eip1559.EncodeJovianExtraData(
		*defaultOpConfig.EIP1559DenominatorCanyon, defaultOpConfig.EIP1559Elasticity, minBaseFee))

	for _, test := range []struct {
		desc           string
		attrParams     *eth.Bytes8
		blockExtraData eth.BytesMax32
		err            string
	}{
		{
			desc: "match-empty",
		},
		{
			desc:           "match-zero-attrs",
			attrParams:     new(eth.Bytes8),
			blockExtraData: defaultExtraData,
		},
		{
			desc:           "match-non-zero",
			attrParams:     &params,
			blockExtraData: jovianExtra(1, params[:]),
		},
		{
			desc:           "err-both-zero",
			attrParams:     new(eth.Bytes8),
			blockExtraData: jovianExtra(1, make([]byte, 8)),
			err:            "invalid block extraData: holocene extraData must encode a non-zero denominator",
		},
		{
			desc:           "err-invalid-params",
			attrParams:     &paramsInvalid,
			blockExtraData: jovianExtra(1, paramsInvalid[:]),
			err:            "invalid attributes EIP1559 parameters: holocene params cannot have a 0 denominator unless elasticity is also 0",
		},
		{
			desc:           "err-invalid-extra",
			attrParams:     &params,
			blockExtraData: jovianExtra(42, params[:]),
			err:            "invalid block extraData: Jovian extraData version byte should be 1, got 42",
		},
		{
			desc:           "err-no-match",
			attrParams:     &paramsAlt,
			blockExtraData: jovianExtra(1, params[:]),
			err:            "eip1559 parameters do not match",
		},
		{
			desc:           "err-non-nil-extra",
			blockExtraData: make(eth.BytesMax32, 17),
			err:            "nil EIP1559Params in attributes but non-nil extraData in block",
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			cfg := &rollup.Config{ChainOpConfig: defaultOpConfig}
			err := checkExtraDataParamsMatch(cfg, uint64(2), test.attrParams, &minBaseFee, test.blockExtraData)
			if test.err == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.err)
			}
		})
	}
}

func TestGetMissingTxnHashes(t *testing.T) {
	depositTxs := make([]*types.Transaction, 5)

	for i := 0; i < len(depositTxs); i++ {
		rng := rand.New(rand.NewSource(1234 + int64(i)))
		safeDeposit := testutils.GenerateDeposit(testutils.RandomHash(rng), rng)
		depositTxs[i] = testutils.TxFromDeposit(safeDeposit)
	}

	tests := []struct {
		safeTransactions            []hexutil.Bytes
		unsafeTransactions          []hexutil.Bytes
		expectedSafeMissingHashes   []common.Hash
		expectedUnsafeMissingHashes []common.Hash
		expectErr                   bool
	}{
		{
			safeTransactions:            []hexutil.Bytes{},
			unsafeTransactions:          []hexutil.Bytes{depositTxToBytes(t, depositTxs[0])},
			expectedSafeMissingHashes:   []common.Hash{depositTxs[0].Hash()},
			expectedUnsafeMissingHashes: []common.Hash{},
			expectErr:                   false,
		},
		{
			safeTransactions:            []hexutil.Bytes{depositTxToBytes(t, depositTxs[0])},
			unsafeTransactions:          []hexutil.Bytes{},
			expectedSafeMissingHashes:   []common.Hash{},
			expectedUnsafeMissingHashes: []common.Hash{depositTxs[0].Hash()},
			expectErr:                   false,
		},
		{
			safeTransactions: []hexutil.Bytes{
				depositTxToBytes(t, depositTxs[0]),
			},
			unsafeTransactions: []hexutil.Bytes{
				depositTxToBytes(t, depositTxs[0]),
				depositTxToBytes(t, depositTxs[1]),
				depositTxToBytes(t, depositTxs[2]),
			},
			expectedSafeMissingHashes: []common.Hash{
				depositTxs[1].Hash(),
				depositTxs[2].Hash(),
			},
			expectedUnsafeMissingHashes: []common.Hash{},
			expectErr:                   false,
		},
		{
			safeTransactions: []hexutil.Bytes{
				depositTxToBytes(t, depositTxs[0]),
				depositTxToBytes(t, depositTxs[1]),
				depositTxToBytes(t, depositTxs[2]),
			},
			unsafeTransactions: []hexutil.Bytes{
				depositTxToBytes(t, depositTxs[0]),
			},
			expectedSafeMissingHashes: []common.Hash{},
			expectedUnsafeMissingHashes: []common.Hash{
				depositTxs[1].Hash(),
				depositTxs[2].Hash(),
			},
			expectErr: false,
		},
		{
			safeTransactions: []hexutil.Bytes{
				depositTxToBytes(t, depositTxs[0]),
				depositTxToBytes(t, depositTxs[1]),
				depositTxToBytes(t, depositTxs[2]),
			},
			unsafeTransactions: []hexutil.Bytes{
				depositTxToBytes(t, depositTxs[2]),
				depositTxToBytes(t, depositTxs[3]),
				depositTxToBytes(t, depositTxs[4]),
			},
			expectedSafeMissingHashes: []common.Hash{
				depositTxs[3].Hash(),
				depositTxs[4].Hash(),
			},
			expectedUnsafeMissingHashes: []common.Hash{
				depositTxs[0].Hash(),
				depositTxs[1].Hash(),
			},
			expectErr: false,
		},
		{
			safeTransactions:            []hexutil.Bytes{{1, 2, 3}},
			unsafeTransactions:          []hexutil.Bytes{},
			expectedSafeMissingHashes:   []common.Hash{},
			expectedUnsafeMissingHashes: []common.Hash{},
			expectErr:                   true,
		},
		{
			safeTransactions:            []hexutil.Bytes{},
			unsafeTransactions:          []hexutil.Bytes{{1, 2, 3}},
			expectedSafeMissingHashes:   []common.Hash{},
			expectedUnsafeMissingHashes: []common.Hash{},
			expectErr:                   true,
		},
	}

	for _, test := range tests {
		missingSafeHashes, missingUnsafeHashes, err := getMissingTxnHashes(
			testlog.Logger(t, log.LevelError),
			test.safeTransactions,
			test.unsafeTransactions,
		)

		if test.expectErr {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.ElementsMatch(t, test.expectedSafeMissingHashes, missingSafeHashes)
			require.ElementsMatch(t, test.expectedUnsafeMissingHashes, missingUnsafeHashes)
		}
	}
}

func depositTxToBytes(t *testing.T, tx *types.Transaction) hexutil.Bytes {
	txBytes, err := tx.MarshalBinary()
	require.NoError(t, err)

	return txBytes
}
