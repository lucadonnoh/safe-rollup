// Package fees estimates the data-availability size of OP-Stack transactions with the Fjord
// FastLZ regression. Consumers import the package as opfees.
package fees

import "math/big"

var (
	// L1CostIntercept and L1CostFastlzCoef are the coefficients of the Fjord linear
	// regression that estimates a transaction's compressed DA size from its FastLZ size.
	L1CostIntercept  = big.NewInt(-42_585_600)
	L1CostFastlzCoef = big.NewInt(836_500)

	// MinTransactionSize is the lower bound the Fjord DA-size estimate is clamped to.
	MinTransactionSize       = big.NewInt(100)
	MinTransactionSizeScaled = new(big.Int).Mul(MinTransactionSize, big.NewInt(1e6))
)

// RollupCostData carries the byte counts of a transaction's L1 representation needed to
// compute its data-availability cost.
type RollupCostData struct {
	Zeroes, Ones uint64
	FastLzSize   uint64
}

// NewRollupCostData computes the RollupCostData for the given L1 transaction bytes.
func NewRollupCostData(data []byte) (out RollupCostData) {
	for _, b := range data {
		if b == 0 {
			out.Zeroes++
		} else {
			out.Ones++
		}
	}
	out.FastLzSize = uint64(FlzCompressLen(data))
	return out
}

// EstimatedDASize estimates the number of bytes the transaction occupies in its DA batch
// using the Fjord linear-regression model.
func (cd RollupCostData) EstimatedDASize() *big.Int {
	fastLzSize := new(big.Int).SetUint64(cd.FastLzSize)
	estimatedSize := new(big.Int).Add(L1CostIntercept, new(big.Int).Mul(L1CostFastlzCoef, fastLzSize))
	if estimatedSize.Cmp(MinTransactionSizeScaled) < 0 {
		estimatedSize.Set(MinTransactionSizeScaled)
	}
	return estimatedSize.Div(estimatedSize, big.NewInt(1e6))
}
