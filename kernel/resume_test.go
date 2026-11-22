package kernel_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/provider"
	"github.com/truongpx396/nexus-agent/internal/provider/fake"
	"github.com/truongpx396/nexus-agent/kernel"
)

// A resolution the kernel does not recognise must stop the run. Reading it as
// "no result" would append an empty, successful tool_result and let the loop
// carry on past an execution nobody approved.

func TestResume_AnUnknownApprovalDecisionStopsTheRunAndRecordsNoResult(t *testing.T) {
	r := newRig(toolFunc(func(kernel.ToolUseRequest) kernel.ToolResult {
		t.Error("the tool ran for a decision the kernel does not recognise")
		return kernel.ToolResult{}
	}), finalTurn)
	cfg := kernel.RunConfig{System: "test", ModelID: "fake", MaxTurns: 5, AutonomyLevel: "supervised"}

	var gotErr error
	for _, err := range r.k.Resume(context.Background(), r.st, cfg, kernel.PendingResolution{
		ToolID: "platform/echo@v1", ToolUseEventID: uuid.New(), Decision: kernel.ApprovalDecisionKind("maybe"),
	}) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil || !strings.Contains(gotErr.Error(), "unknown approval decision") {
		t.Fatalf("Resume with an unknown decision = %v, want an unknown-decision error", gotErr)
	}
	if n := len(ofType(r.log.Events(r.sessionID), eventlog.EventToolResult)); n != 0 {
		t.Fatalf("an unknown decision recorded %d tool_result events, want none", n)
	}
}

func TestResumeDelegation_AnUnknownOutcomeStopsTheRunAndRecordsNoResult(t *testing.T) {
	r := newRig(toolFunc(func(kernel.ToolUseRequest) kernel.ToolResult { return kernel.ToolResult{} }), finalTurn)
	cfg := kernel.RunConfig{System: "test", ModelID: "fake", MaxTurns: 5, AutonomyLevel: "supervised"}

	var gotErr error
	for _, err := range r.k.ResumeDelegation(context.Background(), r.st, cfg, kernel.DelegationResolution{
		ToolID: "platform/delegate@v1", ToolUseEventID: uuid.New(), Outcome: kernel.DelegationOutcomeKind("lost"),
	}) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil || !strings.Contains(gotErr.Error(), "unknown outcome") {
		t.Fatalf("ResumeDelegation with an unknown outcome = %v, want an unknown-outcome error", gotErr)
	}
	if n := len(ofType(r.log.Events(r.sessionID), eventlog.EventToolResult)); n != 0 {
		t.Fatalf("an unknown outcome recorded %d tool_result events, want none", n)
	}
}

// promptRecorder remembers every prompt it is sent and hands it on to a
// scripted provider, which refuses a prompt a hosted API would refuse.
type promptRecorder struct {
	inner   provider.Provider
	prompts []provider.Prompt
}

func (p *promptRecorder) Stream(ctx context.Context, pr provider.Prompt, tools []provider.ToolSchema, rc provider.RunContext) (provider.Stream, error) {
	p.prompts = append(p.prompts, pr)
	return p.inner.Stream(ctx, pr, tools, rc)
}

// suspendingExecutor holds the first call for approval or delegation, and
// approves it when asked to.
type suspendingExecutor struct{ result kernel.ToolResult }

func (e suspendingExecutor) Execute(context.Context, kernel.ToolUseRequest, kernel.ExecContext) kernel.ToolResult {
	return e.result
}

func (suspendingExecutor) ExecuteApproved(context.Context, kernel.ToolUseRequest, []byte, kernel.ExecContext) kernel.ToolResult {
	return kernel.ToolResult{Output: json.RawMessage(`{"echo":"hi"}`)}
}

// rehydrated rebuilds a run state from what a suspended run left in the log,
// the way oversight and delegate do before they resume it.
func rehydrated(t *testing.T, r *rig) *kernel.RunState {
	t.Helper()
	history := r.log.Events(r.sessionID)
	transcript, ids, err := kernel.Rehydrate(context.Background(), history, func(_ context.Context, e eventlog.Event) ([]byte, error) { return e.Payload, nil })
	if err != nil {
		t.Fatal(err)
	}
	return &kernel.RunState{TenantID: r.tenantID, SessionID: r.sessionID, Seal: seal, History: history, Transcript: transcript, ToolUseIDs: ids}
}

// toolResultsFor counts the tool_result blocks a prompt carries for one call.
func toolResultsFor(p provider.Prompt, toolUseID string) (texts []string) {
	for _, m := range p.Messages {
		for _, b := range m.Blocks {
			if b.Kind == provider.BlockToolResult && b.ToolUseID == toolUseID {
				texts = append(texts, b.Text)
			}
		}
	}
	return texts
}

