package sources

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/sources/caching"
)

type EngineClientConfig struct {
	L2ClientConfig
}

func EngineClientDefaultConfig(config *rollup.Config) *EngineClientConfig {
	return &EngineClientConfig{
		// engine is trusted, no need to recompute responses etc.
		L2ClientConfig: *L2ClientDefaultConfig(config, true),
	}
}

// EngineClient extends L2Client with engine API bindings.
type EngineClient struct {
	*L2Client
	*EngineAPIClient
}

func NewEngineClient(client client.RPC, log log.Logger, metrics caching.Metrics, config *EngineClientConfig) (*EngineClient, error) {
	l2Client, err := NewL2Client(client, log, metrics, &config.L2ClientConfig)
	if err != nil {
		return nil, err
	}

	engineAPIClient := NewEngineAPIClient(client, log)

	return &EngineClient{
		L2Client:        l2Client,
		EngineAPIClient: engineAPIClient,
	}, nil
}

// EngineAPIClient is an RPC client for the Engine API functions.
type EngineAPIClient struct {
	RPC client.RPC
	log log.Logger
}

// NewEngineAPIClient talks to an execution engine on which every fork through Jovian (Prague on L1
// terms) is active, so it always uses forkchoiceUpdatedV3, newPayloadV4 and getPayloadV4.
func NewEngineAPIClient(rpc client.RPC, l log.Logger) *EngineAPIClient {
	return &EngineAPIClient{
		RPC: rpc,
		log: l,
	}
}

// ForkchoiceUpdate updates the forkchoice on the execution client. If attributes is not nil, the engine client will also begin building a block
// based on attributes after the new head block and return the payload ID.
// It's the caller's responsibility to check the error type, and in case of an rpc.Error, check the ErrorCode.
func (s *EngineAPIClient) ForkchoiceUpdate(ctx context.Context, fc *eth.ForkchoiceState, attributes *eth.PayloadAttributes) (*eth.ForkchoiceUpdatedResult, error) {
	llog := s.log.New("state", fc)       // local logger
	tlog := llog.New("attr", attributes) // trace logger
	tlog.Trace("Sharing forkchoice-updated signal")
	var result eth.ForkchoiceUpdatedResult
	err := s.RPC.CallContext(ctx, &result, string(eth.FCUV3), fc, attributes)
	if err != nil {
		llog.Warn("Failed to share forkchoice-updated signal", "err", err)
		return nil, err
	}
	tlog.Trace("Shared forkchoice-updated signal")
	if attributes != nil { // block building is optional, we only get a payload ID if we are building a block
		tlog.Trace("Received payload id", "payloadId", result.PayloadID)
	}
	return &result, nil
}

// NewPayload executes a full block on the execution engine.
// This returns a PayloadStatusV1 which encodes any validation/processing error,
// and this type of error is kept separate from the returned `error` used for RPC errors, like timeouts.
func (s *EngineAPIClient) NewPayload(ctx context.Context, payload *eth.ExecutionPayload, parentBeaconBlockRoot *common.Hash) (*eth.PayloadStatusV1, error) {
	e := s.log.New("block_hash", payload.BlockHash)
	e.Trace("sending payload for execution")

	var result eth.PayloadStatusV1

	err := s.RPC.CallContext(ctx, &result, string(eth.NewPayloadV4), payload, []common.Hash{}, parentBeaconBlockRoot, []hexutil.Bytes{})

	e.Trace("Received payload execution result", "status", result.Status, "latestValidHash", result.LatestValidHash, "message", result.ValidationError)
	if err != nil {
		e.Error("Payload execution failed", "err", err)
		return nil, fmt.Errorf("failed to execute payload: %w", err)
	}
	return &result, nil
}

// GetPayload gets the execution payload associated with the PayloadId.
// It's the caller's responsibility to check the error type, and in case of an rpc.Error, check the ErrorCode.
func (s *EngineAPIClient) GetPayload(ctx context.Context, payloadInfo eth.PayloadInfo) (*eth.ExecutionPayloadEnvelope, error) {
	e := s.log.New("payload_id", payloadInfo.ID)
	e.Trace("getting payload")
	var result eth.ExecutionPayloadEnvelope
	err := s.RPC.CallContext(ctx, &result, string(eth.GetPayloadV4), payloadInfo.ID)
	if err != nil {
		e.Warn("Failed to get payload", "payload_id", payloadInfo.ID, "err", err)
		return nil, err
	}
	e.Trace("Received payload")
	return &result, nil
}
