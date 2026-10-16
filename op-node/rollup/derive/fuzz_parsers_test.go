package derive

import (
	"math/big"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/ethereum-optimism/optimism/op-service/testutils"
)

var (
	pk, _   = crypto.GenerateKey()
	opts, _ = bind.NewKeyedTransactorWithChainID(pk, common.Big1)
	from    = crypto.PubkeyToAddress(pk.PublicKey)
)

func cap_byte_slice(b []byte, c int) []byte {
	if len(b) <= c {
		return b
	} else {
		return b[:c]
	}
}

func BytesToBigInt(b []byte) *big.Int {
	return new(big.Int).SetBytes(cap_byte_slice(b, 32))
}

// FuzzL1InfoJovianRoundTrip checks that our Jovian encoder round trips properly
func FuzzL1InfoJovianRoundTrip(f *testing.F) {
	f.Fuzz(func(t *testing.T, number, time uint64, baseFee, blobBaseFee, hash []byte, seqNumber uint64, baseFeeScalar, blobBaseFeeScalar, operatorFeeScalar uint32, operatorFeeConstant uint64, daFootprintGasScalar uint16) {
		in := L1BlockInfo{
			Number:               number,
			Time:                 time,
			BaseFee:              BytesToBigInt(baseFee),
			BlockHash:            common.BytesToHash(hash),
			SequenceNumber:       seqNumber,
			BlobBaseFee:          BytesToBigInt(blobBaseFee),
			BaseFeeScalar:        baseFeeScalar,
			BlobBaseFeeScalar:    blobBaseFeeScalar,
			OperatorFeeScalar:    operatorFeeScalar,
			OperatorFeeConstant:  operatorFeeConstant,
			DAFootprintGasScalar: daFootprintGasScalar,
		}
		enc, err := in.marshalBinaryJovian()
		if err != nil {
			t.Fatalf("Failed to marshal Jovian binary: %v", err)
		}
		var out L1BlockInfo
		err = out.unmarshalBinaryJovian(enc)
		if err != nil {
			t.Fatalf("Failed to unmarshal Jovian binary: %v", err)
		}
		if !cmp.Equal(in, out, cmp.Comparer(testutils.BigEqual)) {
			t.Fatalf("The Jovian data did not round trip correctly. in: %v. out: %v", in, out)
		}
	})
}

// Standard ABI types copied from golang ABI tests
var (
	Uint256Type, _ = abi.NewType("uint256", "", nil)
	Uint64Type, _  = abi.NewType("uint64", "", nil)
	BytesType, _   = abi.NewType("bytes", "", nil)
	BoolType, _    = abi.NewType("bool", "", nil)
	AddressType, _ = abi.NewType("address", "", nil)
)

// EncodeDepositOpaqueDataV0 performs ABI encoding to create the opaque data field of the deposit event.
func EncodeDepositOpaqueDataV0(t *testing.T, mint *big.Int, value *big.Int, gasLimit uint64, isCreation bool, data []byte) []byte {
	t.Helper()
	// in OptimismPortal.sol:
	// bytes memory opaqueData = abi.encodePacked(msg.value, _value, _gasLimit, _isCreation, _data);
	// Geth does not support abi.encodePacked, so we emulate it here by slicing of the padding from the individual elements
	// See https://github.com/ethereum/go-ethereum/issues/22257
	// And https://docs.soliditylang.org/en/v0.8.13/abi-spec.html#non-standard-packed-mode

	var out []byte

	v, err := abi.Arguments{{Name: "msg.value", Type: Uint256Type}}.Pack(mint)
	require.NoError(t, err)
	out = append(out, v...)

	v, err = abi.Arguments{{Name: "_value", Type: Uint256Type}}.Pack(value)
	require.NoError(t, err)
	out = append(out, v...)

	v, err = abi.Arguments{{Name: "_gasLimit", Type: Uint64Type}}.Pack(gasLimit)
	require.NoError(t, err)
	out = append(out, v[32-8:]...) // 8 bytes only with abi.encodePacked

	v, err = abi.Arguments{{Name: "_isCreation", Type: BoolType}}.Pack(isCreation)
	require.NoError(t, err)
	out = append(out, v[32-1:]...) // 1 byte only with abi.encodePacked

	// no slice header, just the raw data with abi.encodePacked
	out = append(out, data...)

	return out
}
