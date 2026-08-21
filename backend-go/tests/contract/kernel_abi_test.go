// This file covers T027 — the kernel ABI interfaces declared in
// contracts/kernel-abi.md each compile with at least one stub
// implementation, asserted through Go's type system (`var _ Iface =
// (*stub)(nil)`), plus one runtime test per interface where a runtime
// behavior is meaningful to pin (the fake Provider's scripted stream, the
// stub Tool's fail-closed default taint, and so on).
package contract

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/truongpx396/nexus-agent/backend-go/internal/cost"
	"github.com/truongpx396/nexus-agent/backend-go/internal/provider"
	"github.com/truongpx396/nexus-agent/backend-go/internal/provider/fake"
	"github.com/truongpx396/nexus-agent/backend-go/internal/tools"
	"github.com/truongpx396/nexus-agent/backend-go/kernel"
)

// ---------------------------------------------------------------------
// Provider (FR-027) — backend-go/internal/provider, backend-go/internal/provider/fake
// ---------------------------------------------------------------------

// The deterministic fake Provider (T015a) must satisfy the swappable
// Provider interface (T015) so the correctness suite never bills a live
// model (FR-097).
var _ provider.Provider = (*fake.Provider)(nil)

// TestFakeProviderStreamsScriptedChunks drives the fake Provider through a
// short scripted turn and asserts the stream terminates on a `done` chunk
// with the scripted reason — the minimum runtime behavior a
// recorded/deterministic provider must exhibit to back the correctness
// suite reproducibly.
func TestFakeProviderStreamsScriptedChunks(t *testing.T) {
	ctx := context.Background()
	script := []provider.Chunk{
		{Kind: provider.ChunkContent, Text: "hello from the fake provider"},
		{Kind: provider.ChunkDone, DoneReason: provider.DoneStop},
	}

	p := fake.New(script...)

	ch, err := p.Stream(ctx, provider.Prompt{}, nil)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	var got []provider.Chunk
	for c := range ch {
		got = append(got, c)
	}

	if len(got) != len(script) {
		t.Fatalf("Stream() emitted %d chunks, want %d", len(got), len(script))
	}

	last := got[len(got)-1]
	if last.Kind != provider.ChunkDone {
		t.Fatalf("last chunk Kind = %q, want %q", last.Kind, provider.ChunkDone)
	}
	if last.DoneReason != provider.DoneStop {
		t.Fatalf("last chunk DoneReason = %q, want %q", last.DoneReason, provider.DoneStop)
	}
}

// ---------------------------------------------------------------------
// Tool (FR-007, FR-008, FR-009, FR-011, FR-087) — backend-go/internal/tools
// ---------------------------------------------------------------------

// stubTool is a minimal Tool implementation used only to prove the
// interface compiles and that its TaintDeclaration defaults fail closed.
type stubTool struct{}

func (stubTool) ID() tools.ToolRef {
	return tools.ToolRef{Namespace: "contract", Name: "stub", Version: "v1"}
}

func (stubTool) Description() string {
	return "a minimal stub tool for kernel ABI contract tests"
}

func (stubTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (stubTool) OutputSchema() json.RawMessage { return nil }

func (stubTool) DescriptorDigest() []byte { return []byte("stub-descriptor-digest") }

func (stubTool) Disclosure() tools.Disclosure { return tools.DisclosureResident }

// Taint returns the fail-closed default: every leg engaged, because a
// missing or unclassifiable declaration MUST assume all three taint legs
// (FR-087).
func (stubTool) Taint() tools.TaintDeclaration { return tools.DefaultTaintDeclaration() }

func (stubTool) EffectClass() tools.EffectClass { return tools.EffectClassOther }

func (stubTool) IsConcurrencySafe(input json.RawMessage) bool { return false }

func (stubTool) CheckPermissions(ctx context.Context, input json.RawMessage) (tools.PermissionResult, error) {
	return tools.PermissionResult{Allowed: false}, nil
}

func (stubTool) ValidateInput(ctx context.Context, input json.RawMessage) (tools.ValidationResult, error) {
	return tools.ValidationResult{Valid: false}, nil
}

func (stubTool) Call(ctx context.Context, input json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{}, nil
}

var _ tools.Tool = (*stubTool)(nil)

// TestStubToolDefaultTaintIsFailClosed asserts both the documented default
// constructor and a stub Tool's Taint() read back as {true, true, true} —
// FR-087's fail-closed default is a claim about runtime values, not just
// field existence, and Go's bool zero value (false) would silently violate
// it if the interface relied on a zero-valued struct instead of an
// explicit constructor.
func TestStubToolDefaultTaintIsFailClosed(t *testing.T) {
	var tool tools.Tool = stubTool{}
	taint := tool.Taint()

	tests := []struct {
		name string
		got  bool
	}{
		{"returns_untrusted", taint.ReturnsUntrusted},
		{"reads_private_data", taint.ReadsPrivateData},
		{"mutates_external", taint.MutatesExternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.got {
				t.Fatalf("TaintDeclaration.%s = false, want true (FR-087: all three taint legs assumed engaged by default)", tt.name)
			}
		})
	}

	if got := tools.DefaultTaintDeclaration(); got != (tools.TaintDeclaration{ReturnsUntrusted: true, ReadsPrivateData: true, MutatesExternal: true}) {
		t.Fatalf("DefaultTaintDeclaration() = %+v, want all three fields true", got)
	}
}