func terminalReasonOf(t *testing.T, events []eventlog.Event) string {
	t.Helper()
	terminals := ofType(events, eventlog.EventTerminal)
	if len(terminals) == 0 {
		return ""
	}
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(terminals[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Reason
}

// A suspension pairs its tool_use with a placeholder result so the log is
// never unpaired, and the real result follows on resume. The model must be
// shown one of them: two tool_results for one tool_use is a 400 from a hosted
// API, and the scripted provider now refuses it the same way.
func TestResume_AnApprovedCallLeavesExactlyOneResultInThePrompt(t *testing.T) {
	r := newRig(suspendingExecutor{result: kernel.ToolResult{IsError: true, Reason: "approval required", AwaitingApproval: true, AskKind: "once", CanonicalDigest: []byte("d")}}, toolTurn, finalTurn)
	rec := &promptRecorder{inner: fake.New(toolTurn, finalTurn)}
	r.k.Provider = rec

	events, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ofType(r.log.Events(r.sessionID), eventlog.EventTerminal)); got != 0 {
		t.Fatalf("a suspended run wrote %d terminal events, want none", got)
	}
	useID := ofType(events, eventlog.EventToolUse)[0].EventID

	var resumed []eventlog.Event
	for ev, rerr := range r.k.Resume(context.Background(), rehydrated(t, r),
		kernel.RunConfig{System: "test", ModelID: "fake", MaxTurns: 5, AutonomyLevel: "supervised"},
		kernel.PendingResolution{ToolUseEventID: useID, ToolID: "platform/echo@v1", Input: json.RawMessage(`{"msg":"hi"}`), ApprovedDigest: []byte("d"), Decision: kernel.ApprovalDecisionGranted}) {
		if rerr != nil {
			t.Fatal(rerr)
		}
		resumed = append(resumed, ev)
	}

	if got := terminalReasonOf(t, resumed); got != "completed" {
		t.Fatalf("resumed run ended %q, want completed (a malformed prompt ends it as an error)", got)
	}
	last := rec.prompts[len(rec.prompts)-1]
	if got := toolResultsFor(last, "tu1"); len(got) != 1 || got[0] != `{"echo":"hi"}` {
		t.Fatalf("the post-approval prompt answers tu1 with %q, want the real result once", got)
	}
}

func TestResumeDelegation_AReturnedChildLeavesExactlyOneResultInThePrompt(t *testing.T) {
	r := newRig(suspendingExecutor{result: kernel.ToolResult{IsError: true, Reason: "delegating", AwaitingDelegation: true, ChildSessionID: uuid.New()}}, toolTurn, finalTurn)
	rec := &promptRecorder{inner: fake.New(toolTurn, finalTurn)}
	r.k.Provider = rec

	events, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	useID := ofType(events, eventlog.EventToolUse)[0].EventID

	var resumed []eventlog.Event
	for ev, rerr := range r.k.ResumeDelegation(context.Background(), rehydrated(t, r),
		kernel.RunConfig{System: "test", ModelID: "fake", MaxTurns: 5, AutonomyLevel: "supervised"},
		kernel.DelegationResolution{ToolUseEventID: useID, ToolID: "platform/delegate@v1", Outcome: kernel.DelegationReturned, Result: json.RawMessage(`{"output":"child"}`)}) {
		if rerr != nil {
			t.Fatal(rerr)
		}
		resumed = append(resumed, ev)
	}

	if got := terminalReasonOf(t, resumed); got != "completed" {
		t.Fatalf("resumed run ended %q, want completed", got)
	}
	last := rec.prompts[len(rec.prompts)-1]
	if got := toolResultsFor(last, "tu1"); len(got) != 1 || got[0] != `{"output":"child"}` {
		t.Fatalf("the post-delegation prompt answers tu1 with %q, want the child's result once", got)
	}
}

// parallelTurn is a model turn that asks for two tools at once, which a model
// making parallel calls does routinely.
var parallelTurn = fake.Script{Chunks: []fake.ChunkSpec{
	{Kind: "tool_use", ToolUseID: "tu1", ToolName: "platform/echo@v1", Input: `{"msg":"first"}`},
	{Kind: "tool_use", ToolUseID: "tu2", ToolName: "platform/other@v1", Input: `{"msg":"second"}`},
	{Kind: "done", Done: "stop"},
}}

// gatedExecutor holds the tools named in gated for approval and runs the rest,
// recording which tools actually ran.
type gatedExecutor struct {
	gated map[string]bool
	ran   *[]string
}

func (e gatedExecutor) Execute(_ context.Context, req kernel.ToolUseRequest, _ kernel.ExecContext) kernel.ToolResult {
	if e.gated[req.ToolName] {
		return kernel.ToolResult{IsError: true, Reason: "approval required", AwaitingApproval: true, AskKind: "once", CanonicalDigest: []byte("d")}
	}
	*e.ran = append(*e.ran, req.ToolName)
	return kernel.ToolResult{Output: json.RawMessage(`{"ran":"` + req.ToolName + `"}`)}
}

func (e gatedExecutor) ExecuteApproved(_ context.Context, req kernel.ToolUseRequest, _ []byte, _ kernel.ExecContext) kernel.ToolResult {
	*e.ran = append(*e.ran, req.ToolName+" (approved)")
	return kernel.ToolResult{Output: json.RawMessage(`{"echo":"approved"}`)}
}

func resumeFirst(t *testing.T, r *rig, useID uuid.UUID) []eventlog.Event {
	t.Helper()
	var out []eventlog.Event
	for ev, err := range r.k.Resume(context.Background(), rehydrated(t, r),
		kernel.RunConfig{System: "test", ModelID: "fake", MaxTurns: 5, AutonomyLevel: "supervised"},
		kernel.PendingResolution{ToolUseEventID: useID, ToolID: "platform/echo@v1", Input: json.RawMessage(`{"msg":"first"}`), ApprovedDigest: []byte("d"), Decision: kernel.ApprovalDecisionGranted}) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

// When call 1 of a turn needs approval, call 2 has been requested but not run.
// After the approval it must be run, and the model must be shown its real result:
// it asked for the call, and used to be told that it had failed.
func TestResume_ACallAfterTheSuspendedOneIsRunNotFailed(t *testing.T) {
	var ran []string
	exec := gatedExecutor{gated: map[string]bool{"platform/echo@v1": true}, ran: &ran}
	r := newRig(exec, parallelTurn, finalTurn)
	rec := &promptRecorder{inner: fake.New(parallelTurn, finalTurn)}
	r.k.Provider = rec

	events, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Fatalf("before the approval these ran: %v, want nothing (call 1 suspended the run)", ran)
	}
	resumed := resumeFirst(t, r, ofType(events, eventlog.EventToolUse)[0].EventID)

	if got := terminalReasonOf(t, resumed); got != "completed" {
		t.Fatalf("resumed run ended %q, want completed", got)
	}
	if len(ran) != 2 || ran[0] != "platform/echo@v1 (approved)" || ran[1] != "platform/other@v1" {
		t.Fatalf("ran = %v, want the approved call and then the second call", ran)
	}
	last := rec.prompts[len(rec.prompts)-1]
	if got := toolResultsFor(last, "tu2"); len(got) != 1 || got[0] != `{"ran":"platform/other@v1"}` {
		t.Fatalf("the prompt answers tu2 with %q, want its real result once", got)
	}
	if got := toolResultsFor(last, "tu1"); len(got) != 1 || got[0] != `{"echo":"approved"}` {
		t.Fatalf("the prompt answers tu1 with %q, want the approved result once", got)
	}
	for _, e := range r.log.Events(r.sessionID) {
		if e.Type == eventlog.EventToolResult && strings.Contains(string(e.Payload), "interrupted_before_execution") {
			t.Fatal("a call the model asked for was answered with a synthetic interrupted_before_execution error")
		}
	}
	if err := provider.ValidateTranscript(last.Messages); err != nil {
		t.Fatalf("the prompt after the resume is not one a hosted API accepts: %v", err)
	}
}

// If the second call itself needs approval, the run suspends again on it, and it
// is not answered as if it had failed.
func TestResume_ASecondCallThatNeedsApprovalSuspendsTheRunAgain(t *testing.T) {
	var ran []string
	exec := gatedExecutor{gated: map[string]bool{"platform/echo@v1": true, "platform/other@v1": true}, ran: &ran}
	r := newRig(exec, parallelTurn, finalTurn)
	rec := &promptRecorder{inner: fake.New(parallelTurn, finalTurn)}
	r.k.Provider = rec

	events, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	resumed := resumeFirst(t, r, ofType(events, eventlog.EventToolUse)[0].EventID)

	if n := len(ofType(resumed, eventlog.EventTerminal)); n != 0 {
		t.Fatalf("the run ended (%d terminal events) instead of suspending on the second call", n)
	}
	if st, ok := r.log.Status(r.sessionID); !ok || st.Value != eventlog.SessionStatusSuspended {
		t.Fatalf("status = %v, want suspended on the second call", st)
	}
	for _, e := range r.log.Events(r.sessionID) {
		if e.Type == eventlog.EventToolResult && strings.Contains(string(e.Payload), "interrupted_before_execution") {
			t.Fatal("the second call was answered as interrupted although it is waiting for approval")
		}
	}
}
