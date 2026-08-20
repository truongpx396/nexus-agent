package kernel

import (
	"context"

	"github.com/truongpx396/nexus-agent/backend-go/internal/cost"
	"github.com/truongpx396/nexus-agent/backend-go/internal/tools"
)

// ScopeRef selects a SUBSET of a parent session's live scope for a
// delegated child. This is the ONLY field on DelegationSpec that may carry
// tool/connector/egress/data-label/region information — see the frozen
// test TestDelegationSpecHasNoScopeWideningField, which asserts no OTHER
// field on DelegationSpec matches a widening-shaped name. There is
// structurally no way to WIDEN through this type: it selects a subset of
// what the parent already has, never grants anything new (FR-098).
type ScopeRef struct {
	Tools []string
	// add other reasonable subset-selector fields (e.g. Connectors []string,
	// MaxEgressDomains []string) if you judge them useful for a subset
	// selector — none beyond Tools are exercised by name in the current
	// tests. Do NOT add a field here that could plausibly GRANT something
	// (no "AdditionalTools", no "Region" override) — only ever a narrowing
	// selector.
}

// AcceptanceCriterion is what a delegation's returned summary is judged
// against before folding into the parent (FR-044, FR-100) — no
// self-declared success.
type AcceptanceCriterion struct {
	Description string
	// add fields as you judge reasonable; not exercised by name in current tests.
}

// DelegationSpec is the whole instruction handed to a sub-agent (FR-079).
//
// CRITICAL, test-enforced constraints on this type (do not violate either):
//  1. No field other than Scope may match a widening-shaped name (tool/
//     connector/egress/region/data_label) — TestDelegationSpecHasNoScopeWideningField.
//  2. No field, AT ANY NESTING DEPTH (including inside ScopeRef), may match
//     an identity-shaped name (tenant_id/user_id/org_id/account_id) —
//     TestDelegationSpecCarriesNoModelSuppliedIdentity recurses into nested
//     structs. Identity comes from ctx, never from a delegation spec field.
type DelegationSpec struct {
	Goal             string
	Context          string
	Scope            ScopeRef
	ReturnSchema     []byte // JSON Schema, raw bytes to avoid a schema-library dependency here
	Acceptance       AcceptanceCriterion
	MaxSummaryTokens int
	MaxIterations    int
	CeilingAmount    cost.Money
}

// Outcome is the closed set of results a delegation can resolve to.
type Outcome string

const (
	OutcomeAccepted       Outcome = "accepted"
	OutcomeRejectedSchema Outcome = "rejected_schema"
	OutcomeRejectedAccept Outcome = "rejected_acceptance"
	OutcomeBoundExceeded  Outcome = "bound_exceeded"
	OutcomeChildError     Outcome = "child_error"
	OutcomeReaped         Outcome = "reaped"
)

// DelegationResult is what a child returns to its parent. Summary is ALWAYS
// untrusted content (FR-087, FR-100) — the taint leg is never cleared just
// because a child "succeeded".
type DelegationResult struct {
	Summary      string
	TaintEngaged tools.TaintDeclaration
	Usage        cost.Usage
	Outcome      Outcome
}

// ReapReason is why a child was reaped without a normal return.
type ReapReason string

const (
	ReapParentTerminal   ReapReason = "parent_terminal"
	ReapParentCancelled  ReapReason = "parent_cancelled"
	ReapCeilingExhausted ReapReason = "ceiling_exhausted"
)

// Delegation is the sub-agent seam (FR-079, FR-098-FR-101). A sub-agent is
// a second locus of execution holding real credentials and spending real
// money, so delegation is a tool invocation through the same pipeline, not
// a side channel around it.
type Delegation interface {
	// Delegate's scope MUST be provably a subset of the parent's at call time.
	Delegate(ctx context.Context, spec DelegationSpec) (DelegationResult, error)
	// Reap tears down children on parent terminal/cancel/ceiling breach.
	Reap(ctx context.Context, parentSessionID SessionID, reason ReapReason) error
}
