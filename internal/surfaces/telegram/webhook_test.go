package telegram

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
)

type fakeChannels struct {
	secret string
	ok     bool
	err    error
}

func (f fakeChannels) WebhookSecret(context.Context, uuid.UUID) (string, bool, error) {
	return f.secret, f.ok, f.err
}
func (f fakeChannels) BotToken(context.Context, uuid.UUID) (string, error) { return "bot-token", nil }

type fakeStarter struct {
	started bool
	calls   int
	req     runs.Request
}

func (f *fakeStarter) StartRun(_ context.Context, req runs.Request) (<-chan runs.Event, error) {
	f.started = true
	f.calls++
	f.req = req
	ch := make(chan runs.Event)
	close(ch)
	return ch, nil
}

// fakeResumer records what SendMessage was called with — used to
// prove dispatch's own resume-vs-fresh decision, mirroring fakeStarter's
// own shape.
type fakeResumer struct {
	err       error // what SendMessage answers, to script a session that has ended
	resumed   bool
	sessionID uuid.UUID
	input     string
}

func (f *fakeResumer) SendMessage(_ context.Context, _ uuid.UUID, sessionID uuid.UUID, input string) error {
	f.resumed = true
	f.sessionID = sessionID
	f.input = input
	return f.err
}

// fakeLocker is surfaces.Locker's own test double — Acquire's every call
// result is queued up front (acquireResults), so a test can script
// contention (ok=false) as easily as success; Release just records what it
// was called with and, if set, closes done once Release has run.
type fakeLocker struct {
	mu             sync.Mutex
	acquireResults []bool // consumed in order, one per Acquire call; exhausted results are all treated as false
	acquireCalls   []string
	releaseCalls   []releaseCall
	releaseErr     error
	done           chan struct{}
}

type releaseCall struct {
	sessionKey, token string
}

func (f *fakeLocker) Acquire(_ context.Context, sessionKey string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls = append(f.acquireCalls, sessionKey)
	ok := false
	if len(f.acquireResults) > 0 {
		ok = f.acquireResults[0]
		f.acquireResults = f.acquireResults[1:]
	}
	if !ok {
		return "", false, nil
	}
	return "token-" + sessionKey, true, nil
}

func (f *fakeLocker) Release(_ context.Context, sessionKey, token string) error {
	f.mu.Lock()
	f.releaseCalls = append(f.releaseCalls, releaseCall{sessionKey: sessionKey, token: token})
	f.mu.Unlock()
	if f.done != nil {
		close(f.done)
	}
	return f.releaseErr
}

func TestHandleWebhook_WrongSecretRefusedBeforeBodyIsEverParsed(t *testing.T) {
	launcher := &fakeLauncher{}
	s := &Server{Channels: fakeChannels{secret: "correct-secret", ok: true}, Launcher: launcher}
	tenantID := uuid.New()

	// A body that would fail JSON parsing if it were ever reached — proves
	// the auth check runs BEFORE any parsing is attempted
	// (docs/constitution.md: "before the kernel sees the payload").
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/telegram/"+tenantID.String(), bytes.NewReader([]byte("not json at all")))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-secret")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if launcher.started {
		t.Fatal("a run was started despite a wrong secret token")
	}
}

func TestHandleWebhook_NoChannelConfiguredRefusesWithoutStartingARun(t *testing.T) {
	launcher := &fakeLauncher{}
	s := &Server{Channels: fakeChannels{ok: false}, Launcher: launcher}
	tenantID := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/telegram/"+tenantID.String(), bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no channel configured)", rec.Code)
	}
	if launcher.started {
		t.Fatal("a run was started despite no configured channel")
	}
}

