package derive

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"

	"github.com/ethereum-optimism/optimism/op-core/predeploys"
	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/solabi"
)

const (
	L1InfoFuncJovianSignature = "setL1BlockValuesJovian()"
	L1InfoJovianLen           = 4 + 32*5 + 4 + 8 + 2
)

var (
	L1InfoFuncJovianBytes4 = crypto.Keccak256([]byte(L1InfoFuncJovianSignature))[:4]
	L1InfoDepositerAddress = common.HexToAddress("0xdeaddeaddeaddeaddeaddeaddeaddeaddead0001")
	L1BlockAddress         = predeploys.L1BlockAddr
	ErrInvalidJovianFormat = errors.New("invalid jovian l1 block info format")
)

const (
	RegolithSystemTxGas         = 1_000_000
	DAFootprintGasScalarDefault = 400
)

// L1BlockInfo presents the information stored in a L1Block.setL1BlockValuesJovian call
type L1BlockInfo struct {
	Number    uint64
	Time      uint64
	BaseFee   *big.Int
	BlockHash common.Hash
	// Not strictly a piece of L1 information. Represents the number of L2 blocks since the start of the epoch,
	// i.e. when the actual L1 info was first introduced.
	SequenceNumber uint64
	// BatcherAddr version 0 is just the address with 0 padding to the left.
	BatcherAddr common.Address

	BlobBaseFee       *big.Int
	BaseFeeScalar     uint32
	BlobBaseFeeScalar uint32

	OperatorFeeScalar   uint32
	OperatorFeeConstant uint64

	DAFootprintGasScalar uint16
}

// SetDAFootprintGasScalarOrDefault sets the DAFootprintGasScalar field, defaulting to
// DAFootprintGasScalarDefault if 0 is provided. This helps in translating the gas scalar value from
// the L1 SystemConfig representation to the L1BlockInfo representation.
func (info *L1BlockInfo) SetDAFootprintGasScalarOrDefault(daFootprintGasScalar uint16) {
	if daFootprintGasScalar == 0 {
		info.DAFootprintGasScalar = DAFootprintGasScalarDefault
	} else {
		info.DAFootprintGasScalar = daFootprintGasScalar
	}
}

// Jovian Binary Format
// +---------+--------------------------+
// | Bytes   | Field                    |
// +---------+--------------------------+
// | 4       | Function signature       |
// | 4       | BaseFeeScalar            |
// | 4       | BlobBaseFeeScalar        |
// | 8       | SequenceNumber           |
// | 8       | Timestamp                |
// | 8       | L1BlockNumber            |
// | 32      | BaseFee                  |
// | 32      | BlobBaseFee              |
// | 32      | BlockHash                |
// | 32      | BatcherHash              |
// | 4       | OperatorFeeScalar        |
// | 8       | OperatorFeeConstant      |
// | 2       | DAFootprintGasScalar     |
// +---------+--------------------------+

func (info *L1BlockInfo) marshalBinaryJovian() ([]byte, error) {
	w := bytes.NewBuffer(make([]byte, 0, L1InfoJovianLen))
	if err := solabi.WriteSignature(w, L1InfoFuncJovianBytes4); err != nil {
		return nil, err
	}
	for _, v := range []any{info.BaseFeeScalar, info.BlobBaseFeeScalar, info.SequenceNumber, info.Time, info.Number} {
		if err := binary.Write(w, binary.BigEndian, v); err != nil {
			return nil, err
		}
	}
	if err := solabi.WriteUint256(w, info.BaseFee); err != nil {
		return nil, err
	}
	blobBasefee := info.BlobBaseFee
	if blobBasefee == nil {
		blobBasefee = big.NewInt(1) // set to 1, to match the min blob basefee as defined in EIP-4844
	}
	if err := solabi.WriteUint256(w, blobBasefee); err != nil {
		return nil, err
	}
	if err := solabi.WriteHash(w, info.BlockHash); err != nil {
		return nil, err
	}
	// ABI encoding will perform the left-padding with zeroes to 32 bytes, matching the "batcherHash" SystemConfig format and version 0 byte.
	if err := solabi.WriteAddress(w, info.BatcherAddr); err != nil {
		return nil, err
	}
	for _, v := range []any{info.OperatorFeeScalar, info.OperatorFeeConstant, info.DAFootprintGasScalar} {
		if err := binary.Write(w, binary.BigEndian, v); err != nil {
			return nil, err
		}
	}
	return w.Bytes(), nil
}

