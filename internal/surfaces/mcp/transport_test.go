package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/surfaces/mcp"
)

// strictServer behaves as a server that enforces the Streamable HTTP transport: it
// wants both Accept types, assigns a session and requires it, requires the protocol
// header and the initialized notification, and answers tools/call as an event stream
// with a server notification ahead of the response.
type strictServer struct {
	*httptest.Server
	handshakes  atomic.Int32
	initialized atomic.Bool
	expireNext  atomic.Bool // the next request that carries a session id is answered 404, once
}

const strictSession = "session-7"

func newStrictServer(t *testing.T) *strictServer {
	t.Helper()
	s := &strictServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			http.Error(w, "Accept must list application/json and text/event-stream", http.StatusNotAcceptable)
			return
		}
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Method == "initialize" {
			s.handshakes.Add(1)
			s.initialized.Store(false)
			w.Header().Set("Mcp-Session-Id", strictSession)
			w.Header().Set("content-type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": map[string]any{"protocolVersion": "2025-06-18"}})
			return
		}
		if r.Header.Get("Mcp-Session-Id") != strictSession {
			http.Error(w, "missing or unknown Mcp-Session-Id", http.StatusBadRequest)
			return
		}
		if s.expireNext.CompareAndSwap(true, false) {
			http.Error(w, "session expired", http.StatusNotFound)
			return
		}
		if r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
			http.Error(w, "missing MCP-Protocol-Version", http.StatusBadRequest)
			return
		}
		if req.Method == "notifications/initialized" {
			s.initialized.Store(true)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if !s.initialized.Load() {
			http.Error(w, "notifications/initialized not received", http.StatusConflict)
			return
		}
		switch req.Method {
		case "tools/list":
			w.Header().Set("content-type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": map[string]any{"tools": []mcp.Tool{issueTool}}})
		case "tools/call":
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"info\"}}\n\n")
			_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n\n", *req.ID)
		default:
			http.Error(w, "unknown method", http.StatusNotImplemented)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestClient_WorksAgainstAStrictStreamableHTTPServer(t *testing.T) {
	srv := newStrictServer(t)
	c := &mcp.Client{BaseURL: srv.URL}

	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "create_issue" {
		t.Fatalf("ListTools = %v, %v, want the server's one tool", tools, err)
	}
	for range 3 {
		res, err := c.CallTool(context.Background(), "create_issue", json.RawMessage(`{"title":"x"}`))
		if err != nil || len(res.Content) != 1 || res.Content[0].Text != "done" {
			t.Fatalf("CallTool = %+v, %v, want the response read out of the event stream", res, err)
		}
	}
	if n := srv.handshakes.Load(); n != 1 {
		t.Fatalf("%d handshakes for four calls, want one: the session is reused", n)
	}
}

func TestClient_ASessionTheServerEndedIsReplacedAndTheCallRetriedOnce(t *testing.T) {
	srv := newStrictServer(t)
	c := &mcp.Client{BaseURL: srv.URL}
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}

	srv.expireNext.Store(true)
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("a call after the session expired failed instead of starting a new one: %v", err)
	}
	if n := srv.handshakes.Load(); n != 2 {
		t.Fatalf("%d handshakes, want 2 (the original and the replacement)", n)
	}
}

func TestClient_ARefusedStatusIsAnErrorNotAnEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := (&mcp.Client{BaseURL: srv.URL}).ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("ListTools against a 500 = %v, want an error naming the status", err)
	}
}

// A server that never stops writing must not make the process allocate until it is
// killed, whether it answers in JSON or as an event stream.
func TestClient_AnOversizedResponseIsRefusedNotBuffered(t *testing.T) {
	for _, kind := range []string{"application/json", "text/event-stream"} {
		t.Run(kind, func(t *testing.T) {
			var written atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req.Method == "initialize" {
					w.Header().Set("content-type", "application/json")
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
					return
				}
				w.Header().Set("content-type", kind)
				if kind == "application/json" {
					_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"`))
				} else {
					_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":\""))
				}
				chunk := []byte(strings.Repeat("a", 64<<10))
				for range 2000 { // 128 MB if the client kept reading
					n, err := w.Write(chunk)
					written.Add(int64(n))
					if err != nil {
						return
					}
				}
			}))
			defer srv.Close()

			_, err := (&mcp.Client{BaseURL: srv.URL}).ListTools(context.Background())
			if err == nil {
				t.Fatal("ListTools = nil error for a response that never ends")
			}
			if w := written.Load(); w > 16<<20 {
				t.Fatalf("the server wrote %d bytes before the client gave up; the read is not bounded", w)
			}
		})
	}
}

// By default the resolver talks through the egress-safe client, so a server that is
// on the allowlist but reachable only at a private address is refused.
func TestResolve_TheDefaultClientRefusesAPrivateAddress(t *testing.T) {
	srv := newRemote(t, issueTool) // listens on the loopback interface
	d := &fakeDirectory{servers: map[string]mcp.Server{"gh": {ServerID: uuid.New(), Name: "gh", BaseURL: srv.URL, AuthKind: "none", Status: "admitted"}}}
	r := &mcp.Resolver{Servers: d, AllowedHosts: []string{"*"}} // AllowPrivateNetworks left false

	_, ok, err := r.Resolve(context.Background(), uuid.New(), uuid.New(), refFor("gh", issueTool))
	if err == nil || ok || !strings.Contains(err.Error(), "private") {
		t.Fatalf("Resolve = ok %v, err %v, want a refusal to connect to a private address", ok, err)
	}
	if srv.lists.Load() != 0 {
		t.Fatal("the server was reached although its address is private")
	}
}

// A redirect is held to the allowlist: an allowed host cannot send the request on
// to one the allowlist never named.
func TestResolve_ARedirectOutOfTheAllowlistIsRefused(t *testing.T) {
	target := newRemote(t, issueTool)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	d := &fakeDirectory{servers: map[string]mcp.Server{"gh": {ServerID: uuid.New(), Name: "gh", BaseURL: redirector.URL, AuthKind: "none", Status: "admitted"}}}
	r := &mcp.Resolver{Servers: d, AllowedHosts: []string{"127.0.0.1"}, AllowPrivateNetworks: true}

	_, ok, err := r.Resolve(context.Background(), uuid.New(), uuid.New(), refFor("gh", issueTool))
	if err == nil || ok || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("Resolve = ok %v, err %v, want the redirect refused by the allowlist", ok, err)
	}
	if target.lists.Load() != 0 {
		t.Fatal("the redirect target was reached")
	}
}

// The session is kept per server and credential, so listing and calling many times
// does not repeat the handshake, and a different credential is a different session.
func TestResolve_TheHandshakeHappensOncePerServerAndCredential(t *testing.T) {
	srv := newStrictServer(t)
	d := &fakeDirectory{
		servers: map[string]mcp.Server{"gh": {ServerID: uuid.New(), Name: "gh", BaseURL: srv.URL, AuthKind: "bearer_static", Status: "admitted"}},
		tokens:  map[string]string{"gh": "tok-1"},
	}
	r := &mcp.Resolver{Servers: d, AllowedHosts: []string{"*"}, AllowPrivateNetworks: true, CacheTTL: 1} // a 1ns cache: every Resolve lists again

	for range 3 {
		if _, ok, err := r.Resolve(context.Background(), uuid.New(), uuid.New(), refFor("gh", issueTool)); err != nil || !ok {
			t.Fatalf("Resolve = %v, %v", ok, err)
		}
	}
	if n := srv.handshakes.Load(); n != 1 {
		t.Fatalf("%d handshakes for three resolutions under one credential, want 1", n)
	}
}
