// Package integration holds the trace<->event-log join test (T029f,
// FR-119, FR-120, SC-035), scoped as directed: the "run that suspended"
// and "run whose worker was killed mid-turn" scenarios are real,
// documented t.Skip subtests deferred to Phase 3's kernel loop.
//
// This test compiles against backend-go/internal/queue (T021's event-log
// store) and backend-go/internal/observability (T024c's trace-context
// propagation, extended here with a join helper), neither of which exists
// yet, and is expected to FAIL TO BUILD until those land. Proposed API:
//
//	package queue
//
//	// Event is the subset of the durable event-log row (FR-006) this join
//	// needs: every event carries the reciprocal trace_id/span_id of the
//	// turn-scoped span that covers it (kernel-abi.md "Telemetry" section).
//	type Event struct {
//		SessionID string
//		Seq       int64    // monotonic per session
//		TraceID   [16]byte // W3C trace id
//		SpanID    [8]byte  // W3C span id
//		Type      string
//		Timestamp time.Time
//	}
//
//	// EventLog is the append/read seam T021 implements against Postgres.
//	// NewMemoryEventLog backs tests (and a single-process dev topology)
//	// with an in-process implementation of the SAME interface.
//	type EventLog interface {
//		Append(ctx context.Context, e Event) (seq int64, err error)
//		Read(ctx context.Context, sessionID string, seq int64) (Event, error)
//		ReadRange(ctx context.Context, sessionID string, fromSeq, toSeq int64) ([]Event, error)
//	}
//	func NewMemoryEventLog() EventLog
//
//	package observability
//
//	// SpanEventRange is what ResolveSpan resolves a span_id to: the exact
//	// contiguous seq range of durable events that span covers (FR-119,
//	// FR-120 — "a run is not one long root span: ... each turn ... is its
//	// own trace ... carrying ... the covered seq range").
//	type SpanEventRange struct {
//		SessionID string
//		TraceID   [16]byte
//		SpanID    [8]byte
//		FromSeq   int64
//		ToSeq     int64
//	}
//
//	// EventTraceJoin resolves the bidirectional mapping between a span and
//	// the event-log seq range it covers, reading from the durable
//	// EventLog rather than in-process state — "emission is driven from
//	// durable events, so telemetry survives a worker kill" (FR-120).
//	type EventTraceJoin struct{ Log queue.EventLog }
//	func NewEventTraceJoin(log queue.EventLog) *EventTraceJoin
//	func (j *EventTraceJoin) ResolveSpan(ctx context.Context, sessionID string, spanID [8]byte) (SpanEventRange, error)
//	func (j *EventTraceJoin) ResolveEvent(ctx context.Context, sessionID string, seq int64) (traceID [16]byte, spanID [8]byte, err error)
package integration

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/truongpx396/nexus-agent/backend-go/internal/observability"
	"github.com/truongpx396/nexus-agent/backend-go/internal/queue"
)

func randomTraceID(t *testing.T) [16]byte {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatalf("crypto/rand.Read (trace id): %v", err)
	}
	return id
}

func randomSpanID(t *testing.T) [8]byte {
	t.Helper()
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatalf("crypto/rand.Read (span id): %v", err)
	}
	return id
}

// TestTraceEventJoin_SpanResolvesToExactContiguousSeqRange appends three
// back-to-back turns (three spans) under one trace and asserts the middle
// span's resolved range is exactly its own events — not its neighbors'.
func TestTraceEventJoin_SpanResolvesToExactContiguousSeqRange(t *testing.T) {
	ctx := context.Background()
	log := queue.NewMemoryEventLog()
	join := observability.NewEventTraceJoin(log)

	const sessionID = "sess-join-1"
	traceID := randomTraceID(t)

	spanA := randomSpanID(t)
	spanB := randomSpanID(t)
	spanC := randomSpanID(t)

	// spanA covers seq 1-2, spanB covers seq 3-7, spanC covers seq 8-9 —
	// three turns back to back in one trace, one span per turn (FR-119).
	plan := []struct {
		spanID [8]byte
		typ    string
	}{
		{spanA, "turn_started"},
		{spanA, "turn_ended"},
		{spanB, "tool_use"},
		{spanB, "tool_result"},
		{spanB, "content"},
		{spanB, "usage"},
		{spanB, "turn_ended"},
		{spanC, "turn_started"},
		{spanC, "turn_ended"},
	}

	for i, step := range plan {
		_, err := log.Append(ctx, queue.Event{
			SessionID: sessionID,
			Seq:       int64(i + 1),
			TraceID:   traceID,
			SpanID:    step.spanID,
			Type:      step.typ,
		})
		require.NoErrorf(t, err, "append synthetic event %d", i+1)
	}

	rng, err := join.ResolveSpan(ctx, sessionID, spanB)
	require.NoError(t, err)
	require.Equal(t, sessionID, rng.SessionID)
	require.Equal(t, traceID, rng.TraceID)
	require.Equal(t, spanB, rng.SpanID)
	require.Equal(t, int64(3), rng.FromSeq,
		"span B's range must start exactly where its own events start, not span A's")
	require.Equal(t, int64(7), rng.ToSeq,
		"span B's range must end exactly where its own events end, not span C's")
	require.Equal(t, int64(5), rng.ToSeq-rng.FromSeq+1,
		"the range must be contiguous: 5 events, no gaps, no overlap")
}

