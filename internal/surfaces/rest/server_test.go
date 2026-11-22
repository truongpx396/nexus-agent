package rest_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	restkit "github.com/truongpx396/nexus-agent/internal/surfaces/rest"
	"github.com/truongpx396/nexus-agent/internal/surfaces/rest/resttest"
)

// pingModule is a Module with one route that reports the principal it was
// handed.
type pingModule struct{}

func (pingModule) Routes(r restkit.Router) {
	r.GET("/ping", func(c *gin.Context) {
		tenantID, userID, ok := restkit.Principal(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, map[string]string{"tenant": tenantID.String(), "user": userID.String()})
	})
}

func TestServer_EveryModuleRouteIsAuthenticated(t *testing.T) {
	tenantID, userID := uuid.New(), uuid.New()
	h := restkit.NewServer(resttest.Verifier{TenantID: tenantID, UserID: userID}, pingModule{}).Handler()

	// No token: the module cannot opt out of authentication, because the only
	// way to register a route is Router.GET, Router.POST or Router.Authenticated.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /ping with no token = %d, want 401", rec.Code)
	}

	rec = resttest.Do(t, h, http.MethodGet, "/ping", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ping with a valid token = %d, want 200 (%s)", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); !strings.Contains(got, tenantID.String()) || !strings.Contains(got, userID.String()) {
		t.Errorf("body %q does not carry the verified principal", got)
	}
}

// An unmounted path answers 404 without asking for a token first: wrapping the
// whole router in authentication would turn every 404 into a 401 and hide which
// capabilities a deployment has.
func TestServer_AnUnmountedPathIs404NotUnauthorized(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{}, pingModule{}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /nowhere = %d, want 404", rec.Code)
	}
}

func TestServer_AnswersABrowsersPreflightWithoutAToken(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{}, pingModule{}).Handler()
	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /ping = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("Allow-Origin = %q, want the caller's origin", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("a preflight must say which headers are allowed")
	}
}

// A known path on the wrong method answers 405 with an Allow header, as net/http's
// mux did: the caller learns the route exists, and a 404 stays reserved for a path
// nothing mounts.
func TestServer_AWrongMethodIs405WithAnAllowHeader(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{TenantID: uuid.New(), UserID: uuid.New()}, pingModule{}).Handler()
	rec := resttest.Do(t, h, http.MethodPost, "/ping", `{}`)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /ping = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q, want it to name GET", allow)
	}
}

// A trailing slash is a different path, not a redirect: a redirect would send an
// authenticated request, body and all, to a route the caller did not name.
func TestServer_ATrailingSlashIs404NotARedirect(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{TenantID: uuid.New(), UserID: uuid.New()}, pingModule{}).Handler()
	rec := resttest.Do(t, h, http.MethodGet, "/ping/", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /ping/ = %d (Location %q), want 404", rec.Code, rec.Header().Get("Location"))
	}
}

// panicModule has a route that panics and one that answers, so a test can show the
// server surviving the first.
type panicModule struct{}

func (panicModule) Routes(r restkit.Router) {
	r.GET("/boom", func(*gin.Context) { panic("handler bug with a secret inside") })
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "fine") })
}

func TestServer_APanickingHandlerAnswers500AndTheServerKeepsServing(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{TenantID: uuid.New(), UserID: uuid.New()}, panicModule{}).Handler()

	rec := resttest.Do(t, h, http.MethodGet, "/boom", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a panicking handler answered %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("the 500 carried the panic value: %q", rec.Body.String())
	}
	if rec = resttest.Do(t, h, http.MethodGet, "/ok", ""); rec.Code != http.StatusOK {
		t.Fatalf("the server did not keep serving after a panic: %d", rec.Code)
	}
}

func TestServer_EveryResponseCarriesARequestID(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{TenantID: uuid.New(), UserID: uuid.New()}, panicModule{}).Handler()

	generated := resttest.Do(t, h, http.MethodGet, "/ok", "").Header().Get("X-Request-Id")
	if _, err := uuid.Parse(generated); err != nil {
		t.Fatalf("no request id was generated: %q", generated)
	}

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+resttest.Token)
	req.Header.Set("X-Request-Id", "trace-abc.123_def")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); got != "trace-abc.123_def" {
		t.Fatalf("a well-formed X-Request-Id was replaced: %q", got)
	}

	// An id with a line break or other junk could forge a log line; it is replaced.
	req = httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer "+resttest.Token)
	req.Header.Set("X-Request-Id", "bad id\nlevel=error msg=forged")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); strings.ContainsAny(got, " \n") || got == "" {
		t.Fatalf("a malformed X-Request-Id was echoed: %q", got)
	}
}

// RFC 7235: the authentication scheme is case-insensitive.
func TestServer_TheBearerSchemeIsCaseInsensitive(t *testing.T) {
	h := restkit.NewServer(resttest.Verifier{TenantID: uuid.New(), UserID: uuid.New()}, panicModule{}).Handler()
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		req := httptest.NewRequest(http.MethodGet, "/ok", nil)
		req.Header.Set("Authorization", scheme+" "+resttest.Token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("scheme %q = %d, want 200", scheme, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Basic "+resttest.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("scheme Basic = %d, want 401", rec.Code)
	}
}

// Each request is counted and timed under its route template, so /metrics can say
// which route a 5xx came from without carrying an identifier in a label.
func TestServer_RequestsAreCountedPerRouteTemplateAndStatus(t *testing.T) {
	srv := restkit.NewServer(resttest.Verifier{TenantID: uuid.New(), UserID: uuid.New()}, panicModule{}, pingModule{})
	srv.Metrics = restkit.NewRouteMetrics()
	h := srv.Handler()
	for range 3 {
		resttest.Do(t, h, http.MethodGet, "/ok", "")
	}
	resttest.Do(t, h, http.MethodGet, "/boom", "")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nowhere/12345", nil))

	var out strings.Builder
	srv.Metrics.WritePrometheus(&out)
	for _, want := range []string{
		`nexus_http_requests_total{method="GET",route="/ok",status="200"} 3`,
		`nexus_http_requests_total{method="GET",route="/boom",status="500"} 1`,
		`nexus_http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`nexus_http_request_duration_seconds_count{method="GET",route="/ok",status="200"} 3`,
		`le="+Inf"} 3`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("metrics lack %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "12345") {
		t.Error("a path parameter leaked into a metric label")
	}
}
