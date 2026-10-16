package altda

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/optimism/op-node/rollup/derive/params"
)

func TestCommitment(t *testing.T) {
	data := []byte("frame")
	comm := NewCommitment(data)
	require.NoError(t, comm.Verify(data))
	require.ErrorIs(t, comm.Verify([]byte("other")), ErrCommitmentMismatch)

	txData := comm.TxData()
	require.Len(t, txData, 34)
	require.Equal(t, byte(params.DerivationVersion1), txData[0])

	decoded, err := DecodeCommitment(txData[1:])
	require.NoError(t, err)
	require.Equal(t, comm, decoded)

	for _, bad := range [][]byte{nil, txData[1:33], append([]byte{1}, comm...), append(txData[1:], 0)} {
		_, err := DecodeCommitment(bad)
		require.ErrorIs(t, err, ErrInvalidCommitment)
	}
}
