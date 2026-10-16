package altda

import (
	"context"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
)

type memStore struct {
	mu sync.Mutex
	db map[string][]byte
}

func (s *memStore) Get(_ context.Context, key []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.db[string(key)]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}

func (s *memStore) Put(_ context.Context, key []byte, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db[string(key)] = value
	return nil
}

func TestDAClient(t *testing.T) {
	ctx := context.Background()
	store := &memStore{db: map[string][]byte{}}
	server := NewDAServer("127.0.0.1", 0, store, testlog.Logger(t, log.LevelDebug))
	require.NoError(t, server.Start())

	cfg := CLIConfig{DAServerURL: server.HttpEndpoint()}
	require.NoError(t, cfg.Check())
	client := cfg.NewDAClient()

	rng := rand.New(rand.NewSource(1234))
	randomData := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	input := randomData(2000)

	comm, err := client.SetInput(ctx, input)
	require.NoError(t, err)
	require.Equal(t, NewCommitment(input), comm)

	stored, err := client.GetInput(ctx, comm)
	require.NoError(t, err)
	require.Equal(t, input, stored)

	// The client rejects data that does not match the commitment.
	require.NoError(t, store.Put(ctx, comm.Encode(), []byte("bad data")))
	_, err = client.GetInput(ctx, comm)
	require.ErrorIs(t, err, ErrCommitmentMismatch)

	_, err = client.GetInput(ctx, NewCommitment(randomData(32)))
	require.ErrorIs(t, err, ErrNotFound)

	_, err = client.SetInput(ctx, []byte{})
	require.ErrorIs(t, err, ErrInvalidInput)

	require.NoError(t, server.Stop())
	_, err = client.SetInput(ctx, input)
	require.Error(t, err)
}
