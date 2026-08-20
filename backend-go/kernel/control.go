package kernel

import "context"

// CancelReason names why RunControl.Cancel was invoked. Free-form string
// type for now — "user_requested" is a valid value per the frozen test.
type CancelReason string

// RunControl is the lifecycle-operations seam (FR-005, FR-004). Every
// terminal reason must be reachable through a defined operation.
//
// tightenAutonomy is RATCHET ONLY — there is NO widening operation on this
// interface, and none may ever be added (a mid-run autonomy widening would
// be a direct prompt-injection lever, FR-111). This is enforced by a
// reflection-based test over this interface's method set
// (TestRunControlAndDelegationExposeNoWideningOperation) — do not add any
// method whose name could read as widen/loosen/broaden/expand/escalate/elevate.
type RunControl interface {
	// Steer delivers mid-run input to a RUNNING session's queue, drained at
	// a turn boundary. Not a new run submission.
	Steer(ctx context.Context, sessionID SessionID, message, idempotencyKey string) error

	// Cancel is the ONLY producer of the `aborted` terminal reason.
	Cancel(ctx context.Context, sessionID SessionID, reason CancelReason, drain bool) error

	// Resume continues the SAME run from its last checkpoint — never a restart.
	Resume(ctx context.Context, sessionID SessionID, fromCheckpointID CheckpointID) error

	// TightenAutonomy is one-way: it may only move toward MORE restrictive.
	TightenAutonomy(ctx context.Context, sessionID SessionID, level AutonomyLevel) error

	// Replay is PURE — no model call, no tool execution, no external
	// effect, appends nothing to the log.
	Replay(ctx context.Context, sessionID SessionID, toSeq Seq) (ProjectedState, error)

	// Fork creates a NEW run from an existing one at a given seq, with
	// declared overrides, and external effects disabled/confined. Returns
	// the new run's SessionID.
	Fork(ctx context.Context, sessionID SessionID, atSeq Seq, overrides ForkOverrides, actor PrincipalRef) (SessionID, error)
}
