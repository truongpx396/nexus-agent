package kernel

// SessionID identifies one run. A distinct named string type (not a bare
// string) so a SessionID can never be silently passed where a different
// kind of identifier is expected.
type SessionID string

// Seq is a monotonic per-session event sequence number.
type Seq int64

// CheckpointID identifies a durable resume record.
type CheckpointID string

// AutonomyLevel is the three-value ratchet: read_only < supervised < full.
// Numeric so "tighten" (T016a's RunControl.tightenAutonomy) can be checked
// as "new level's int value <= current level's int value" — never widened.
type AutonomyLevel int

const (
	AutonomyReadOnly AutonomyLevel = iota
	AutonomySupervised
	AutonomyFull
)

// TerminalReason is the closed, ten-value set every run ends under
// (kernel-abi.md's "loop terminal contract", FR-004). This is THE single
// place every terminal reason is enumerated — AllTerminalReasons below is
// what a caller ranges over to handle every case exhaustively. Adding a
// terminal state anywhere else in the codebase without adding it here is a
// contract defect.
type TerminalReason string

const (
	TerminalCompleted       TerminalReason = "completed"
	TerminalMaxTurns        TerminalReason = "max_turns"
	TerminalCostExhausted   TerminalReason = "cost_exhausted"
	TerminalCreditExhausted TerminalReason = "credit_exhausted"
	TerminalError           TerminalReason = "error"
	TerminalAborted         TerminalReason = "aborted"
	TerminalPromptTooLong   TerminalReason = "prompt_too_long"
	TerminalHookStopped     TerminalReason = "hook_stopped"
	TerminalApprovalExpired TerminalReason = "approval_expired"
	TerminalInputExpired    TerminalReason = "input_expired"
)

// AllTerminalReasons is the closed set, in the order declared above. Its
// length (10) and membership are asserted exactly by
// TestTerminalReasonEnumIsClosedAndComplete in kernel_abi_test.go — this
// value MUST also match the `terminal_reason` CHECK constraint in
// backend-go/migrations/0002_runtime.sql (already written; the 10 values
// there are in the same order) and the 10-value enum in
// specs/001-agent-platform/contracts/kernel-abi.md exactly (SC-020).
var AllTerminalReasons = []TerminalReason{
	TerminalCompleted,
	TerminalMaxTurns,
	TerminalCostExhausted,
	TerminalCreditExhausted,
	TerminalError,
	TerminalAborted,
	TerminalPromptTooLong,
	TerminalHookStopped,
	TerminalApprovalExpired,
	TerminalInputExpired,
}

// PrincipalRef identifies who/what is acting — a human operator, a
// service, or (in a later phase) another agent. Free-form for now
// ("operator:test" is a valid value per the frozen test) — a later task
// may narrow this to a struct; do not do that here, it would break the
// frozen test's `kernel.PrincipalRef("operator:test")` literal.
type PrincipalRef string

// ProjectedState is the pure, side-effect-free reconstruction RunControl.Replay
// returns (kernel-abi.md: "replay is PURE: it calls no model, executes no
// tool, emits no external effect, and appends nothing").
type ProjectedState struct {
	SessionID SessionID
	Seq       Seq
	Status    string
	// add other reasonable fields (e.g. TaintState) — none are exercised
	// by name in the current tests beyond the struct existing and being
	// returnable; keep it minimal.
}

// ForkOverrides declares what a fork run diverges from its source on
// (kernel-abi.md's fork operation: "declared overrides (agent version,
// prompt, tool catalog, model)").
type ForkOverrides struct {
	AgentVersion int
	ToolCatalog  []string
	ModelID      string
	// PromptOverride left as a documented extension point (string, empty
	// by default) — add it only if you judge it trivial; not exercised by
	// name in the current tests.
}