// TestHandleWebhook_RateLimitedRefusesBeforeBodyIsParsed pre-exhausts the
// bucket directly (chat.Limiter is unit-tested on its own merits in
// internal/surfaces) so the ONE HTTP request this test issues is the
// rate-limited one — proving the 429 short-circuit happens before
// startRun's DB-touching session creation is ever reached, without this
// unit test needing a real Store (internal/store.Store.InTenantTx panics
// on a nil pool; a request that gets past rate-limiting is covered by
// webhook_integration_test.go instead, the same unit/integration split
// internal/surfaces/rest's own tests draw).
func TestHandleWebhook_RateLimitedRefusesBeforeBodyIsParsed(t *testing.T) {
	launcher := &fakeLauncher{}
	limiter := chat.NewLimiter(1, time.Hour)
	tenantID := uuid.New()
	limiter.Allow(tenantID.String()) // consume the one token this bucket starts with

	s := &Server{Channels: fakeChannels{secret: "s", ok: true}, Launcher: launcher, RateLimit: limiter}
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/telegram/"+tenantID.String(), bytes.NewReader([]byte("would fail to parse if reached")))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "s")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (bucket pre-exhausted)", rec.Code)
	}
	if launcher.started {
		t.Fatal("a run was started despite the rate limit")
	}
}

func TestDescriptor_ClaimsNoStreamingOrApprovalRendering(t *testing.T) {
	if Descriptor.SupportsStreaming {
		t.Error("Descriptor.SupportsStreaming = true, want false — this surface has no server-push channel of its own")
	}
	if Descriptor.CanRenderApprovalContext {
		t.Error("Descriptor.CanRenderApprovalContext = true, want false — a chat bot gets the one-line fallback, not structured rendering")
	}
}

// fakeLauncher records what a surface asked runs.Launcher to start, without a
// database — used to prove dispatch's own start-vs-ignore decisions.
type fakeLauncher struct {
	started bool
	calls   int
	req     runs.LaunchRequest
}

func (f *fakeLauncher) Launch(_ context.Context, req runs.LaunchRequest) (runs.Launched, error) {
	f.started = true
	f.calls++
	f.req = req
	ch := make(chan runs.Event)
	close(ch)
	return runs.Launched{SessionID: uuid.New(), Events: ch}, nil
}

func TestHandleWebhook_AChannelLookupFailureDoesNotLeakTheCause(t *testing.T) {
	cause := errors.New("dial tcp 10.0.0.7:5432: connection refused")
	s := &Server{Channels: fakeChannels{err: cause}}

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/telegram/"+uuid.NewString(), bytes.NewReader(nil))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "10.0.0.7") || strings.Contains(body, "refused") {
		t.Fatalf("the response body %q carries the infrastructure error, which an unauthenticated caller can read", body)
	}
}

// countingChannels counts the lookups that reach it: each one is a tenant-scoped
// database transaction and an unseal in production.
type countingChannels struct {
	fakeChannels
	lookups atomic.Int64
}

func (c *countingChannels) WebhookSecret(ctx context.Context, id uuid.UUID) (string, bool, error) {
	c.lookups.Add(1)
	return c.fakeChannels.WebhookSecret(ctx, id)
}

// A flood of requests for tenants that do not exist, none carrying a valid
// secret, must not become a flood of lookups: the per-address limit refuses the
// excess before any tenant is read.
func TestHandleWebhook_AFloodOfUnauthenticatedRequestsCostsABoundedNumberOfLookups(t *testing.T) {
	ch := &countingChannels{}
	s := &Server{Channels: ch, IPLimit: chat.NewLimiter(100, time.Hour)}

	limited := 0
	for range 1000 {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/telegram/"+uuid.NewString(), bytes.NewReader(nil))
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if got := ch.lookups.Load(); got != 100 {
		t.Fatalf("%d lookups reached the channel store for 1,000 unauthenticated requests, want 100", got)
	}
	if limited != 900 {
		t.Fatalf("%d requests were refused with 429, want 900", limited)
	}
}

// The same tenant's secret is read once per window, not once per request.
func TestHandleWebhook_ATenantsSecretIsLookedUpOncePerWindow(t *testing.T) {
	ch := &countingChannels{fakeChannels: fakeChannels{secret: "s", ok: true}}
	s := &Server{Channels: ch, Secrets: chat.NewTTLCache[ChannelSecret](time.Minute, 16)}
	tenantID := uuid.New()

	for range 5 {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/telegram/"+tenantID.String(), bytes.NewReader(nil))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
		s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := ch.lookups.Load(); got != 1 {
		t.Fatalf("%d lookups for 5 requests of one tenant, want 1", got)
	}
}
