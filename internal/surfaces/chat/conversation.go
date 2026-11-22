package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// ErrBusy reports a conversation whose lock another delivery holds. The
// delivery is not dropped: the caller answers with an error, the provider
// sends it again, and the retry is dispatched.
var ErrBusy = errors.New("conversation is busy")

// Inbound is one message a person sent a chat surface, after the surface has
// authenticated and parsed it.
type Inbound struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	// SessionKey names the conversation, "<surface>:<peer>": every message from
	// one peer finds the same key, so a reply continues the open conversation.
	SessionKey string
	// DeliveryID is the provider's own id for the message, which a redelivery
	// repeats. Empty for a provider that sends none.
	DeliveryID string
	Text       string
}

// Conversation is the decide-then-act sequence every chat surface runs for an
// inbound message: claim the delivery, find the conversation, then continue it
// or start a new one. A surface keeps only what is its own (how it
// authenticates and parses a request, and the Sender that answers) and hands
// the rest to this type.
//
// It does not wait for the turn. The worker that drives the run publishes its
// events, and Replier delivers the answer, so nothing here outlives the call.
type Conversation struct {
	SurfaceID string
	Reader    runs.Reader
	Dedup     surfaces.InboundDedup
	Launcher  runs.Launcher

	// Resume continues an existing conversation. Nil starts a new session for
	// every message.
	Resume runs.Resumer

	// Lock serialises the decide-then-act step for one conversation, so two
	// deliveries racing for it cannot each decide there is no open session and
	// both start one. It is held for that step only, never for the turn. Nil
	// leaves the step unlocked.
	Lock surfaces.Locker
}

// Dispatch handles one inbound message. A nil error means the message reached a
// session or was a redelivery of one that had; any error means it did not, and
// the claim on its delivery id is released so that the provider's retry is
// processed.
func (c *Conversation) Dispatch(ctx context.Context, in Inbound) (err error) {
	claimed, err := c.Dedup.Claim(ctx, in.TenantID, c.SurfaceID, in.DeliveryID, in.SessionKey)
	if err != nil {
		return fmt.Errorf("claim delivery: %w", err)
	}
	if !claimed {
		log.Info().Any("tenant_id", in.TenantID).Str("surface", c.SurfaceID).Str("delivery_id", in.DeliveryID).
			Msg("surfaces: duplicate delivery acknowledged without dispatching")
		return nil
	}
	defer func() {
		if err == nil {
			return
		}
		if rerr := c.Dedup.Release(context.WithoutCancel(ctx), in.TenantID, c.SurfaceID, in.DeliveryID); rerr != nil {
			log.Error().Err(rerr).Any("tenant_id", in.TenantID).Str("surface", c.SurfaceID).Str("delivery_id", in.DeliveryID).
				Msg("surfaces: release claim after a failed dispatch; the provider's retry will be treated as a duplicate")
		}
	}()

	if c.Lock != nil {
		token, ok, lerr := surfaces.AcquireSessionLock(ctx, c.Lock, in.SessionKey)
		if lerr != nil {
			return fmt.Errorf("acquire session lock: %w", lerr)
		}
		if !ok {
			return fmt.Errorf("%w: %s", ErrBusy, in.SessionKey)
		}
		defer func() {
			if rerr := c.Lock.Release(context.WithoutCancel(ctx), in.SessionKey, token); rerr != nil {
				log.Error().Err(rerr).Str("session_key", in.SessionKey).Msg("surfaces: session lock release failed")
			}
		}()
	}

	// A run outlives the webhook request that started it.
	runCtx := context.WithoutCancel(ctx)

	if c.Resume != nil {
		sess, found, ferr := c.Reader.FindByKey(ctx, in.TenantID, in.SessionKey)
		if ferr != nil {
			return fmt.Errorf("look up session for %s: %w", in.SessionKey, ferr)
		}
		if found && !ended(sess.Status) {
			serr := c.Resume.SendMessage(runCtx, in.TenantID, sess.RunID, in.Text)
			if serr == nil {
				return nil
			}
			if !errors.Is(serr, runs.ErrSessionEnded) {
				return fmt.Errorf("send message to session %s: %w", sess.RunID, serr)
			}
			// It ended between the lookup and the send: start a new one.
		}
	}
	if _, lerr := c.Launcher.Launch(runCtx, runs.LaunchRequest{
		TenantID: in.TenantID, UserID: in.UserID, SurfaceID: c.SurfaceID, SessionKey: in.SessionKey,
		Input: in.Text, Autonomy: "supervised", Conversational: true,
	}); lerr != nil {
		return fmt.Errorf("start run: %w", lerr)
	}
	return nil
}

// ended reports whether a session takes no more messages.
func ended(status string) bool {
	return status == eventlog.SessionStatusCompleted || status == eventlog.SessionStatusFailed
}
