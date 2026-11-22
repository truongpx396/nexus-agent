// Package zalo is a webhook surface (README Phase 11, task 11.4),
// structurally the same shape as internal/surfaces/telegram (its own doc
// comment) with one thing genuinely different: how inbound authenticity is
// verified. Zalo's Official Account platform signs each webhook body with
// an HMAC over the app secret rather than a static header token; this
// package implements the general shared-secret HMAC-over-raw-body pattern
// that models — see verifySignature's own doc comment for the honesty note
// on what is and isn't claimed about byte-exact fidelity to Zalo's own
// wire format. Zero kernel change — this package never imports kernel/.
package zalo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
)

// zaloNamespace is telegram.telegramNamespace's own counterpart — a fixed
// UUID namespace for deriving a stable per-(tenant-OA,sender.id) UserID,
// recomputed fresh from every inbound event rather than stored in a
// mapping row (task 7.13).
var zaloNamespace = uuid.MustParse("6f8e1a2b-0000-4000-8000-000000000002")

// inboundEvent is the minimal subset of a Zalo OA "user_send_text" webhook
// event this surface reads.
type inboundEvent struct {
	AppID     string `json:"app_id"`
	EventName string `json:"event_name"`
	Sender    struct {
		ID string `json:"id"`
	} `json:"sender"`
	Message struct {
		Text  string `json:"text"`
		MsgID string `json:"msg_id"`
	} `json:"message"`
}

// ChannelPort resolves the tenant's admitted Zalo credentials —
// structurally identical to internal/surfaces/telegram.ChannelPort, into
// the same migrations/0021_messaging_channels.sql table (kind='zalo').
type ChannelPort interface {
	// AppSecret returns the tenant's configured OA app secret, unsealed —
	// used both to verify an inbound signature and (via Sender) to
	// authenticate outbound sends. ok=false means no active zalo channel
	// is configured for tenantID at all.
	AppSecret(ctx context.Context, tenantID uuid.UUID) (secret string, ok bool, err error)
	// AccessToken returns the tenant's current OA send-API access token,
	// unsealed — used only by Sender.Send.
	AccessToken(ctx context.Context, tenantID uuid.UUID) (token string, err error)
}

// ChannelSecret is what a tenant's app secret lookup answered: ok=false is "no
// zalo channel is configured", which is cached like any other answer.
type ChannelSecret struct {
	secret string
	ok     bool
}

// Server holds everything one Zalo webhook handler needs.
type Server struct {
	// Reader finds the conversation a message continues (FindByKey) and opens
	// the sealed events a notification quotes (EventOpener).
	Reader runs.Reader
	// Dedup acknowledges a redelivered message without dispatching it twice.
	Dedup    surfaces.InboundDedup
	Launcher runs.Launcher
	Channels ChannelPort

	// IPLimit limits requests by client address before anything is looked up, so
	// traffic that carries no valid credential cannot cost a database transaction.
	IPLimit *chat.Limiter

	// RateLimit limits an authenticated tenant's webhook traffic.
	RateLimit *chat.Limiter

	// Secrets caches a tenant's webhook credential for a short time, an absent
	// channel included. Nil looks it up on every request.
	Secrets *chat.TTLCache[ChannelSecret]

	// Resume delivers a message to the conversation's open session; nil starts a
	// new session for every message (chat.Conversation.Resume).
	Resume runs.Resumer

	// Lock serialises one conversation's decide-then-act step
	// (chat.Conversation.Lock); nil leaves it unlocked.
	Lock surfaces.Locker
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/webhooks/zalo/{tenant_id}", s.handleWebhook)
	return mux
}

// verifySignature checks an HMAC-SHA256-over-raw-body signature against
// the tenant's app secret — the general shared-secret webhook-authenticity
// pattern this surface models (docs/constitution.md's "verify provider
// authenticity ... before the kernel sees the payload"). Honesty note,
// matching this codebase's own documented-gap convention: Zalo OA's real
// wire format computes its "mac" field over a specific ordered subset of
// the JSON body's own fields, not the raw byte stream — reproducing that
// exact algorithm needs Zalo's own current API reference in hand, which
// this implementation does not claim byte-exact fidelity to. What IS real
// here is the security property that matters for this phase: the header
// is verified against a per-tenant secret, in constant time, before the
// body is parsed — the same shape a byte-exact implementation would need
// regardless.
func verifySignature(body []byte, secret, headerSig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(headerSig))
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
		secret, ok, err := s.Channels.AppSecret(r.Context(), tenantID)
		return ChannelSecret{secret, ok}, err
	})
	if err != nil {
		log.Error().Err(err).Any("tenant_id", tenantID).Msg("zalo: resolve channel")
		http.Error(w, "resolve channel: internal error", http.StatusInternalServerError)
		return
	}
	secret := cs.secret
	if !cs.ok {
		http.Error(w, "no zalo channel configured for this tenant", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	// Signature is verified over the raw body BEFORE any JSON parsing is
	// attempted — same ordering discipline as Telegram's header check.
	if !verifySignature(body, secret, r.Header.Get("X-ZEvent-Signature")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if s.RateLimit != nil && !s.RateLimit.Allow(tenantID.String()) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}

	var ev inboundEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}
	if ev.Message.Text == "" {
		w.WriteHeader(http.StatusOK) // not every OA event is a text message worth starting a run over
		return
	}

	userID := uuid.NewSHA1(zaloNamespace, fmt.Appendf(nil, "%s:%s", tenantID, ev.Sender.ID))
	// sessionKey is deterministic per (tenant, sender) — see
	// internal/surfaces/telegram/webhook.go's own doc comment on this
	// field for the full rationale.
	sessionKey := "zalo:" + ev.Sender.ID
	// deliveryID is Zalo's own msg_id — the provider-native id ClaimDelivery
	// dedupes a redelivered event against. Existing test fixtures don't set
	// this field, so it defaults to "" and the inbound-delivery dedup's own
	// documented empty-string behavior (always claims, never dedupes)
	// applies — intentional, not a gap this change introduces.
	deliveryID := ev.Message.MsgID

	conv := chat.Conversation{
		SurfaceID: Descriptor.SurfaceID, Reader: s.Reader, Dedup: s.Dedup, Launcher: s.Launcher, Resume: s.Resume, Lock: s.Lock,
	}
	err = conv.Dispatch(r.Context(), chat.Inbound{
		TenantID: tenantID, UserID: userID, SessionKey: sessionKey, DeliveryID: deliveryID, Text: ev.Message.Text,
	})
	switch {
	case errors.Is(err, chat.ErrBusy):
		http.Error(w, "conversation busy; retry", http.StatusServiceUnavailable)
	case err != nil:
		log.Error().Err(err).Any("tenant_id", tenantID).Msg("zalo: dispatch")
		http.Error(w, "dispatch: internal error", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
	}
}
