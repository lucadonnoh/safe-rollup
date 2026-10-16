package eip1559

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateHolocene1559Params(t *testing.T) {
	tests := []struct {
		name     string
		params   []byte
		expected string
	}{
		{
			name:     "Wrong Length",
			params:   []byte{0x00, 0x01},
			expected: "holocene eip-1559 params should be 8 bytes, got 2",
		},
		{
			name:     "Zero denominator, non-zero elasticity",
			params:   []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
			expected: "holocene params cannot have a 0 denominator unless elasticity is also 0",
		},
		{
			name:     "Zero elasticity, non-zero denominator",
			params:   []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00},
			expected: "holocene params cannot have a 0 elasticity unless denominator is also 0",
		},
		{
			name:   "Both zero (valid)",
			params: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name:   "Both non-zero (valid)",
			params: []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHolocene1559Params(tc.params)
			if tc.expected == "" && err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			if tc.expected != "" && (err == nil || err.Error() != tc.expected) {
				t.Errorf("Expected error: %s, but got: %v", tc.expected, err)
			}
		})
	}
}

func TestValidateJovianExtraData(t *testing.T) {
	require.NoError(t, ValidateJovianExtraData(EncodeJovianExtraData(250, 6, 7)))
	require.EqualError(t, ValidateJovianExtraData(make([]byte, 9)), "Jovian extraData should be 17 bytes, got 9")
	bad := EncodeJovianExtraData(250, 6, 7)
	bad[0] = 0
	require.EqualError(t, ValidateJovianExtraData(bad), "Jovian extraData version byte should be 1, got 0")
	require.EqualError(t, ValidateJovianExtraData(EncodeJovianExtraData(0, 6, 7)), "holocene extraData must encode a non-zero denominator")

	d, e, m := DecodeJovianExtraData(EncodeJovianExtraData(250, 6, 7))
	require.Equal(t, uint64(250), d)
	require.Equal(t, uint64(6), e)
	require.Equal(t, uint64(7), *m)
}
