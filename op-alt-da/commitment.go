package altda

import (
	"bytes"
	"encoding/hex"
	"errors"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/ethereum-optimism/optimism/op-node/rollup/derive/params"
)

// ErrInvalidCommitment is returned when inbox data is not a keccak256 commitment.
var ErrInvalidCommitment = errors.New("invalid commitment")

// ErrCommitmentMismatch is returned when the data does not hash to the commitment.
var ErrCommitmentMismatch = errors.New("commitment mismatch")

// keccak256CommitmentType is the type byte of keccak256 commitments in the alt-DA format.
const keccak256CommitmentType = 0

// Commitment is the keccak256 hash of a batcher frame. The batcher stores the frame in the DA
// service under this key and posts TxData() to the batch inbox.
type Commitment []byte

// NewCommitment commits to the given data.
func NewCommitment(data []byte) Commitment {
	return crypto.Keccak256(data)
}

// DecodeCommitment parses an encoded commitment: the type byte followed by the 32-byte hash.
func DecodeCommitment(encoded []byte) (Commitment, error) {
	if len(encoded) != 33 || encoded[0] != keccak256CommitmentType {
		return nil, ErrInvalidCommitment
	}
	return Commitment(encoded[1:]), nil
}

// Encode prefixes the hash with the commitment type. It is the key in the DA service.
func (c Commitment) Encode() []byte {
	return append([]byte{keccak256CommitmentType}, c...)
}

// TxData is the batch inbox calldata: the derivation version byte followed by the encoding.
func (c Commitment) TxData() []byte {
	return append([]byte{params.DerivationVersion1}, c.Encode()...)
}

// Verify checks that the data hashes to the commitment.
func (c Commitment) Verify(data []byte) error {
	if !bytes.Equal(c, crypto.Keccak256(data)) {
		return ErrCommitmentMismatch
	}
	return nil
}

func (c Commitment) String() string {
	return hex.EncodeToString(c.Encode())
}
