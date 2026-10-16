package rollup

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethereum-optimism/optimism/op-service/eth"
)

// VerifierHeadSource classifies the origin of a head reported by the SuperAuthority.
type VerifierHeadSource uint8

const (
	// VerifierHeadPreActivation: the registered verifier is inactive at the
	// current local-safe timestamp. Caller uses local-safe / local-finalized.
	VerifierHeadPreActivation VerifierHeadSource = iota
	// VerifierHeadAnchor: the active verifier has no verified-DB entry for this
	// chain yet. Block is zero; Timestamp is the pre-activation cap
	// (`activationTimestamp - 1`); caller resolves the canonical L2 block at
	// that timestamp.
	VerifierHeadAnchor
	// VerifierHeadVerified: Block is the verified tip from the verifier.
	VerifierHeadVerified
)

// Exhaustive — adding a variant requires updating every consumer's switch.
func (s VerifierHeadSource) String() string {
	switch s {
	case VerifierHeadPreActivation:
		return "pre-activation"
	case VerifierHeadAnchor:
		return "anchor"
	case VerifierHeadVerified:
		return "verified"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(s))
	}
}

// VerifierHead is the result of a SuperAuthority head query.
//   - Source == PreActivation: Block and Timestamp are zero; caller uses local.
//   - Source == Verified:      Block is the verified tip; Timestamp is its L2 time.
//   - Source == Anchor:        Block is zero; Timestamp is the pre-activation cap
//     (`activationTimestamp - 1`); caller resolves the canonical L2 block at that
//     timestamp itself.
type VerifierHead struct {
	Block     eth.BlockID
	Timestamp uint64
	Source    VerifierHeadSource
}

// SuperAuthority is the cross-chain attestation surface a supernode exposes to
// op-node: cross-verified safe / finalized head reporting and payload deny-list
// checks. Returned heads are consumed by the engine controller to choose what
// to publish as SafeL2Head / FinalizedHead and whether to apply a payload.
type SuperAuthority interface {
	// FullyVerifiedL2Head returns the cross-verified safe L2 head.
	// `ok=false` signals a transient read failure — caller must hold the
	// previous value (floored at FinalizedHead), never fall back to local-safe.
	FullyVerifiedL2Head(ctx context.Context) (head VerifierHead, ok bool)

	// FinalizedL2Head is the finalized analogue of FullyVerifiedL2Head.
	// Finalized blocks cannot reorg, so the caller may cache the result.
	FinalizedL2Head(ctx context.Context) (head VerifierHead, ok bool)

	// IsDenied reports whether a payload hash is denied at the given block
	// number. Errors are logged but not fatal.
	IsDenied(blockNumber uint64, payloadHash common.Hash) (bool, error)

	// MaxDeniedHeight returns the highest denied block height, and whether any
	// denial exists. Consulted on every unsafe payload insert, so it must be
	// cheap (a local lookup, no network I/O). A read error is returned rather
	// than reported as "no denials", so callers can log it.
	MaxDeniedHeight() (uint64, bool, error)
}
