package cost

import "context"

// SessionID identifies the session a budget reservation, reconciliation, or
// metered record is scoped to.
type SessionID string

// ModelID identifies the model a reservation's estimated cost is priced
// against.
type ModelID string

// MeterID identifies a non-token metered resource (for example
// sandbox-seconds, stored bytes, or egress) reported through
// BudgetGate.Record.
type MeterID string

// ReservationID identifies a single pre-spend reservation returned by
// BudgetGate.Reserve and later settled by BudgetGate.Reconcile.
type ReservationID string

// Decision is the outcome taxonomy of a pre-spend gate resolution (FR-083,
// FR-188). It is string-valued so callers and tests can compare
// string(decision) against the literal values below.
type Decision string

const (
	// DecisionAllow grants the reservation as requested.
	DecisionAllow Decision = "allow"
	// DecisionRefuseCeiling refuses the reservation because granting it
	// would exceed a hard ceiling.
	DecisionRefuseCeiling Decision = "refuse_ceiling"
	// DecisionRefuseCredit refuses the reservation because available
	// credit is insufficient.
	DecisionRefuseCredit Decision = "refuse_credit"
	// DecisionDegrade grants the reservation at a degraded scope rather
	// than the full request.
	DecisionDegrade Decision = "degrade"
	// DecisionSkip records that the gate was bypassed for this call; a
	// skip is always a recorded decision, never a silent absence
	// (FR-188).
	DecisionSkip Decision = "skip"
)

// Reservation is the record produced by a BudgetGate.Reserve call.
type Reservation struct {
	// ReservationID identifies this reservation for a later Reconcile
	// call.
	ReservationID ReservationID
	// Granted reports whether the reservation was granted at all (true
	// for DecisionAllow and DecisionDegrade; false for a refusal).
	Granted bool
	// Decision is the resolved outcome taxonomy entry for this
	// reservation (FR-083, FR-188).
	Decision Decision
	// CounterEpoch is the atomic-counter generation this reservation was
	// granted against, so a later Reconcile can detect a counter reset
	// between reserve and reconcile. Populated by the real T026a
	// implementation; unused by this declaration-only task.
	CounterEpoch int64
}

// Usage is the token-class split of a turn's consumption, matching
// data-model.md's CostRecord shape (FR-014, SC-003). Carrying the split --
// rather than a single collapsed count -- is what makes the cache-read
// gate (>90% cache-read) measurable.
type Usage struct {
	// InputTokensUncached counts input tokens billed at the uncached
	// rate.
	InputTokensUncached int
	// InputTokensCacheRead counts input tokens served from cache.
	InputTokensCacheRead int
	// InputTokensCacheWrite counts input tokens billed for writing into
	// cache.
	InputTokensCacheWrite int
	// OutputTokens counts generated output tokens.
	OutputTokens int
}

// ReconcileUsage is the actual usage a BudgetGate.Reconcile call settles a
// reservation against.
type ReconcileUsage struct {
	// Usage is the actual token-class split observed for the reserved
	// call.
	Usage Usage
	// Unreported flags a reservation whose usage was never reported, so
	// Reconcile must settle it at the full reserved worst case, flagged
	// (FR-185).
	Unreported bool
}

// BudgetGate is the pre-spend reservation seam (FR-083): every model call
// reserves an estimated cost before it starts, then reconciles that
// reservation against actual usage once the call completes, with non-token
// meters reported separately through Record. This declaration is
// interface-only for T016a -- Reserve, Reconcile, and Record have no
// atomic-counter or storage behavior yet; the real implementation lands in
// a later phase (T026a).
type BudgetGate interface {
	// Reserve requests a pre-spend reservation for sessionID against
	// modelID's estimated input/output token cost for chunk chunkSeq, and
	// returns the resolved Reservation (FR-083).
	Reserve(ctx context.Context, sessionID SessionID, estInputTokens, reservedOutputTokens int, modelID ModelID, chunkSeq int) (Reservation, error)
	// Reconcile settles reservationID against actual, the actual usage
	// observed for the reserved call.
	Reconcile(ctx context.Context, reservationID ReservationID, actual ReconcileUsage) error
	// Record reports quantity -- an exact integer in meterID's own unit,
	// derived money is computed from it, never the reverse (FR-180) --
	// against resourceRef for the given window, for a non-token metered
	// resource.
	Record(ctx context.Context, meterID MeterID, quantity int64, resourceRef, window string) error
}
