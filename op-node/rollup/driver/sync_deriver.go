package driver

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-node/rollup/engine"
	"github.com/ethereum-optimism/optimism/op-node/rollup/status"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/event"
	"github.com/ethereum-optimism/optimism/op-service/log"
)

type SyncDeriver struct {
	// The derivation pipeline is reset whenever we reorg.
	// The derivation pipeline determines the new l2Safe.
	Derivation DerivationPipeline

	// The engine controller is used by the sequencer & Derivation components.
	// We will also use it for EL sync in a future PR.
	Engine *engine.EngineController

	// Sync Mod Config
	SyncCfg *sync.Config

	Config *rollup.Config

	L1 L1Chain
	// Track L1 view when new unsafe L1 block is observed
	L1Tracker *status.L1Tracker
	L2        L2Chain

	Emitter event.Emitter

	Log log.Logger

	Ctx context.Context

	StepDeriver StepDeriver
}

func (s *SyncDeriver) AttachEmitter(em event.Emitter) {
	s.Emitter = em
}

func (s *SyncDeriver) OnL1Unsafe(ctx context.Context) {
	// a new L1 head may mean we have the data to not get an EOF again.
	s.StepDeriver.RequestStep(ctx, false)
}

func (s *SyncDeriver) OnL1Finalized(ctx context.Context) {
	// On "safe" L1 blocks: no step, justified L1 information does not do anything for L2 derivation or status.
	// On "finalized" L1 blocks: we may be able to mark more L2 data as finalized now.
	s.StepDeriver.RequestStep(ctx, false)
}

func (s *SyncDeriver) OnEvent(ctx context.Context, ev event.Event) bool {
	switch x := ev.(type) {
	case StepEvent:
		s.SyncStep()
	case rollup.ResetEvent:
		s.onResetEvent(ctx, x)
	case rollup.L1TemporaryErrorEvent:
		s.Log.Warn("L1 temporary error", "err", x.Err)
		s.StepDeriver.RequestStep(ctx, false)
	case rollup.EngineTemporaryErrorEvent:
		s.Log.Warn("Engine temporary error", "err", x.Err)
		// Make sure that for any temporarily failed attributes we retry processing.
		// This will be triggered by a step. After appropriate backoff.
		s.StepDeriver.RequestStep(ctx, false)
	case engine.EngineResetConfirmedEvent:
		s.onEngineConfirmedReset(ctx, x)
	case derive.DeriverIdleEvent:
		// Once derivation is idle the system is healthy
		// and we can wait for new inputs. No backoff necessary.
		s.StepDeriver.ResetStepBackoff(ctx)
	case derive.DeriverMoreEvent:
		// If there is more data to process,
		// continue derivation quickly
		s.StepDeriver.RequestStep(ctx, true)
	default:
		return false
	}
	return true
}

func (s *SyncDeriver) OnUnsafeL2Payload(ctx context.Context, envelope *eth.ExecutionPayloadEnvelope) {
	s.Log.Debug("OnUnsafeL2Payload called", "id", envelope.ExecutionPayload.ID(), "syncMode", s.SyncCfg.SyncMode)

	switch s.SyncCfg.SyncMode {
	case sync.CLSync:
		s.Engine.AddUnsafePayload(ctx, envelope)
	case sync.ELSync:
		ref, err := derive.PayloadToBlockRef(s.Config, envelope.ExecutionPayload)
		if err != nil {
			s.Log.Error("Failed to turn execution payload into a block ref", "id", envelope.ExecutionPayload.ID(), "err", err)
			return
		}
		if ref.Number <= s.Engine.UnsafeL2Head().Number {
			return
		}
		s.Log.Info("Inserting unsafe L2 execution payload to drive EL sync", "id", envelope.ExecutionPayload.ID())
		if err := s.Engine.InsertUnsafePayload(s.Ctx, envelope, ref); err != nil {
			s.Log.Warn("Failed to insert unsafe payload for EL sync", "id", envelope.ExecutionPayload.ID(), "err", err)
		}
	}
}

func (s *SyncDeriver) onEngineConfirmedReset(ctx context.Context, x engine.EngineResetConfirmedEvent) {
	s.Log.Info("Confirming pipeline reset")
	s.Emitter.Emit(ctx, derive.ConfirmPipelineResetEvent{})
}

func (s *SyncDeriver) onResetEvent(ctx context.Context, x rollup.ResetEvent) {
	// If the system corrupts, e.g. due to a reorg, simply reset it
	s.Log.Warn("Deriver system is resetting", "err", x.Err)
	s.Emitter.Emit(ctx, engine.ResetEngineRequestEvent{})
	s.StepDeriver.RequestStep(ctx, false)
}

func (s *SyncDeriver) tryBackupUnsafeReorg() {
	// Map a failed restore of the unsafe head from backupUnsafe to its event. TryBackupUnsafeReorg
	// should emit these events directly once there are no more synchronous callers.
	if err := s.Engine.TryBackupUnsafeReorg(s.Ctx); err != nil {
		if errors.Is(err, derive.ErrReset) {
			s.Emitter.Emit(s.Ctx, rollup.ResetEvent{Err: err})
		} else if errors.Is(err, derive.ErrTemporary) {
			s.Emitter.Emit(s.Ctx, rollup.EngineTemporaryErrorEvent{Err: err})
		} else {
			s.Emitter.Emit(s.Ctx, rollup.CriticalErrorEvent{
				Err: fmt.Errorf("unexpected TryBackupUnsafeReorg error type: %w", err),
			})
		}
	}
}

// SyncStep performs the sequence of encapsulated syncing steps.
// Warning: this sequence will be broken apart as outlined in op-node derivers design doc.
func (s *SyncDeriver) SyncStep() {
	s.Log.Debug("Sync process step")

	s.tryBackupUnsafeReorg()

	if s.Engine.IsEngineInitialELSyncing() {
		// The pipeline cannot move forwards if doing initial EL sync.
		s.Log.Debug("Rollup driver is backing off because execution engine is performing initial EL sync.",
			"unsafe_head", s.Engine.UnsafeL2Head())
		s.StepDeriver.ResetStepBackoff(s.Ctx)
		return
	}

	// Any now processed forkchoice updates will trigger CL-sync payload processing, if any payload is queued up.

	// Since we don't force attributes to be processed at this point,
	// we cannot safely directly trigger the derivation, as that may generate new attributes that
	// conflict with what attributes have not been applied yet.
	// Instead, we request the engine to repeat where its pending-safe head is at.
	// Upon the pending-safe signal the attributes deriver can then ask the pipeline
	// to generate new attributes, if no attributes are known already.
	s.Engine.RequestPendingSafeUpdate(s.Ctx)

}
