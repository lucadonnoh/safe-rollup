package derive

import (
	"fmt"

	"github.com/ethereum-optimism/optimism/op-core/eip1559"
	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
)

// payloadL1InfoDeposit decodes the L1 info from a payload's first transaction,
// which must be the L1-info deposit.
func payloadL1InfoDeposit(rollupCfg *rollup.Config, payload *eth.ExecutionPayload) (*L1BlockInfo, error) {
	if len(payload.Transactions) == 0 {
		return nil, fmt.Errorf("l2 block is missing L1 info deposit tx, block hash: %s", payload.BlockHash)
	}
	deposit, err := optypes.UnmarshalDepositTx(payload.Transactions[0])
	if err != nil {
		return nil, fmt.Errorf("failed to decode L1 info deposit tx from L2 block: %w", err)
	}
	info, err := L1BlockInfoFromBytes(deposit.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse L1 info deposit tx from L2 block: %w", err)
	}
	return info, nil
}

// PayloadToBlockRef extracts the essential L2BlockRef information from an execution payload,
// falling back to genesis information if necessary.
func PayloadToBlockRef(rollupCfg *rollup.Config, payload *eth.ExecutionPayload) (eth.L2BlockRef, error) {
	genesis := &rollupCfg.Genesis
	var l1Origin eth.BlockID
	var sequenceNumber uint64
	if uint64(payload.BlockNumber) == genesis.L2.Number {
		if payload.BlockHash != genesis.L2.Hash {
			return eth.L2BlockRef{}, fmt.Errorf("expected L2 genesis hash to match L2 block at genesis block number %d: %s <> %s", genesis.L2.Number, payload.BlockHash, genesis.L2.Hash)
		}
		l1Origin = genesis.L1
		sequenceNumber = 0
	} else {
		info, err := payloadL1InfoDeposit(rollupCfg, payload)
		if err != nil {
			return eth.L2BlockRef{}, err
		}
		l1Origin = eth.BlockID{Hash: info.BlockHash, Number: info.Number}
		sequenceNumber = info.SequenceNumber
	}

	return eth.L2BlockRef{
		Hash:           payload.BlockHash,
		Number:         uint64(payload.BlockNumber),
		ParentHash:     payload.ParentHash,
		Time:           uint64(payload.Timestamp),
		L1Origin:       l1Origin,
		SequenceNumber: sequenceNumber,
	}, nil
}

func PayloadToSystemConfig(rollupCfg *rollup.Config, payload *eth.ExecutionPayload) (eth.SystemConfig, error) {
	if uint64(payload.BlockNumber) == rollupCfg.Genesis.L2.Number {
		if payload.BlockHash != rollupCfg.Genesis.L2.Hash {
			return eth.SystemConfig{}, fmt.Errorf(
				"expected L2 genesis hash to match L2 block at genesis block number %d: %s <> %s",
				rollupCfg.Genesis.L2.Number, payload.BlockHash, rollupCfg.Genesis.L2.Hash)
		}
		return rollupCfg.Genesis.SystemConfig, nil
	}

	info, err := payloadL1InfoDeposit(rollupCfg, payload)
	if err != nil {
		return eth.SystemConfig{}, err
	}
	// We do not know if the scalar was derived from a v0 or v1 scalar, but v1 is fine, a 0 blob
	// base fee has the same effect.
	r := eth.SystemConfig{
		BatcherAddr: info.BatcherAddr,
		Scalar: eth.EncodeScalar(eth.EcotoneScalars{
			BlobBaseFeeScalar: info.BlobBaseFeeScalar,
			BaseFeeScalar:     info.BaseFeeScalar,
		}),
		GasLimit: uint64(payload.GasLimit),
		OperatorFeeParams: eth.EncodeOperatorFeeParams(eth.OperatorFeeParams{
			Scalar:   info.OperatorFeeScalar,
			Constant: info.OperatorFeeConstant,
		}),
		DAFootprintGasScalar: info.DAFootprintGasScalar,
	}

	if err := eip1559.ValidateJovianExtraData(payload.ExtraData); err != nil {
		return eth.SystemConfig{}, err
	}
	d, e, m := eip1559.DecodeJovianExtraData(payload.ExtraData)
	copy(r.EIP1559Params[:], eip1559.EncodeHolocene1559Params(d, e))

	// ValidateOptimismExtraData returning a nil error guarantees that m is not nil
	r.MinBaseFee = *m
	return r, nil
}
