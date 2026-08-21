package observability

import (
	"context"
	"fmt"
	"math"

	"github.com/truongpx396/nexus-agent/backend-go/internal/queue"
)

// SpanEventRange is what ResolveSpan resolves a span_id to: the exact
// contiguous seq range of durable events that span covers (FR-119, FR-120
// -- "a run is not one long root span: ... each turn ... is its own trace
// ... carrying ... the covered seq range").
type SpanEventRange struct {
	SessionID string
	TraceID   [16]byte
	SpanID    [8]byte
	FromSeq   int64
	ToSeq     int64
}

// EventTraceJoin resolves the bidirectional mapping between a span and the
// event-log seq range it covers, reading from the durable EventLog rather
// than in-process state -- "emission is driven from durable events, so
// telemetry survives a worker kill" (FR-120).
type EventTraceJoin struct {
	Log queue.EventLog
}

// NewEventTraceJoin constructs an EventTraceJoin reading from log.
func NewEventTraceJoin(log queue.EventLog) *EventTraceJoin {
	return &EventTraceJoin{Log: log}
}

// allEvents fetches every event for sessionID from the durable log. Task
// 11's EventLog interface (already committed, frozen) has no "list all"
// method -- deliberately, since the real Postgres-backed EventLog scans by
// range, not a full-session dump. This uses ReadRange across the full
// int64 domain, which the in-memory implementation (and any correct
// range-scanning implementation) satisfies exactly: every event whose Seq
// falls in [math.MinInt64, math.MaxInt64] is every event that exists.
func (j *EventTraceJoin) allEvents(ctx context.Context, sessionID string) ([]queue.Event, error) {
	return j.Log.ReadRange(ctx, sessionID, math.MinInt64, math.MaxInt64)
}

// ResolveSpan finds every event in sessionID whose SpanID matches spanID,
// and returns the exact contiguous [min seq, max seq] range they span,
// plus the shared TraceID. Returns an error if no event matches.
func (j *EventTraceJoin) ResolveSpan(ctx context.Context, sessionID string, spanID [8]byte) (SpanEventRange, error) {
	events, err := j.allEvents(ctx, sessionID)
	if err != nil {
		return SpanEventRange{}, fmt.Errorf("queue trace join: reading session %q: %w", sessionID, err)
	}

	rng := SpanEventRange{SessionID: sessionID, SpanID: spanID}
	found := false
	for _, e := range events {
		if e.SpanID != spanID {
			continue
		}
		if !found {
			rng.TraceID = e.TraceID
			rng.FromSeq = e.Seq
			rng.ToSeq = e.Seq
			found = true
			continue
		}
		if e.Seq < rng.FromSeq {
			rng.FromSeq = e.Seq
		}
		if e.Seq > rng.ToSeq {
			rng.ToSeq = e.Seq
		}
	}
	if !found {
		return SpanEventRange{}, fmt.Errorf("queue trace join: no events found for session %q span %x", sessionID, spanID)
	}
	return rng, nil
}

// ResolveEvent reads the single event at sessionID/seq and returns its
// trace_id/span_id directly.
func (j *EventTraceJoin) ResolveEvent(ctx context.Context, sessionID string, seq int64) (traceID [16]byte, spanID [8]byte, err error) {
	e, err := j.Log.Read(ctx, sessionID, seq)
	if err != nil {
		return [16]byte{}, [8]byte{}, fmt.Errorf("queue trace join: resolving event: %w", err)
	}
	return e.TraceID, e.SpanID, nil
}
