package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// BuildNotification returns the payload for one event worth telling a chat user
// about, or ok=false for every other event: an approval request and the model's
// reply are the two a person has to see. A reply's body is sealed in the log, so
// it is opened here, the way the REST event stream opens it; a failure to open
// is logged and the event skipped, because nothing else can tell the user.
func BuildNotification(ctx context.Context, opener runs.EventOpener, sessionID uuid.UUID, ev eventlog.Event) (payload []byte, ok bool) {
	switch ev.Type { //nolint:exhaustive // only these two event types are ever worth telling a chat user about
	case eventlog.EventApprovalRequested:
		toolID := ""
		if ev.ToolID != nil {
			toolID = *ev.ToolID
		}
		b, err := json.Marshal(surfaces.Notification{Kind: surfaces.NotifyApproval, SessionID: sessionID.String(), ToolID: toolID})
		return b, err == nil
	case eventlog.EventContent:
		plaintext, err := opener.Open(ctx, ev)
		if err != nil {
			log.Error().Err(err).Any("session_id", sessionID).Msg("surfaces: open content event for delivery")
			return nil, false
		}
		var body struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(plaintext, &body); err != nil {
			return nil, false
		}
		b, err := json.Marshal(surfaces.Notification{Kind: surfaces.NotifyContent, Text: body.Body})
		return b, err == nil
	}
	return nil, false
}

// Replier delivers what a chat session produces. The worker that drives a turn
// calls OnEvent for each event it appends, so a reply does not depend on the
// process that took the webhook still being alive, and RetryDue sends whatever
// an earlier attempt left undelivered. Both go through Outbox, whose ledger row
// is the single record of what was owed.
type Replier struct {
	// Senders maps a surface id to its Sender factory. A session started on a
	// surface with no entry here (REST, cron) is never replied to.
	Senders map[string]surfaces.SenderFor
	Reader  runs.Reader
	Outbox  *surfaces.Outbox
}

// recipientOf is the person a session's replies go to. Every chat surface keys
// its conversations "<surface>:<recipient>", so the key alone says who to answer.
func recipientOf(surfaceID, sessionKey string) (string, bool) {
	r, ok := strings.CutPrefix(sessionKey, surfaceID+":")
	return r, ok && r != ""
}

// OnEvent delivers ev if it is a reply or an approval request in a chat
// session. It is cheap for every other event: no read is made for a type that
// is never delivered.
func (r *Replier) OnEvent(ctx context.Context, tenantID, sessionID uuid.UUID, ev eventlog.Event) {
	if ev.Type != eventlog.EventContent && ev.Type != eventlog.EventApprovalRequested {
		return
	}
	run, err := r.Reader.GetRun(ctx, tenantID, sessionID)
	if err != nil {
		log.Error().Err(err).Any("session_id", sessionID).Msg("surfaces: reply: read session")
		return
	}
	r.deliver(ctx, tenantID, run, ev)
}

func (r *Replier) deliver(ctx context.Context, tenantID uuid.UUID, run runs.RunView, ev eventlog.Event) {
	sender, ok := r.Senders[run.SurfaceID]
	if !ok {
		return
	}
	recipient, ok := recipientOf(run.SurfaceID, run.SessionKey)
	if !ok {
		return
	}
	payload, ok := BuildNotification(ctx, r.Reader.EventOpener(tenantID), run.RunID, ev)
	if !ok {
		return
	}
	if err := r.Outbox.Deliver(ctx, tenantID, run.RunID, ev.Seq, run.SurfaceID, recipient, payload, sender(tenantID)); err != nil {
		log.Error().Err(err).Any("session_id", run.RunID).Int64("seq", ev.Seq).Msg("surfaces: reply: deliver")
	}
}

// retryBatch bounds how many deliveries one pass takes for one tenant.
const retryBatch = 50

// RetryDue attempts again every delivery of tenantID that is due: one that
// failed, and one whose first attempt never happened.
func (r *Replier) RetryDue(ctx context.Context, tenantID uuid.UUID) error {
	due, err := r.Outbox.Ledger.ClaimDue(ctx, tenantID, retryBatch)
	if err != nil {
		return fmt.Errorf("surfaces: claim due deliveries: %w", err)
	}
	for _, d := range due {
		sender, ok := r.Senders[d.SurfaceID]
		if !ok {
			continue
		}
		run, err := r.Reader.GetRun(ctx, tenantID, d.SessionID)
		if err != nil {
			log.Error().Err(err).Any("session_id", d.SessionID).Msg("surfaces: retry: read session")
			continue
		}
		events, err := r.Reader.Events(ctx, tenantID, d.SessionID)
		if err != nil {
			log.Error().Err(err).Any("session_id", d.SessionID).Msg("surfaces: retry: read events")
			continue
		}
		for _, ev := range events {
			if ev.Seq != d.Seq {
				continue
			}
			payload, ok := BuildNotification(ctx, r.Reader.EventOpener(tenantID), d.SessionID, ev)
			if !ok {
				break
			}
			if err := r.Outbox.Deliver(ctx, tenantID, d.SessionID, d.Seq, d.SurfaceID, d.Recipient, payload, sender(tenantID)); err != nil {
				log.Error().Err(err).Any("session_id", run.RunID).Int64("seq", d.Seq).Msg("surfaces: retry: deliver")
			}
			break
		}
	}
	return nil
}
