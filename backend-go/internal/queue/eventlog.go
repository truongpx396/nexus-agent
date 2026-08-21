// Package queue holds the durable event-log store (T021) and the
// versioned control-plane <-> data-plane handshake (T026). This file
// implements the event-log half: an in-process EventLog fixture backing
// this batch's tests (T021's real Postgres-backed implementation, with
// schema_version/digest/key_id and upcast-on-read, is a later task).
package queue

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Event is the subset of the durable event-log row (FR-006) the trace/event
// join needs: every event carries the reciprocal trace_id/span_id of the
// turn-scoped span that covers it (kernel-abi.md "Telemetry" section).
type Event struct {
	SessionID string
	Seq       int64    // monotonic per session
	TraceID   [16]byte // W3C trace id
	SpanID    [8]byte  // W3C span id
	Type      string
	Timestamp time.Time
}

// EventLog is the append/read seam T021 implements against Postgres in a
// later task. NewMemoryEventLog backs THIS batch's tests (and a
// single-process dev topology) with an in-process implementation of the
// SAME interface -- this task only builds the in-memory implementation.
type EventLog interface {
	Append(ctx context.Context, e Event) (seq int64, err error)
	Read(ctx context.Context, sessionID string, seq int64) (Event, error)
	ReadRange(ctx context.Context, sessionID string, fromSeq, toSeq int64) ([]Event, error)
}

// memoryEventLog is an in-process EventLog, keyed by session, each
// session's events kept in append order.
type memoryEventLog struct {
	mu     sync.Mutex
	bySess map[string][]Event
}

// NewMemoryEventLog constructs an in-process EventLog.
func NewMemoryEventLog() EventLog {
	return &memoryEventLog{bySess: make(map[string][]Event)}
}

func (l *memoryEventLog) Append(ctx context.Context, e Event) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bySess[e.SessionID] = append(l.bySess[e.SessionID], e)
	return e.Seq, nil
}

func (l *memoryEventLog) Read(ctx context.Context, sessionID string, seq int64) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.bySess[sessionID] {
		if e.Seq == seq {
			return e, nil
		}
	}
	return Event{}, fmt.Errorf("queue: no event at session %q seq %d", sessionID, seq)
}

func (l *memoryEventLog) ReadRange(ctx context.Context, sessionID string, fromSeq, toSeq int64) ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Event
	for _, e := range l.bySess[sessionID] {
		if e.Seq >= fromSeq && e.Seq <= toSeq {
			out = append(out, e)
		}
	}
	return out, nil
}
