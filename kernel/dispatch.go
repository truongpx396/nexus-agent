package kernel

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/obs"
	"github.com/truongpx396/nexus-agent/internal/provider"
)

// dispatchCall runs one tool call of a turn and handles everything its outcome
// decides: it appends the paired tool_result, records a taint transition, ends the
// run on a permission denial, suspends it on an approval or a delegation, and feeds
// the stuck detector. runTurns calls it for each call the model made in a turn, and
// the resume paths call it for the calls of that turn that were still waiting when
// a suspension was resolved, so the two cannot drift.
//
// It returns the result block for the transcript and stop=true when the run must
// not go on: it terminated, it suspended, an append failed, or the consumer stopped
// reading. Every one of those has already been yielded.
func (k *Kernel) dispatchCall(ctx, parent context.Context, st *RunState, execCtx ExecContext, tu ToolUseRequest, useEvent eventlog.Event, yield func(eventlog.Event, error) bool) (block provider.ContentBlock, stop bool) {
	toolCtx, toolSpan := k.startSpan(parent, "tool.call", obs.ObservationTool, obs.Attrs{"tool.id": tu.ToolName})
	result := k.Tools.Execute(toolCtx, tu, execCtx)
	outcome := "ok"
	if result.IsError {
		outcome = "error"
	}
	if k.TraceContent {
		toolSpan.SetContent(string(tu.Input), resultText(result))
	}
	toolSpan.End(obs.Attrs{"outcome": outcome})

	ev, err := k.appendToolResult(ctx, st, useEvent.EventID, useEvent.ToolID, result)
	if err != nil {
		yield(eventlog.Event{}, err)
		return provider.ContentBlock{}, true
	}
	block = provider.ContentBlock{Kind: provider.BlockToolResult, ToolUseID: tu.ToolUseID, Text: resultText(result), IsError: result.IsError}
	if !yield(ev, nil) {
		return block, true
	}

	// The durable half of Rule-of-Two taint state (README pattern 21's own "session
	// taint state as a projection" — kernel.RehydrateTaint is the read half):
	// appended regardless of what happens next (permission denied, awaiting
	// approval/delegation, stuck) — the leg was engaged for real the moment the
	// permission chain resolved it (tools.ExecuteResult's own doc comment), whether
	// or not this call's own outcome goes on to suspend or terminate the run.
	if result.TaintChanged {
		tev, err := k.appendEvent(ctx, st, eventlog.EventTaintTransition, eventlog.ActorSystem, nil, nil, nil, taintTransitionPayload{Engaged: result.TaintEngaged})
		if err != nil {
			yield(eventlog.Event{}, err)
			return block, true
		}
		if !yield(tev, nil) {
			return block, true
		}
	}

	if result.PermissionDenied {
		toolID := "unknown"
		if useEvent.ToolID != nil {
			toolID = *useEvent.ToolID
		}
		k.terminate(ctx, st, yield, TerminalPermissionDenied(toolID))
		return block, true
	}
	if result.AwaitingApproval {
		k.suspendForApproval(ctx, st, yield, useEvent.EventID, useEvent.ToolID, tu.Input, result)
		return block, true
	}
	if result.AwaitingDelegation {
		k.suspendForDelegation(ctx, st, yield, useEvent.EventID, useEvent.ToolID, result.ChildSessionID)
		return block, true
	}

	if k.Stuck != nil {
		verdict := k.Stuck.Record(st.SessionID, tu.ToolName, tu.Input)
		if verdict.Suspected {
			sev, err := k.appendEvent(ctx, st, eventlog.EventStuckSuspected, eventlog.ActorSystem, nil, nil, nil, stuckSuspectedPayload{Reason: string(verdict.Reason)})
			if err != nil {
				yield(eventlog.Event{}, err)
				return block, true
			}
			if !yield(sev, nil) {
				return block, true
			}
			if verdict.Terminate {
				k.terminate(ctx, st, yield, TerminalStuckTerminated(string(verdict.Reason)))
				return block, true
			}
		}
	}
	return block, false
}

// runRemainingCalls executes the calls of the turn that were still waiting when a
// suspension was resolved. A turn whose assistant message holds several tool_use
// blocks runs them in order, and when call i suspends the run, calls i+1 to n have
// durable tool_use events but have not run. Resuming used to settle only call i and
// enter the turn loop, whose hygiene pass then answered each of the rest with a
// synthetic "interrupted_before_execution" error: the model asked for calls that
// were never attempted and was told they had failed, which a model making parallel
// calls meets on every approval under supervised autonomy.
//
// resolved is the tool_use event the suspension was on. The calls are those after
// it that still have no result, in the order they were requested, run through the
// ordinary dispatch, so one of them can itself need an approval, in which case the
// run suspends again on it and the calls after that wait in turn. It reports
// ok=false when the run ended or suspended and the turn loop must not be entered.
func (k *Kernel) runRemainingCalls(ctx context.Context, st *RunState, cfg RunConfig, resolved uuid.UUID, yield func(eventlog.Event, error) bool) (ok bool) {
	pending := pendingCalls(st, resolved)
	if len(pending) == 0 {
		return true
	}
	execCtx := ExecContext{TenantID: st.TenantID, SessionID: st.SessionID, AutonomyLevel: k.currentAutonomy(ctx, st, cfg)}
	for _, ev := range pending {
		tu, found := toolUseRequest(st, ev)
		if !found {
			// The assistant message that made the call is not in the transcript, so
			// there is nothing to run it with. Leave it for the hygiene pass, which
			// records that it was never run.
			continue
		}
		block, stop := k.dispatchCall(ctx, ctx, st, execCtx, tu, ev, yield)
		if stop {
			return false
		}
		st.settleToolResult(tu.ToolUseID, block.Text, block.IsError)
	}
	return true
}

// pendingCalls are the tool_use events after resolved that no tool_result answers,
// in the order they were requested.
func pendingCalls(st *RunState, resolved uuid.UUID) []eventlog.Event {
	var after int64
	answered := map[uuid.UUID]bool{}
	for _, e := range st.History {
		switch e.Type { //nolint:exhaustive // only a tool_use and its result matter here
		case eventlog.EventToolUse:
			if e.EventID == resolved {
				after = e.Seq
			}
		case eventlog.EventToolResult:
			if e.PairRef != nil {
				answered[*e.PairRef] = true
			}
		}
	}
	var out []eventlog.Event
	for _, e := range st.History {
		if e.Type == eventlog.EventToolUse && e.Seq > after && !answered[e.EventID] {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b eventlog.Event) int { return int(a.Seq - b.Seq) })
	return out
}

// toolUseRequest rebuilds the request the model made for ev from the transcript,
// where the assistant message holds the call's name and input under the provider's
// own id.
func toolUseRequest(st *RunState, ev eventlog.Event) (ToolUseRequest, bool) {
	id, ok := st.ToolUseIDs[ev.EventID]
	if !ok {
		return ToolUseRequest{}, false
	}
	for _, m := range st.Transcript {
		for _, b := range m.Blocks {
			if b.Kind == provider.BlockToolUse && b.ToolUseID == id {
				return ToolUseRequest{ToolUseID: id, ToolName: b.ToolName, Input: b.Input}, true
			}
		}
	}
	return ToolUseRequest{}, false
}
