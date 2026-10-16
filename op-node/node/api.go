package node

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/version"
	"github.com/ethereum-optimism/optimism/op-service/apis"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/rpc"
)

type l2EthClient interface {
	InfoByHash(ctx context.Context, hash common.Hash) (eth.BlockInfo, error)
}

type driverClient interface {
	SyncStatus(ctx context.Context) (*eth.SyncStatus, error)
	BlockRefWithStatus(ctx context.Context, num uint64) (eth.L2BlockRef, *eth.SyncStatus, error)
	ResetDerivationPipeline(context.Context) error
	StartSequencer(ctx context.Context, blockHash common.Hash) error
	StopSequencer(context.Context) (common.Hash, error)
	SequencerActive(context.Context) (bool, error)
	OnUnsafeL2Payload(ctx context.Context, payload *eth.ExecutionPayloadEnvelope)
	SetRecoverMode(ctx context.Context, mode bool) error
}

type adminAPI struct {
	*rpc.CommonAdminAPI
	dr driverClient
}

var _ apis.OpnodeAdminServer = (*adminAPI)(nil)

func NewAdminAPI(dr driverClient, log log.Logger) *adminAPI {
	return &adminAPI{
		CommonAdminAPI: rpc.NewCommonAdminAPI(log),
		dr:             dr,
	}
}

func (n *adminAPI) ResetDerivationPipeline(ctx context.Context) error {
	return n.dr.ResetDerivationPipeline(ctx)
}

func (n *adminAPI) StartSequencer(ctx context.Context, blockHash common.Hash) error {
	return n.dr.StartSequencer(ctx, blockHash)
}

func (n *adminAPI) StopSequencer(ctx context.Context) (common.Hash, error) {
	return n.dr.StopSequencer(ctx)
}

func (n *adminAPI) SequencerActive(ctx context.Context) (bool, error) {
	return n.dr.SequencerActive(ctx)
}

// PostUnsafePayload is a special API that allows posting an unsafe payload to the L2 derivation pipeline.
// It should only be used by op-conductor for sequencer failover scenarios.
func (n *adminAPI) PostUnsafePayload(ctx context.Context, envelope *eth.ExecutionPayloadEnvelope) error {
	payload := envelope.ExecutionPayload
	if actual, ok := envelope.CheckBlockHash(); !ok {
		log.Error("payload has bad block hash", "bad_hash", payload.BlockHash.String(), "actual", actual.String())
		return fmt.Errorf("payload has bad block hash: %s, actual block hash is: %s", payload.BlockHash.String(), actual.String())
	}
	n.dr.OnUnsafeL2Payload(ctx, envelope)
	return nil
}

func (n *adminAPI) SetRecoverMode(ctx context.Context, mode bool) error {
	return n.dr.SetRecoverMode(ctx, mode)
}

type nodeAPI struct {
	config *rollup.Config
	client l2EthClient
	dr     driverClient
	log    log.Logger
}

var _ apis.RollupNodeServer = (*nodeAPI)(nil)

func NewNodeAPI(config *rollup.Config, l2Client l2EthClient, dr driverClient, log log.Logger) *nodeAPI {
	return &nodeAPI{
		config: config,
		client: l2Client,
		dr:     dr,
		log:    log,
	}
}

func (n *nodeAPI) SyncStatus(ctx context.Context) (*eth.SyncStatus, error) {
	return n.dr.SyncStatus(ctx)
}

func (n *nodeAPI) RollupConfig(_ context.Context) (*rollup.Config, error) {
	return n.config, nil
}

func (n *nodeAPI) Version(ctx context.Context) (string, error) {
	return version.Version + "-" + version.Meta, nil
}
