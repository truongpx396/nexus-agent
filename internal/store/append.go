package store

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/truongpx396/nexus-agent/internal/eventlog"
)

// Append inserts one event, computing its seq under a transaction-scoped
// advisory lock keyed by session_id — released automatically at the
// transaction's end, so there is no separate unlock path to forget. This is
// a correctness guard against a same-session concurrent writer; Phase 2 only
// ever runs one goroutine per session (no queue/worker pool yet), so nothing
// exercises the lock under real contention until Phase 6's session-key Redis
// lock (README task 6.2) is what actually makes concurrent workers safe —
// this is the transaction-local belt in the meantime.
//
// e.Seq and e.CreatedAt are ignored on input; the returned Event carries the
// values Postgres actually assigned, which is what kernel/loop.go yields to
// its caller and what internal/surfaces/rest forwards over SSE.
func Append(ctx context.Context, tx pgx.Tx, e eventlog.Event) (eventlog.Event, error) {
	if !e.Type.Known() {
		return eventlog.Event{}, fmt.Errorf("append event: %w", eventlog.UnknownTypeError{Type: e.Type})
	}
	k1, k2 := advisoryKey(e.SessionID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int4, $2::int4)`, k1, k2); err != nil {
		return eventlog.Event{}, fmt.Errorf("acquire session append lock: %w", err)
	}

	err := tx.QueryRow(ctx, `
		INSERT INTO events (
			event_id, session_id, tenant_id, seq, schema_version, type,
			payload, payload_digest, key_id, actor, tool_id, pair_ref,
			model_id, trace_id, span_id
		)
		SELECT $1, $2, $3, COALESCE(MAX(seq), 0) + 1, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		FROM events WHERE session_id = $2
		RETURNING seq, created_at`,
		e.EventID, e.SessionID, e.TenantID, e.SchemaVersion, e.Type,
		e.Payload, e.PayloadDigest, e.KeyID, e.Actor, e.ToolID, e.PairRef,
		e.ModelID, e.TraceID, e.SpanID,
	).Scan(&e.Seq, &e.CreatedAt)
	if err != nil {
		return eventlog.Event{}, fmt.Errorf("append event: %w", err)
	}
	return e, nil
}

// advisoryKey is the pair of 32-bit keys the session's append lock is taken on:
// two disjoint words of its random id, so 64 bits of it. The lock space is shared
// by every tenant, and a 32-bit hash of the id would make two unrelated sessions
// collide, and so wait for each other's transactions, about once in 65,000 pairs.
func advisoryKey(id uuid.UUID) (int32, int32) {
	return int32(binary.BigEndian.Uint32(id[0:4])), int32(binary.BigEndian.Uint32(id[8:12])) //nolint:gosec // reinterpreting the bits as the signed key Postgres takes is the point
}