// ---------------------------------------------------------------------
// RunControl (FR-005, FR-004) — backend-go/kernel
// ---------------------------------------------------------------------

type stubRunControl struct{}

func (stubRunControl) Steer(ctx context.Context, sessionID kernel.SessionID, message, idempotencyKey string) error {
	return nil
}

func (stubRunControl) Cancel(ctx context.Context, sessionID kernel.SessionID, reason kernel.CancelReason, drain bool) error {
	return nil
}

func (stubRunControl) Resume(ctx context.Context, sessionID kernel.SessionID, fromCheckpointID kernel.CheckpointID) error {
	return nil
}

func (stubRunControl) TightenAutonomy(ctx context.Context, sessionID kernel.SessionID, level kernel.AutonomyLevel) error {
	return nil
}

func (stubRunControl) Replay(ctx context.Context, sessionID kernel.SessionID, toSeq kernel.Seq) (kernel.ProjectedState, error) {
	return kernel.ProjectedState{}, nil
}

func (stubRunControl) Fork(ctx context.Context, sessionID kernel.SessionID, atSeq kernel.Seq, overrides kernel.ForkOverrides, actor kernel.PrincipalRef) (kernel.SessionID, error) {
	return "", nil
}

var _ kernel.RunControl = (*stubRunControl)(nil)

// TestRunControlStubSatisfiesInterface exercises every RunControl
// operation once at runtime — steer, cancel, resume, tightenAutonomy, the
// pure replay, and fork — so a signature mismatch a compiler could paper
// over with an accidental match still surfaces as a real call.
func TestRunControlStubSatisfiesInterface(t *testing.T) {
	var rc kernel.RunControl = stubRunControl{}
	ctx := context.Background()
	sessionID := kernel.SessionID("sess-1")

	if err := rc.Steer(ctx, sessionID, "steering message", "idem-1"); err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	if err := rc.Cancel(ctx, sessionID, kernel.CancelReason("user_requested"), true); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := rc.Resume(ctx, sessionID, kernel.CheckpointID("")); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := rc.TightenAutonomy(ctx, sessionID, kernel.AutonomyLevel(1)); err != nil {
		t.Fatalf("TightenAutonomy() error = %v", err)
	}
	if _, err := rc.Replay(ctx, sessionID, kernel.Seq(0)); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if _, err := rc.Fork(ctx, sessionID, kernel.Seq(0), kernel.ForkOverrides{}, kernel.PrincipalRef("operator:test")); err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
}

// TestTerminalReasonEnumIsClosedAndComplete asserts kernel.AllTerminalReasons
// — the single place every terminal reason must be enumerated — matches
// the ten-value closed set from kernel-abi.md's loop terminal contract
// exactly. A terminal state no caller can produce, or one silently added
// outside this registry, is a contract defect.
func TestTerminalReasonEnumIsClosedAndComplete(t *testing.T) {
	want := map[kernel.TerminalReason]bool{
		kernel.TerminalCompleted:       true,
		kernel.TerminalMaxTurns:        true,
		kernel.TerminalCostExhausted:   true,
		kernel.TerminalCreditExhausted: true,
		kernel.TerminalError:           true,
		kernel.TerminalAborted:         true,
		kernel.TerminalPromptTooLong:   true,
		kernel.TerminalHookStopped:     true,
		kernel.TerminalApprovalExpired: true,
		kernel.TerminalInputExpired:    true,
	}

	got := kernel.AllTerminalReasons
	if len(got) != len(want) {
		t.Fatalf("kernel.AllTerminalReasons has %d entries, want %d: %v", len(got), len(want), got)
	}

	seen := make(map[kernel.TerminalReason]bool, len(got))
	for _, r := range got {
		if !want[r] {
			t.Errorf("kernel.AllTerminalReasons contains unexpected reason %q", r)
		}
		if seen[r] {
			t.Errorf("kernel.AllTerminalReasons contains duplicate reason %q", r)
		}
		seen[r] = true
	}
	for r := range want {
		if !seen[r] {
			t.Errorf("kernel.AllTerminalReasons is missing reason %q", r)
		}
	}
}

