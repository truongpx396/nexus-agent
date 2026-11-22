// Package telegram is a webhook surface (README Phase 11, task 11.4):
// inbound Telegram Bot API updates resolve a per-turn principal and submit
// a run through the ordinary session-creation-then-StartRun sequence every
// surface uses; outbound replies go through the shared
// internal/surfaces.Outbox exactly like REST's own approval-notification
// path already does. Zero kernel change — this package never imports
// kernel/ (tests/contract/boundaries_test.go's wildcard rule).
package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
)

// telegramNamespace is a fixed, arbitrary UUID used only as the namespace
// for deriving a stable per-(tenant-bot,from.id) UserID (uuid.NewSHA1) —
// task 7.13's "resolved fresh from THIS request, never cached" is satisfied
// by recomputing this from the inbound payload on every update, not by
// storing a mapping row; the same Telegram user always maps to the same
// UUID without a lookup table.
var telegramNamespace = uuid.MustParse("6f8e1a2b-0000-4000-8000-000000000001")

// update is the minimal subset of Telegram's Bot API Update object this
// surface actually reads.
type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// ChannelPort resolves the tenant's admitted Telegram credentials —
// internal/connectors-shaped seam (structural, not imported) into
// migrations/0021_messaging_channels.sql; cmd/nexusd's own implementation
// reads the sealed bot token/webhook secret via internal/crypto.
type ChannelPort interface {
	// WebhookSecret returns the tenant's configured secret_token for
	// verifying X-Telegram-Bot-Api-Secret-Token — ok=false means no
	// active telegram channel is configured for tenantID at all.
	WebhookSecret(ctx context.Context, tenantID uuid.UUID) (secret string, ok bool, err error)
	// BotToken returns the tenant's sealed bot token, unsealed — used only
	// by Sender.Send, never logged or placed in an event payload.
	BotToken(ctx context.Context, tenantID uuid.UUID) (token string, err error)
}

// ChannelSecret is what a tenant's webhook secret lookup answered: ok=false is
// "no telegram channel is configured", which is cached like any other answer.
type ChannelSecret struct {
	secret string
	ok     bool
}

// Server holds everything one Telegram webhook handler needs.
type Server struct {
	// Reader finds the conversation a message continues (FindByKey) and opens
	// the sealed events a notification quotes (EventOpener).
	Reader runs.Reader
	// Dedup acknowledges a redelivered message without dispatching it twice.
	Dedup    surfaces.InboundDedup
	Launcher runs.Launcher
	Channels ChannelPort

	// Resume delivers a message to the conversation's open session; nil starts a
	// new session for every message (chat.Conversation.Resume).
	Resume runs.Resumer

	// IPLimit limits requests by client address before anything is looked up, so
	// traffic that carries no valid secret cannot cost a database transaction.
	IPLimit *chat.Limiter

	// RateLimit limits an authenticated tenant's webhook traffic.
	RateLimit *chat.Limiter

	// Secrets caches a tenant's webhook secret for a short time, an absent
	// channel included. Nil looks it up on every request.
	Secrets *chat.TTLCache[ChannelSecret]

	// Lock serialises one conversation's decide-then-act step
	// (chat.Conversation.Lock); nil leaves it unlocked.
	Lock surfaces.Locker
}

// Handler returns the http.Handler cmd/nexusd mounts at
// POST /v1/webhooks/telegram/{tenant_id} — tenant_id in the path is a
// routing key, not a credential; the actual authenticity check is the
// secret_token header compared below, BEFORE the body is ever parsed
// (docs/constitution.md: "verify provider authenticity ... before the
// kernel sees the payload").
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/webhooks/telegram/{tenant_id}", s.handleWebhook)
	return mux
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(r.PathValue("tenant_id"))
	if err != nil {
		http.Error(w, "invalid tenant id", http.StatusBadRequest)
		return
	}

	if s.IPLimit != nil && !s.IPLimit.Allow(chat.ClientIP(r.RemoteAddr)) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}

	cs, err := s.Secrets.Get(tenantID.String(), func() (ChannelSecret, error) {
		secret, ok, err := s.Channels.WebhookSecret(r.Context(), tenantID)
		return ChannelSecret{secret, ok}, err
	})
	if err != nil {
		log.Error().Err(err).Any("tenant_id", tenantID).Msg("telegram: resolve channel")
		http.Error(w, "resolve channel: internal error", http.StatusInternalServerError)
		return
	}
	secret := cs.secret
	if !cs.ok {
		http.Error(w, "no telegram channel configured for this tenant", http.StatusNotFound)
		return
	}
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Rate-limit per tenant BEFORE the body is parsed — a
	// per-tenant bucket is the identity available at this point (Telegram's
	// per-chat identity is inside the body, which fail-closed auth above
	// has already gated); this still bounds a single compromised/abusive
	// tenant's webhook traffic.
	if s.RateLimit != nil && !s.RateLimit.Allow(tenantID.String()) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var upd update
	if err := json.Unmarshal(body, &upd); err != nil {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	if upd.Message == nil || upd.Message.Text == "" {
		w.WriteHeader(http.StatusOK) // not every update is a text message worth starting a run over; ack and ignore
		return
	}

	// Task 7.13: resolved fresh from THIS update, never cached.
	userID := uuid.NewSHA1(telegramNamespace, fmt.Appendf(nil, "%s:%d", tenantID, upd.Message.From.ID))
	chatID := strconv.FormatInt(upd.Message.Chat.ID, 10)
	sessionKey := "telegram:" + chatID
	// deliveryID is Telegram's own update_id — monotonically increasing per
	// bot, never reused — the provider-native id ClaimDelivery dedupes a
	// redelivered update against.
	deliveryID := strconv.FormatInt(upd.UpdateID, 10)

	conv := chat.Conversation{
		SurfaceID: Descriptor.SurfaceID, Reader: s.Reader, Dedup: s.Dedup, Launcher: s.Launcher, Resume: s.Resume, Lock: s.Lock,
	}
	err = conv.Dispatch(r.Context(), chat.Inbound{
		TenantID: tenantID, UserID: userID, SessionKey: sessionKey, DeliveryID: deliveryID, Text: upd.Message.Text,
	})
	switch {
	case errors.Is(err, chat.ErrBusy):
		http.Error(w, "conversation busy; retry", http.StatusServiceUnavailable)
	case err != nil:
		log.Error().Err(err).Any("tenant_id", tenantID).Msg("telegram: dispatch")
		http.Error(w, "dispatch: internal error", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
	}
}
