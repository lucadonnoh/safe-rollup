package txmgr

import (
	"context"
	"errors"
	"math/big"
)

type GasPriceEstimatorFn func(ctx context.Context, backend ETHBackend) (*big.Int, *big.Int, error)

func DefaultGasPriceEstimatorFn(ctx context.Context, backend ETHBackend) (*big.Int, *big.Int, error) {
	tip, err := backend.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, err
	}
	head, err := backend.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	if head.BaseFee == nil {
		return nil, nil, errors.New("txmgr does not support pre-london blocks that do not have a base fee")
	}
	return tip, head.BaseFee, nil
}