// ---------------------------------------------------------------------
// BudgetGate (FR-083) — backend-go/internal/cost
// ---------------------------------------------------------------------

type stubBudgetGate struct{}

func (stubBudgetGate) Reserve(ctx context.Context, sessionID cost.SessionID, estInputTokens, reservedOutputTokens int, modelID cost.ModelID, chunkSeq int) (cost.Reservation, error) {
	return cost.Reservation{ReservationID: "resv-1", Granted: true, Decision: cost.DecisionAllow}, nil
}

func (stubBudgetGate) Reconcile(ctx context.Context, reservationID cost.ReservationID, actual cost.ReconcileUsage) error {
	return nil
}

// Record's quantity is an int64, never a float — FR-180 (control-data-plane.md
// "ReportUsage(v1)"): "quantity is an EXACT INTEGER in the meter's unit; money
// is derived from it, never the reverse." A float64 here would let a
// non-token meter (sandbox-seconds, stored bytes, egress) drift the same way
// T013a's Money guard exists to prevent for cost amounts — except this
// parameter's name wouldn't match that guard's regex, so it must be typed
// correctly at the interface instead of caught later.
func (stubBudgetGate) Record(ctx context.Context, meterID cost.MeterID, quantity int64, resourceRef, window string) error {
	return nil
}

var _ cost.BudgetGate = (*stubBudgetGate)(nil)

