package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	queueredis "github.com/truongpx396/nexus-agent/internal/queue/redis"
	reportingpg "github.com/truongpx396/nexus-agent/internal/reporting/postgres"
	"github.com/truongpx396/nexus-agent/internal/runctl"
	"github.com/truongpx396/nexus-agent/internal/runs"
	runspg "github.com/truongpx396/nexus-agent/internal/runs/postgres"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
	"github.com/truongpx396/nexus-agent/internal/surfaces/email"
	surfpg "github.com/truongpx396/nexus-agent/internal/surfaces/postgres"
	"github.com/truongpx396/nexus-agent/internal/surfaces/telegram"
	"github.com/truongpx396/nexus-agent/internal/surfaces/zalo"
)

// newMux is the process's outer mux: the unauthenticated probes, the three chat
// webhooks, and everything else (restAPI, behind the REST kit's bearer-token
// check). The chat surfaces are thin translators over the same kernel — each
// starts runs through the one launcher every surface shares, and resumes a paused
// conversation through the same runctl.Control.
func newMux(in *infra, svc *services, restAPI http.Handler, metricsToken string) http.Handler {
	// sessionLock serializes each conversational webhook surface's
	// decide-then-act sequence (look up the session, then resume it or start
	// a fresh one) and the turn it kicks off, keyed on the surface's own
	// session_key. It is deliberately a different key from the queue
	// workers', which lock on the session id for the whole run: sharing one
	// would make a delivery wait out every in-flight run. Two racing
	// deliveries for one chat would otherwise both find the session busy
	// and each start a fresh one. SessionLock's key derivation is a fixed
	// function of the key, so this and startQueueWorkers' own instance
	// need not be the same value.
	sessionLock := queueredis.NewSessionLock(in.redis, 30*time.Second)

	channels := surfpg.NewChannels(in.st, in.keys)
	resume := conversationResumer{ctl: svc.ctl}
	runReader := runspg.Reader{Store: in.st, KeyStore: in.keys}
	inboundDedup := surfpg.NewDedup(in.st)
	telegramSrv := &telegram.Server{
		Reader: runReader, Dedup: inboundDedup, Launcher: svc.launcher, Resume: resume, Channels: channels,
		IPLimit: webhookIPLimit(), RateLimit: chat.NewLimiter(20, time.Minute),
		Secrets: chat.NewTTLCache[telegram.ChannelSecret](webhookSecretTTL, webhookSecretCacheSize),
		Lock:    sessionLock,
	}
	zaloSrv := &zalo.Server{
		Reader: runReader, Dedup: inboundDedup, Launcher: svc.launcher, Resume: resume, Channels: channels,
		IPLimit: webhookIPLimit(), RateLimit: chat.NewLimiter(20, time.Minute),
		Secrets: chat.NewTTLCache[zalo.ChannelSecret](webhookSecretTTL, webhookSecretCacheSize),
		Lock:    sessionLock,
	}
	emailSrv := &email.Server{
		Reader: runReader, Dedup: inboundDedup, Launcher: svc.launcher, Resume: resume, Channels: emailChannels{channels},
		IPLimit: webhookIPLimit(), RateLimit: chat.NewLimiter(20, time.Minute),
		Secrets: chat.NewTTLCache[email.ChannelCredential](webhookSecretTTL, webhookSecretCacheSize),
		Lock:    sessionLock,
	}

	mux := http.NewServeMux()
	// /healthz (liveness) and /readyz (Postgres + Redis + signerd all
	// reachable) are unauthenticated and mounted on this OUTER mux — never
	// wrapped by the REST kit's auth middleware (README task 13.1/13.2):
	// a load balancer or Kubernetes probe has no bearer token to send.
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(in.pool, in.redis, in.signer))
	// /metrics lists every tenant's id, spend and tool names, and each render runs
	// several queries per tenant, so it needs the metrics token (a static bearer
	// token, not a tenant's) and is served from a short cache.
	mux.Handle("GET /metrics", requireBearer(metricsToken, newMetricsHandler(tenantDirectory().ListTenantIDs, reportingpg.New(in.st), in.queue)))
	mux.Handle("/v1/webhooks/telegram/", telegramSrv.Handler())
	mux.Handle("/v1/webhooks/zalo/", zaloSrv.Handler())
	mux.Handle("/v1/webhooks/email/", emailSrv.Handler())
	mux.Handle("/", restAPI)
	return mux
}

// webhookSecretTTL is how long a tenant's webhook credential is remembered, and
// webhookSecretCacheSize bounds how many tenants are. A rotated credential is
// honoured within that time, and a flood of requests for tenants that do not
// exist costs one lookup each per window.
const (
	webhookSecretTTL       = 60 * time.Second
	webhookSecretCacheSize = 4096
)

// webhookIPLimit is the per-client-address limit applied before a webhook looks
// a tenant up: 300 requests a minute from one address, far above what a
// provider sends and far below what a flood needs.
func webhookIPLimit() *chat.Limiter { return chat.NewLimiter(300, time.Minute) }

// conversationResumer is the one runs.Resumer telegram, zalo and email share
// (cron has no human peer to resume a conversation with). It hands a message to
// runctl.Control.Send: a conversation paused for input continues, a session
// still working or waiting on a decision takes it as a steering message, and one
// that has ended reports runs.ErrSessionEnded, so the surface starts a new one.
// The surfaces start runs through the shared runs.Launcher.
type conversationResumer struct{ ctl *runctl.Control }

var _ runs.Resumer = conversationResumer{}

func (r conversationResumer) SendMessage(ctx context.Context, tenantID, sessionID uuid.UUID, input string) error {
	err := r.ctl.Send(ctx, tenantID, sessionID, input)
	if errors.Is(err, runctl.ErrSessionState) {
		return fmt.Errorf("%w: %w", runs.ErrSessionEnded, err)
	}
	return err
}

// newReplier is what answers a chat session: the worker that drives a turn hands
// it each event, and a periodic pass retries what an attempt left undelivered.
func newReplier(in *infra, outbox *surfaces.Outbox) *chat.Replier {
	channels := surfpg.NewChannels(in.st, in.keys)
	client := &http.Client{Timeout: 10 * time.Second}
	return &chat.Replier{
		Reader: runspg.Reader{Store: in.st, KeyStore: in.keys},
		Outbox: outbox,
		Senders: map[string]surfaces.SenderFor{
			telegram.Descriptor.SurfaceID: telegram.NewSender(channels, client),
			zalo.Descriptor.SurfaceID:     zalo.NewSender(channels, client),
			email.Descriptor.SurfaceID:    email.NewSender(emailChannels{channels}),
		},
	}
}

// adminTenantLister implements cron.TenantLister over the same
// separate admin-role connection tenantDirectory already dials — nexus_app
// (the runtime pool) is NOSUPERUSER NOBYPASSRLS and cannot make a
// cross-tenant query at all (migrations/0000_app_role.sql).
type adminTenantLister struct{}

func (adminTenantLister) ListTenantIDs(ctx context.Context) ([]uuid.UUID, error) {
	return tenantDirectory().ListTenantIDs(ctx)
}

// emailChannels satisfies email.ChannelPort over the shared channel directory.
// The directory is shared by every chat surface and so imports none of them; the
// one method whose result type is the email surface's own is converted here.
type emailChannels struct{ *surfpg.Channels }

func (c emailChannels) SMTPConfig(ctx context.Context, tenantID uuid.UUID) (email.SMTPConfig, error) {
	cfg, err := c.Channels.SMTPConfig(ctx, tenantID)
	return email.SMTPConfig(cfg), err
}
