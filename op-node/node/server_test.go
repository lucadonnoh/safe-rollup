package node

import (
	"context"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/version"
	rpcclient "github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
)

func TestVersion(t *testing.T) {
	log := testlog.Logger(t, log.LevelError)
	l2Client := &testutils.MockL2Client{}
	drClient := &mockDriverClient{}
	rpcCfg := &oprpc.CLIConfig{
		ListenAddr: "localhost",
		ListenPort: 0,
	}
	rollupCfg := &rollup.Config{
		// ignore other rollup config info in this test
	}
	server := newRPCServer(rpcCfg, rollupCfg, l2Client, drClient, log, "0.0")
	assert.NoError(t, server.Start())
	defer func() {
		require.NoError(t, server.Stop())
	}()

	client, err := rpcclient.NewRPC(context.Background(), log, "http://"+server.Endpoint(), rpcclient.WithDialAttempts(3))
	assert.NoError(t, err)

	var out string
	err = client.CallContext(context.Background(), &out, "optimism_version")
	assert.NoError(t, err)
	assert.Equal(t, version.Version+"-"+version.Meta, out)
}

func randomSyncStatus(rng *rand.Rand) *eth.SyncStatus {
	return &eth.SyncStatus{
		CurrentL1:          testutils.RandomBlockRef(rng),
		CurrentL1Finalized: testutils.RandomBlockRef(rng),
		HeadL1:             testutils.RandomBlockRef(rng),
		SafeL1:             testutils.RandomBlockRef(rng),
		FinalizedL1:        testutils.RandomBlockRef(rng),
		UnsafeL2:           testutils.RandomL2BlockRef(rng),
		SafeL2:             testutils.RandomL2BlockRef(rng),
		FinalizedL2:        testutils.RandomL2BlockRef(rng),
		PendingSafeL2:      testutils.RandomL2BlockRef(rng),
	}
}

func TestSyncStatus(t *testing.T) {
	log := testlog.Logger(t, log.LevelError)
	l2Client := &testutils.MockL2Client{}
	drClient := &mockDriverClient{}
	rng := rand.New(rand.NewSource(1234))
	status := randomSyncStatus(rng)
	drClient.On("SyncStatus").Return(status)

	rpcCfg := &oprpc.CLIConfig{
		ListenAddr: "localhost",
		ListenPort: 0,
	}
	rollupCfg := &rollup.Config{
		// ignore other rollup config info in this test
	}
	server := newRPCServer(rpcCfg, rollupCfg, l2Client, drClient, log, "0.0")
	assert.NoError(t, server.Start())
	defer func() {
		require.NoError(t, server.Stop())
	}()

	client, err := rpcclient.NewRPC(context.Background(), log, "http://"+server.Endpoint(), rpcclient.WithDialAttempts(3))
	assert.NoError(t, err)

	var out *eth.SyncStatus
	err = client.CallContext(context.Background(), &out, "optimism_syncStatus")
	assert.NoError(t, err)
	assert.Equal(t, status, out)
}

type mockDriverClient struct {
	mock.Mock
}

func (c *mockDriverClient) ExpectBlockRefWithStatus(num uint64, ref eth.L2BlockRef, status *eth.SyncStatus, err error) {
	c.Mock.On("BlockRefWithStatus", num).Return(ref, status, &err)
}

func (c *mockDriverClient) BlockRefWithStatus(ctx context.Context, num uint64) (eth.L2BlockRef, *eth.SyncStatus, error) {
	m := c.Mock.MethodCalled("BlockRefWithStatus", num)
	return m[0].(eth.L2BlockRef), m[1].(*eth.SyncStatus), *m[2].(*error)
}

func (c *mockDriverClient) SyncStatus(ctx context.Context) (*eth.SyncStatus, error) {
	return c.Mock.MethodCalled("SyncStatus").Get(0).(*eth.SyncStatus), nil
}

func (c *mockDriverClient) ResetDerivationPipeline(ctx context.Context) error {
	return c.Mock.MethodCalled("ResetDerivationPipeline").Get(0).(error)
}

func (c *mockDriverClient) StartSequencer(ctx context.Context, blockHash common.Hash) error {
	return c.Mock.MethodCalled("StartSequencer").Get(0).(error)
}

func (c *mockDriverClient) StopSequencer(ctx context.Context) (common.Hash, error) {
	return c.Mock.MethodCalled("StopSequencer").Get(0).(common.Hash), nil
}

func (c *mockDriverClient) SequencerActive(ctx context.Context) (bool, error) {
	return c.Mock.MethodCalled("SequencerActive").Get(0).(bool), nil
}

func (c *mockDriverClient) OnUnsafeL2Payload(ctx context.Context, payload *eth.ExecutionPayloadEnvelope) {
	c.Mock.MethodCalled("OnUnsafeL2Payload")
}

func (c *mockDriverClient) OverrideLeader(ctx context.Context) error {
	return c.Mock.MethodCalled("OverrideLeader").Get(0).(error)
}

func (c *mockDriverClient) ConductorEnabled(ctx context.Context) (bool, error) {
	return c.Mock.MethodCalled("ConductorEnabled").Get(0).(bool), nil
}

func (c *mockDriverClient) SetRecoverMode(ctx context.Context, mode bool) error {
	c.Mock.MethodCalled("SetRecoverMode")
	return nil
}
