package derive

import (
	"context"
	"errors"
	"fmt"

	altda "github.com/ethereum-optimism/optimism/op-alt-da"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive/params"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
)

// AltDADataSource resolves the commitments posted to the batch inbox into batch data fetched
// from the DA service.
type AltDADataSource struct {
	log     log.Logger
	src     DataIter
	fetcher AltDAInputFetcher
	// comm is the commitment whose data could not be fetched yet, retried on the next call.
	comm altda.Commitment
}

func NewAltDADataSource(log log.Logger, src DataIter, fetcher AltDAInputFetcher) *AltDADataSource {
	return &AltDADataSource{log: log, src: src, fetcher: fetcher}
}

func (s *AltDADataSource) Next(ctx context.Context) (eth.Data, error) {
	if s.comm == nil {
		data, err := s.src.Next(ctx)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || data[0] != params.DerivationVersion1 {
			s.log.Warn("skipping inbox data that is not a DA commitment")
			return s.Next(ctx)
		}
		comm, err := altda.DecodeCommitment(data[1:])
		if err != nil {
			s.log.Warn("invalid commitment", "commitment", data, "err", err)
			return s.Next(ctx)
		}
		s.comm = comm
	}
	data, err := s.fetcher.GetInput(ctx, s.comm)
	if errors.Is(err, altda.ErrCommitmentMismatch) {
		s.log.Warn("skipping invalid commitment", "comm", s.comm, "err", err)
		s.comm = nil
		return s.Next(ctx)
	} else if err != nil {
		// Includes altda.ErrNotFound: derivation waits until the DA service serves the data.
		return nil, NewTemporaryError(fmt.Errorf("failed to fetch input data with comm %s from da service: %w", s.comm, err))
	}
	s.comm = nil
	return data, nil
}
