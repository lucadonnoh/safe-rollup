package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
)

var (
	// ErrPayloadInvalid is returned when a payload's execution is invalid.
	ErrPayloadInvalid = errors.New("payload execution invalid")
)

type PayloadProcessEvent struct {
	// if payload should be promoted to (local) safe (must also be pending safe, see DerivedFrom)
	Concluding bool
	// payload is promoted to pending-safe if non-zero
	DerivedFrom  eth.L1BlockRef
	BuildStarted time.Time

	Envelope *eth.ExecutionPayloadEnvelope
	Ref      eth.L2BlockRef
}

func (ev PayloadProcessEvent) String() string {
	return "payload-process"
}

func (e *EngineController) onPayloadProcess(ctx context.Context, ev PayloadProcessEvent) {
	insertStarted, _, err := e.processNewPayload(ctx, ev.Envelope, ev.Ref, ev.DerivedFrom)
	if err != nil {
		return
	}
	e.emitter.Emit(ctx, PayloadSuccessEvent{
		Concluding:    ev.Concluding,
		DerivedFrom:   ev.DerivedFrom,
		BuildStarted:  ev.BuildStarted,
		InsertStarted: insertStarted,
		Envelope:      ev.Envelope,
		Ref:           ev.Ref,
	})
}

// processNewPayload handles the NewPayload RPC call.
// It does NOT acquire e.mu (caller is responsible).
// Returns the instants engine_newPayload started and returned, or an error. The
// second is what lets a caller price the newPayload call on its own, separately
// from the forkchoice update that follows it.
// Emits error events for other listeners, but does NOT emit PayloadSuccessEvent (caller's job).
func (e *EngineController) processNewPayload(ctx context.Context, envelope *eth.ExecutionPayloadEnvelope, ref eth.L2BlockRef, derivedFrom eth.L1BlockRef) (time.Time, time.Time, error) {
	rpcCtx, cancel := context.WithTimeout(e.ctx, payloadProcessTimeout)
	defer cancel()

	insertStart := time.Now()
	status, err := e.engine.NewPayload(rpcCtx,
		envelope.ExecutionPayload, envelope.ParentBeaconBlockRoot)
	newPayloadDone := time.Now()
	if err != nil {
		insertErr := fmt.Errorf("failed to insert execution payload: %w", err)
		e.emitter.Emit(ctx, rollup.EngineTemporaryErrorEvent{Err: insertErr})
		return time.Time{}, time.Time{}, insertErr
	}
	switch status.Status {
	case eth.ExecutionInvalid, eth.ExecutionInvalidBlockHash:
		// Depending on execution engine, not all block-validity checks run immediately on build-start
		// at the time of the forkchoiceUpdated engine-API call, nor during getPayload.
		if derivedFrom != (eth.L1BlockRef{}) {
			e.emitDepositsOnlyPayloadAttributesRequest(ctx, ref.ParentID(), derivedFrom)
			return time.Time{}, time.Time{}, ErrPayloadInvalid
		}

		e.emitter.Emit(ctx, PayloadInvalidEvent{
			Envelope: envelope,
			Err:      eth.NewPayloadErr(envelope.ExecutionPayload, status),
		})
		return time.Time{}, time.Time{}, ErrPayloadInvalid
	case eth.ExecutionValid:
		return insertStart, newPayloadDone, nil
	default:
		statusErr := eth.NewPayloadErr(envelope.ExecutionPayload, status)
		e.emitter.Emit(ctx, rollup.EngineTemporaryErrorEvent{Err: statusErr})
		return time.Time{}, time.Time{}, statusErr
	}
}

// ProcessPayload inserts a payload via NewPayload, updates unsafe head, and finalizes via FCU.
// Acquires e.mu. Combines processNewPayload and finalizePayload for the direct-call path.
// Emits UnsafeUpdateEvent on success. On error, emits appropriate error events before returning.
// Returns ErrStaleBuild without inserting when the payload no longer extends the unsafe head.
// A nil return means the payload was inserted (NewPayload valid) and the unsafe head updated;
// the concluding forkchoice update may still have failed transiently — it is logged and
// retried implicitly, since every subsequent build-start and engine update carries the full
// forkchoice state.
// Does NOT emit PayloadSuccessEvent.
func (e *EngineController) ProcessPayload(ctx context.Context, envelope *eth.ExecutionPayloadEnvelope, ref eth.L2BlockRef, buildStarted time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// The unsafe head may have moved (competing insert, block replacement) since
	// the payload was sealed; inserting it anyway would reorg the head backwards.
	if envelope.ExecutionPayload.ParentHash != e.unsafeHead.Hash {
		e.log.Warn("dropping stale sequencer payload, parent is not the unsafe head",
			"payload", ref, "parent", envelope.ExecutionPayload.ParentHash, "unsafe", e.unsafeHead)
		e.requestForkchoiceUpdate(ctx)
		return ErrStaleBuild
	}
	insertStarted, newPayloadDone, err := e.processNewPayload(ctx, envelope, ref, eth.L1BlockRef{})
	if err != nil {
		return err
	}
	// Priced separately because the two candidate publish points pay different
	// amounts: publishing once newPayload reports the block valid pays only
	// "newpayload", while publishing after the whole insert pays "total".
	e.metrics.RecordSequencerInsertTime("newpayload", newPayloadDone.Sub(insertStarted))
	e.finalizePayload(ctx, ref, false, eth.L1BlockRef{}, envelope, buildStarted, insertStarted)
	// "forkchoice" is everything after newPayload that a post-insert publish waits
	// on: the unsafe-head update and the concluding forkchoice call.
	e.metrics.RecordSequencerInsertTime("forkchoice", time.Since(newPayloadDone))
	e.metrics.RecordSequencerInsertTime("total", time.Since(insertStarted))
	return nil
}
