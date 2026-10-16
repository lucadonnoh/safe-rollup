package fees

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRollupCostData(t *testing.T) {
	rcd := NewRollupCostData([]byte{0x00, 0x01, 0x00, 0x02, 0x00})
	require.Equal(t, uint64(3), rcd.Zeroes)
	require.Equal(t, uint64(2), rcd.Ones)
	require.Equal(t, uint64(FlzCompressLen([]byte{0x00, 0x01, 0x00, 0x02, 0x00})), rcd.FastLzSize)

	empty := NewRollupCostData(nil)
	require.Equal(t, RollupCostData{}, empty)
}

func TestEstimatedDASize(t *testing.T) {
	// Below the regression minimum, the estimate clamps to MinTransactionSize (100).
	small := RollupCostData{FastLzSize: 50}
	require.Equal(t, MinTransactionSize, small.EstimatedDASize())

	// Above the minimum: floor((-42_585_600 + 836_500*200) / 1e6) = floor(124.7144) = 124.
	large := RollupCostData{FastLzSize: 200}
	require.Equal(t, big.NewInt(124), large.EstimatedDASize())
}
