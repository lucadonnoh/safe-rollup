package altda

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
)

// ErrCommitmentTypeMismatch is returned when a commitment does not have the configured type.
var ErrCommitmentTypeMismatch = errors.New("commitment type mismatch")

// DAStorage stores and serves batch data by commitment.
type DAStorage interface {
	GetInput(ctx context.Context, key CommitmentData) ([]byte, error)
	SetInput(ctx context.Context, img []byte) (CommitmentData, error)
}

type Config struct {
	// CommitmentType is the type of commitments the batcher posts on L1.
	CommitmentType CommitmentType
}

// DA resolves the commitments the batcher posts on L1 through the offchain DA service.
// Nothing can force the data onchain: until the service serves it, derivation waits.
type DA struct {
	log     log.Logger
	cfg     Config
	storage DAStorage
}

func NewAltDA(log log.Logger, cli CLIConfig, cfg Config) *DA {
	return NewAltDAWithStorage(log, cfg, cli.NewDAClient())
}

func NewAltDAWithStorage(log log.Logger, cfg Config, storage DAStorage) *DA {
	return &DA{log: log, cfg: cfg, storage: storage}
}

// GetInput fetches and verifies the data behind a commitment. It returns ErrNotFound while the
// DA service does not have it.
func (d *DA) GetInput(ctx context.Context, comm CommitmentData) (eth.Data, error) {
	if d.cfg.CommitmentType != comm.CommitmentType() {
		return nil, fmt.Errorf("expected %v, got %v: %w", d.cfg.CommitmentType, comm.CommitmentType(), ErrCommitmentTypeMismatch)
	}
	data, err := d.storage.GetInput(ctx, comm)
	if err != nil {
		return nil, err
	}
	if err := comm.Verify(data); err != nil {
		return nil, err
	}
	return data, nil
}