// TestBudgetGateDecisionTaxonomy pins the five named resolutions
// reserve/reconcile must be able to produce (FR-083, FR-188): allow,
// refuse_ceiling, refuse_credit, degrade, skip. A skip is a recorded
// decision, never an absence — dropping it from this taxonomy is exactly
// the defect FR-188 calls out.
func TestBudgetGateDecisionTaxonomy(t *testing.T) {
	tests := []struct {
		name string
		got  cost.Decision
		want string
	}{
		{"allow", cost.DecisionAllow, "allow"},
		{"refuse_ceiling", cost.DecisionRefuseCeiling, "refuse_ceiling"},
		{"refuse_credit", cost.DecisionRefuseCredit, "refuse_credit"},
		{"degrade", cost.DecisionDegrade, "degrade"},
		{"skip", cost.DecisionSkip, "skip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.got) != tt.want {
				t.Errorf("cost.Decision%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestBudgetGateStubReservesAndReconciles exercises all three BudgetGate
// operations at runtime: a pre-spend reservation, its reconciliation
// against actual usage, and a non-token meter record.
func TestBudgetGateStubReservesAndReconciles(t *testing.T) {
	var gate cost.BudgetGate = stubBudgetGate{}
	ctx := context.Background()

	res, err := gate.Reserve(ctx, "sess-1", 100, 200, "model-x", 0)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if !res.Granted {
		t.Fatalf("Reserve() Granted = false, want true for the stub happy path")
	}

	reconcileErr := gate.Reconcile(ctx, res.ReservationID, cost.ReconcileUsage{Usage: cost.Usage{OutputTokens: 42}})
	if reconcileErr != nil {
		t.Fatalf("Reconcile() error = %v", reconcileErr)
	}

	if recordErr := gate.Record(ctx, "sandbox_seconds", 12, "sandbox-123", "2026-08-17"); recordErr != nil {
		t.Fatalf("Record() error = %v", recordErr)
	}
}

// ---------------------------------------------------------------------
// Delegation (FR-079, FR-098-FR-101) — backend-go/kernel
// ---------------------------------------------------------------------

type stubDelegation struct{}

func (stubDelegation) Delegate(ctx context.Context, spec kernel.DelegationSpec) (kernel.DelegationResult, error) {
	return kernel.DelegationResult{Outcome: kernel.OutcomeAccepted}, nil
}

func (stubDelegation) Reap(ctx context.Context, parentSessionID kernel.SessionID, reason kernel.ReapReason) error {
	return nil
}

var _ kernel.Delegation = (*stubDelegation)(nil)

// TestDelegationStubDelegates exercises Delegate at runtime with a minimal
// spec and asserts the paired-result outcome comes back typed.
func TestDelegationStubDelegates(t *testing.T) {
	var d kernel.Delegation = stubDelegation{}

	result, err := d.Delegate(context.Background(), kernel.DelegationSpec{
		Goal:  "summarize the ticket",
		Scope: kernel.ScopeRef{Tools: []string{"read_ticket"}},
	})
	if err != nil {
		t.Fatalf("Delegate() error = %v", err)
	}
	if result.Outcome != kernel.OutcomeAccepted {
		t.Fatalf("Delegate() Outcome = %q, want %q", result.Outcome, kernel.OutcomeAccepted)
	}
}

// TestDelegationReapCompiles exercises Reap at runtime — the parent-side
// operation that must run on cancel/terminal/ceiling breach (FR-100).
func TestDelegationReapCompiles(t *testing.T) {
	var d kernel.Delegation = stubDelegation{}

	if err := d.Reap(context.Background(), kernel.SessionID("parent-1"), kernel.ReapParentTerminal); err != nil {
		t.Fatalf("Reap() error = %v", err)
	}
}

// wideningVerb flags a method name that reads as widening rather than
// tightening/narrowing an autonomy or scope boundary.
var wideningVerb = regexp.MustCompile(`(?i)widen|loosen|broaden|expand|escalate|elevate`)

// TestRunControlAndDelegationExposeNoWideningOperation asserts, by
// reflecting over the interface method sets, that neither RunControl nor
// Delegation exposes any operation that could widen autonomy or scope.
// tightenAutonomy is ratchet-only (FR-111): there is no widening operation
// on RunControl, and none on Delegation either, because a mid-run
// autonomy widening is a direct prompt-injection lever.
func TestRunControlAndDelegationExposeNoWideningOperation(t *testing.T) {
	ifaces := []struct {
		name string
		typ  reflect.Type
	}{
		{"RunControl", reflect.TypeOf((*kernel.RunControl)(nil)).Elem()},
		{"Delegation", reflect.TypeOf((*kernel.Delegation)(nil)).Elem()},
	}

	for _, iface := range ifaces {
		t.Run(iface.name, func(t *testing.T) {
			for i := 0; i < iface.typ.NumMethod(); i++ {
				name := iface.typ.Method(i).Name
				if wideningVerb.MatchString(name) {
					t.Errorf("%s.%s looks like a widening operation; only tightenAutonomy exists, and it is ratchet-only (FR-111)", iface.name, name)
				}
			}
		})
	}
}

// scopeWideningField flags a DelegationSpec field name that could carry a
// tools/connectors/egress/data-label/region parameter outside the single
// Scope subset selector.
var scopeWideningField = regexp.MustCompile(`(?i)tool|connector|egress|region|datalabel|data_label`)

// identityField flags a DelegationSpec field name that could carry a
// tenant/user identifier supplied by the spec itself rather than sourced
// from context.
var identityField = regexp.MustCompile(`(?i)tenant.?id|user.?id|org.?id|account.?id`)

// TestDelegationSpecHasNoScopeWideningField asserts DelegationSpec has no
// model-facing field, other than the single Scope subset selector, that
// could widen tools, connectors, egress, data label, or region beyond the
// parent's live scope (FR-098, MA2). Scope itself is exempt: it is how a
// subset is expressed, not a widening lever.
func TestDelegationSpecHasNoScopeWideningField(t *testing.T) {
	typ := reflect.TypeOf(kernel.DelegationSpec{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "Scope" {
			continue
		}
		if scopeWideningField.MatchString(field.Name) {
			t.Errorf("DelegationSpec.%s looks like a model-facing parameter that could widen tools/connectors/egress/data-label/region beyond the parent's scope (FR-098); only Scope, a subset selector, may carry this", field.Name)
		}
	}
}

// TestDelegationSpecCarriesNoModelSuppliedIdentity asserts DelegationSpec
// has no tenant/user identity field. Identity must be sourced from
// context, never a bare model-output or spec field (ID2) — a
// model-supplied tenant id on a delegation spec would be a tenancy bypass
// with extra steps.
func TestDelegationSpecCarriesNoModelSuppliedIdentity(t *testing.T) {
	// Recurse into every nested field (Scope's own ScopeRef included) using
	// the same collectFieldPaths helper control_data_plane_test.go defines
	// in this package — a top-level-only scan would miss a TenantID hiding
	// inside ScopeRef, which is exactly the property FR-098/ID2 exists to
	// pin: identity must never ride along on ANY part of a delegation spec.
	fields := map[string]string{}
	collectFieldPaths(reflect.TypeOf(kernel.DelegationSpec{}), "", fields)

	for path := range fields {
		leaf := path
		if i := strings.LastIndex(path, "."); i >= 0 {
			leaf = path[i+1:]
		}
		if identityField.MatchString(leaf) {
			t.Errorf("DelegationSpec.%s looks like a tenant/user identity field; identity must be sourced from context, never a model-facing spec field, at any nesting depth (ID2)", path)
		}
	}
}