func (info *L1BlockInfo) unmarshalBinaryJovian(data []byte) error {
	if len(data) != L1InfoJovianLen {
		return fmt.Errorf("data is unexpected length: %d", len(data))
	}
	r := bytes.NewReader(data)
	if _, err := solabi.ReadAndValidateSignature(r, L1InfoFuncJovianBytes4); err != nil {
		return err
	}
	if err := readBigEndian(r, &info.BaseFeeScalar, &info.BlobBaseFeeScalar, &info.SequenceNumber, &info.Time, &info.Number); err != nil {
		return err
	}
	var err error
	if info.BaseFee, err = solabi.ReadUint256(r); err != nil {
		return err
	}
	if info.BlobBaseFee, err = solabi.ReadUint256(r); err != nil {
		return err
	}
	if info.BlockHash, err = solabi.ReadHash(r); err != nil {
		return err
	}
	// The "batcherHash" will be correctly parsed as address, since the version 0 and left-padding matches the ABI encoding format.
	if info.BatcherAddr, err = solabi.ReadAddress(r); err != nil {
		return err
	}
	if err := readBigEndian(r, &info.OperatorFeeScalar, &info.OperatorFeeConstant, &info.DAFootprintGasScalar); err != nil {
		return err
	}
	if !solabi.EmptyReader(r) {
		return errors.New("too many bytes")
	}
	return nil
}

func readBigEndian(r io.Reader, fields ...any) error {
	for _, f := range fields {
		if err := binary.Read(r, binary.BigEndian, f); err != nil {
			return ErrInvalidJovianFormat
		}
	}
	return nil
}

// L1BlockInfoFromBytes is the inverse of L1InfoDeposit, to see where the L2 chain is derived from
func L1BlockInfoFromBytes(data []byte) (*L1BlockInfo, error) {
	var info L1BlockInfo
	return &info, info.unmarshalBinaryJovian(data)
}

// L1InfoDeposit creates a L1 Info deposit transaction based on the L1 block,
// and the L2 block-height difference with the start of the epoch.
func L1InfoDeposit(l1ChainConfig *params.ChainConfig, sysCfg eth.SystemConfig, seqNumber uint64, block eth.BlockInfo) (*optypes.DepositTx, error) {
	scalars, err := sysCfg.EcotoneScalars()
	if err != nil {
		return nil, err
	}
	operatorFee := sysCfg.OperatorFee()
	l1BlockInfo := L1BlockInfo{
		Number:              block.NumberU64(),
		Time:                block.Time(),
		BaseFee:             block.BaseFee(),
		BlockHash:           block.Hash(),
		SequenceNumber:      seqNumber,
		BatcherAddr:         sysCfg.BatcherAddr,
		BlobBaseFee:         block.BlobBaseFee(l1ChainConfig),
		BaseFeeScalar:       scalars.BaseFeeScalar,
		BlobBaseFeeScalar:   scalars.BlobBaseFeeScalar,
		OperatorFeeScalar:   operatorFee.Scalar,
		OperatorFeeConstant: operatorFee.Constant,
	}
	if l1BlockInfo.BlobBaseFee == nil {
		// The L2 spec states to use the MIN_BLOB_GASPRICE from EIP-4844 if not yet active on L1.
		l1BlockInfo.BlobBaseFee = big.NewInt(1)
	}
	// Use setter to make sure 0 is translated to default value.
	l1BlockInfo.SetDAFootprintGasScalarOrDefault(sysCfg.DAFootprintGasScalar)

	data, err := l1BlockInfo.marshalBinaryJovian()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Jovian l1 block info: %w", err)
	}
	source := L1InfoDepositSource{
		L1BlockHash: block.Hash(),
		SeqNumber:   seqNumber,
	}
	return &optypes.DepositTx{
		SourceHash: source.SourceHash(),
		From:       L1InfoDepositerAddress,
		To:         &L1BlockAddress,
		Mint:       nil,
		Value:      big.NewInt(0),
		Gas:        RegolithSystemTxGas,
		Data:       data,
	}, nil
}

// L1InfoDepositBytes returns a serialized L1-info attributes transaction.
func L1InfoDepositBytes(l1ChainConfig *params.ChainConfig, sysCfg eth.SystemConfig, seqNumber uint64, l1Info eth.BlockInfo) ([]byte, error) {
	dep, err := L1InfoDeposit(l1ChainConfig, sysCfg, seqNumber, l1Info)
	if err != nil {
		return nil, fmt.Errorf("failed to create L1 info tx: %w", err)
	}
	opaqueL1Tx, err := dep.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to encode L1 info tx: %w", err)
	}
	return opaqueL1Tx, nil
}
