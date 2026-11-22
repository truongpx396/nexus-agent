// Package email is a webhook surface (README Phase 11, task 11.5): inbound
// via a provider webhook (IMAP poll is a documented deferral — no stdlib
// IMAP client and a poller is meaningfully more code for no additional
// pattern coverage this phase needs), outbound through stdlib net/smtp.
// Structurally the same shape as internal/surfaces/telegram/zalo (their own
// doc comments) — a per-turn principal resolved fresh from the inbound
// payload, session-creation-then-StartRun, outbox-delivered approval
// notifications. Zero kernel change.
package email

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
)

// emailNamespace is telegram.telegramNamespace's own counterpart — a fixed
// UUID namespace for deriving a stable per-(tenant,from-address) UserID,
// recomputed fresh from every inbound message rather than stored in a
// mapping row (task 7.13).
var emailNamespace = uuid.MustParse("6f8e1a2b-0000-4000-8000-000000000003")

// inboundMessage is the one concrete parsed-inbound-email JSON shape this
// surface accepts — the field set common to inbound-parse webhooks (a
// Postmark/Mailgun/SendGrid-style provider adapter would translate its own
// wire format into this before POSTing here, or POST it directly if it
// already matches).
type inboundMessage struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	TextBody  string `json:"text_body"`
	MessageID string `json:"message_id"`
}

// ChannelPort resolves the tenant's admitted email credentials — inbound
// Basic Auth for the webhook, outbound SMTP for replies — both backed by
// migrations/0021_messaging_channels.sql (kind='email_smtp').
type ChannelPort interface {
	// WebhookCredential returns the Basic Auth (username, password) this
	// tenant's inbound webhook URL is protected by. ok=false means no
	// active email channel is configured for tenantID at all.
	WebhookCredential(ctx context.Context, tenantID uuid.UUID) (username, password string, ok bool, err error)
	// SMTPConfig returns everything Sender needs to submit one outbound
	// message on tenantID's behalf — password unsealed, used only inside
	// Sender.Send.
	SMTPConfig(ctx context.Context, tenantID uuid.UUID) (SMTPConfig, error)
}

// SMTPConfig is one tenant's outbound submission config.
type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
}

// ChannelCredential is what a tenant's webhook credential lookup answered:
// ok=false is "no email channel is configured", which is cached like any other
// answer.
type ChannelCredential struct {
	user, pass string
	ok         bool
}

// Server holds everything one email webhook handler needs.
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
	Secrets *chat.TTLCache[ChannelCredential]

	// Resume delivers a message to the conversation's open session; nil starts a
	// new session for every message (chat.Conversation.Resume).
	Resume runs.Resumer

	// Lock serialises one conversation's decide-then-act step
	// (chat.Conversation.Lock); nil leaves it unlocked.
	Lock surfaces.Locker
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/webhooks/email/{tenant_id}", s.handleWebhook)
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

	cred, err := s.Secrets.Get(tenantID.String(), func() (ChannelCredential, error) {
		user, pass, ok, err := s.Channels.WebhookCredential(r.Context(), tenantID)
		return ChannelCredential{user, pass, ok}, err
	})
	if err != nil {
		log.Error().Err(err).Any("tenant_id", tenantID).Msg("email: resolve channel")
		http.Error(w, "resolve channel: internal error", http.StatusInternalServerError)
		return
	}
	wantUser, wantPass := cred.user, cred.pass
	if !cred.ok {
		http.Error(w, "no email channel configured for this tenant", http.StatusNotFound)
		return
	}
	// Basic Auth verified BEFORE the body is ever read/parsed
	// (docs/constitution.md: "before the kernel sees the payload").
	gotUser, gotPass, hasAuth := r.BasicAuth()
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(wantUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPass), []byte(wantPass)) == 1
	if !hasAuth || !userOK || !passOK {
		w.Header().Set("WWW-Authenticate", `Basic realm="nexus-email-webhook"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if s.RateLimit != nil && !s.RateLimit.Allow(tenantID.String()) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var msg inboundMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "invalid message", http.StatusBadRequest)
		return
	}
	if msg.From == "" || msg.TextBody == "" {
		http.Error(w, "from and text_body are required", http.StatusBadRequest)
		return
	}

	userID := uuid.NewSHA1(emailNamespace, fmt.Appendf(nil, "%s:%s", tenantID, msg.From))
	input := msg.TextBody
	if msg.Subject != "" {
		input = msg.Subject + "\n\n" + msg.TextBody
	}
	// sessionKey is deterministic per (tenant, sender address) — see
	// internal/surfaces/telegram/webhook.go's own doc comment on this
	// field for the full rationale. Lower-cased so the same address always
	// maps to the same key regardless of casing.
	sessionKey := "email:" + strings.ToLower(msg.From)
	// deliveryID is the inbound provider's own Message-ID — the
	// provider-native id ClaimDelivery dedupes a redelivered message
	// against. May legitimately be empty for some inbound-parse providers;
	// the inbound-delivery dedup's own documented empty-string behavior
	// (always claims, never dedupes) applies in that case.
	deliveryID := msg.MessageID

	conv := chat.Conversation{
		SurfaceID: Descriptor.SurfaceID, Reader: s.Reader, Dedup: s.Dedup, Launcher: s.Launcher, Resume: s.Resume, Lock: s.Lock,
	}
	err = conv.Dispatch(r.Context(), chat.Inbound{
		TenantID: tenantID, UserID: userID, SessionKey: sessionKey, DeliveryID: deliveryID, Text: input,
	})
	switch {
	case errors.Is(err, chat.ErrBusy):
		http.Error(w, "conversation busy; retry", http.StatusServiceUnavailable)
	case err != nil:
		log.Error().Err(err).Any("tenant_id", tenantID).Msg("email: dispatch")
		http.Error(w, "dispatch: internal error", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
	}
}