// TestTraceEventJoin_EventResolvesBackToTraceAndSpanLosslessly checks the
// reverse direction: from any seq inside a span's range, resolve back to
// its trace_id/span_id, and confirm re-resolving that span_id reproduces
// the identical range regardless of which seq the round trip started from.
func TestTraceEventJoin_EventResolvesBackToTraceAndSpanLosslessly(t *testing.T) {
	ctx := context.Background()
	log := queue.NewMemoryEventLog()
	join := observability.NewEventTraceJoin(log)

	const sessionID = "sess-join-2"
	traceID := randomTraceID(t)
	spanB := randomSpanID(t)

	seqs := []int64{3, 4, 5, 6, 7}
	for _, seq := range seqs {
		_, err := log.Append(ctx, queue.Event{
			SessionID: sessionID,
			Seq:       seq,
			TraceID:   traceID,
			SpanID:    spanB,
			Type:      "tool_use",
		})
		require.NoError(t, err)
	}

	for _, seq := range seqs {
		seq := seq
		t.Run(fmt.Sprintf("seq_%d", seq), func(t *testing.T) {
			gotTrace, gotSpan, err := join.ResolveEvent(ctx, sessionID, seq)
			require.NoError(t, err)
			require.Equal(t, traceID, gotTrace)
			require.Equal(t, spanB, gotSpan)

			// Round trip: resolving the span this event claims to belong
			// to must reproduce the SAME range regardless of which seq in
			// it we started from — that is what "resolves losslessly"
			// means.
			rng, err := join.ResolveSpan(ctx, sessionID, gotSpan)
			require.NoError(t, err)
			require.Equal(t, int64(3), rng.FromSeq,
				"round trip from seq %d must reproduce the SAME range every span in it shares", seq)
			require.Equal(t, int64(7), rng.ToSeq,
				"round trip from seq %d must reproduce the SAME range every span in it shares", seq)
		})
	}
}

// TestTraceEventJoin_KernelLoopScenarios covers the two scenarios T029f's
// task text names that require a running kernel loop, which does not exist
// until Phase 3. Both are written as real skipped subtests rather than
// silently omitted.
func TestTraceEventJoin_KernelLoopScenarios(t *testing.T) {
	t.Run("run_that_suspended", func(t *testing.T) {
		t.Skip("needs the kernel loop, Phase 3")
		// Once Phase 3's kernel loop exists, this will: start a run that
		// suspends durably on an approval mid-turn (FR-036), resume it
		// after a long gap, and assert the turn's span still exports
		// covering the exact seq range once the turn completes — proving
		// a six-hour approval suspension does not truncate or duplicate
		// the covered range, because "a span exports only when it ends"
		// (kernel-abi.md "Telemetry", FR-119, FR-120).
	})

	t.Run("run_whose_worker_was_killed_mid_turn", func(t *testing.T) {
		t.Skip("needs the kernel loop, Phase 3")
		// Once Phase 3's kernel loop exists, this will: kill the worker
		// process mid-turn after some events are durably appended but
		// before the turn's terminal event, then assert a replacement
		// worker's completion (or the reaper's synthetic termination)
		// still produces a complete turn-scoped span derived from the
		// log — because emission is driven from durable events, not
		// in-process state (FR-120), a killed worker must not produce a
		// partial or missing trace.
	})
}
