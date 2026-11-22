package postgres

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/audit"
	"github.com/truongpx396/nexus-agent/internal/store"
)

// Scoped is a Chain bound to a Store, for the callers that are not already
// inside a transaction: the scheduled anchor-and-verify pass and the control
// plane's receipt backend. Inside a transaction (the event log's append, a
// service's unit of work) the Chain is used directly, so the receipt commits
// with the event it covers.
type Scoped struct {
	St    *store.Store
	Chain *Chain

	// Marks remembers how far each tenant's last clean verification got, so the
	// scheduled pass verifies what is new and not the tenant's whole history every
	// time. Nil verifies everything on every pass, as before; the first pass after a
	// restart does so either way.
	Marks *Watermarks
}

// Watermarks is a Scoped's per-tenant record of verification progress. It is
// held in memory: a restart costs one full replay.
type Watermarks struct {
	mu sync.Mutex
	m  map[uuid.UUID]Watermark
}

// NewWatermarks returns an empty Watermarks.
func NewWatermarks() *Watermarks { return &Watermarks{m: map[uuid.UUID]Watermark{}} }

func (w *Watermarks) get(tenantID uuid.UUID) Watermark {
	if w == nil {
		return Watermark{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.m[tenantID]
}

func (w *Watermarks) set(tenantID uuid.UUID, mark Watermark) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.m[tenantID] = mark
}

// EmitReceipt extends the chain by one receipt in its own tenant-scoped
// transaction.
func (s Scoped) EmitReceipt(ctx context.Context, tenantID, sessionID uuid.UUID, seq int64, eventID uuid.UUID, eventType string, payloadDigest []byte) error {
	return s.St.InTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := s.Chain.Append(ctx, tx, tenantID, sessionID, seq, eventID, eventType, payloadDigest)
		return err
	})
}

// AnchorAndVerify anchors tenantID's new receipts and then verifies what has been
// written since the last clean pass (README task 5.3's scheduled verifier); the
// full replay is `nexusd verify-chain`. A break or a gap is logged, not
// returned: it is an alert for whatever pages the operator, not a reason to fail
// the pass that is the source of truth for the thing it is checking. The error
// is for a pass that could not run at all.
func (s Scoped) AnchorAndVerify(ctx context.Context, tenantID uuid.UUID) error {
	return s.St.InTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, _, err := s.Chain.Anchor(ctx, tx, tenantID); err != nil {
			return err
		}
		report, mark, err := s.Chain.VerifyFrom(ctx, tx, tenantID, s.Marks.get(tenantID))
		if err != nil {
			return err
		}
		logIfBroken(tenantID, report)
		s.Marks.set(tenantID, mark)
		return nil
	})
}

func logIfBroken(tenantID uuid.UUID, report audit.Report) {
	if !report.OK() {
		log.Error().Any("tenant_id", tenantID).Any("breaks", report.Breaks).Any("gaps", report.Gaps).Msg("audit: chain verification found a problem")
	}
}
