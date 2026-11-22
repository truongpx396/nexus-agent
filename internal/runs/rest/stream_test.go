package rest

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/runs"
	restkit "github.com/truongpx396/nexus-agent/internal/surfaces/rest"
	"github.com/truongpx396/nexus-agent/internal/surfaces/rest/resttest"
)

// A recorder holds everything until the handler returns, so it cannot show that
// the SSE route streams. This test runs the route over a real connection: the
// client gets the replayed frame while the handler is still blocked waiting for
// more (client.Do returns only once the response headers reach it, and they
// leave with the first flush), then gets a live event published afterwards, and
// the stream ends on the terminal one.
func TestEvents_FlushesEachFrameToTheClientAsItIsWritten(t *testing.T) {
	tenantID, userID, runID := uuid.New(), uuid.New(), uuid.New()
	ev := func(seq int64, typ eventlog.EventType) eventlog.Event {
		return eventlog.Event{EventID: uuid.New(), SessionID: runID, TenantID: tenantID, Seq: seq, Type: typ, KeyID: "k", Actor: eventlog.ActorSystem, CreatedAt: time.Now()}
	}
	r := &fakeReader{
		run:    runs.RunView{RunID: runID, UserID: userID},
		events: []eventlog.Event{ev(1, eventlog.EventUserMessage)}, // not terminal: the handler stays open
	}
	h := NewHandler(nil, r)
	srv := httptest.NewServer(restkit.NewServer(resttest.Verifier{TenantID: tenantID, UserID: userID}, h).Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/runs/"+runID.String()+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+resttest.Token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("no response headers while the run is still open: the first frame was not flushed (%v)", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}

	in := bufio.NewReader(resp.Body)
	frame := func() string {
		t.Helper()
		var b strings.Builder
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended before a whole frame (%q so far): %v", b.String(), err)
			}
			if line == "\n" {
				return b.String()
			}
			b.WriteString(line)
		}
	}

	if first := frame(); !strings.HasPrefix(first, "id: 1\nevent: user_message\ndata: ") {
		t.Fatalf("first frame = %q, want the replayed user_message", first)
	}

	// The subscription was made before the first flush, so this live publish cannot be missed.
	h.PublishEvent(tenantID, runID, ev(2, eventlog.EventTerminal), nil)
	if last := frame(); !strings.HasPrefix(last, "id: 2\nevent: terminal\ndata: ") {
		t.Fatalf("live frame = %q, want the terminal event", last)
	}
	if rest, err := in.ReadString('\n'); err == nil || rest != "" {
		t.Errorf("stream continued past the terminal event with %q (%v), want it closed", rest, err)
	}
}

// syncReader lets a test change what the store holds while a stream is open.
type syncReader struct {
	*fakeReader
	mu sync.Mutex
}

func (r *syncReader) Events(ctx context.Context, tenantID, runID uuid.UUID) ([]eventlog.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fakeReader.Events(ctx, tenantID, runID)
}

func (r *syncReader) set(events ...eventlog.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = events
}

type streamRig struct {
	tenantID, runID uuid.UUID
	reader          *syncReader
	h               *Handler
	srv             *httptest.Server
}

func newStreamRig(t *testing.T) *streamRig {
	t.Helper()
	tenantID, userID, runID := uuid.New(), uuid.New(), uuid.New()
	r := &syncReader{fakeReader: &fakeReader{run: runs.RunView{RunID: runID, UserID: userID}}}
	h := NewHandler(nil, r)
	srv := httptest.NewServer(restkit.NewServer(resttest.Verifier{TenantID: tenantID, UserID: userID}, h).Handler())
	t.Cleanup(srv.Close)
	return &streamRig{tenantID: tenantID, runID: runID, reader: r, h: h, srv: srv}
}

func (s *streamRig) event(seq int64, typ eventlog.EventType) eventlog.Event {
	return eventlog.Event{EventID: uuid.New(), SessionID: s.runID, TenantID: s.tenantID, Seq: seq, Type: typ, KeyID: "k", Actor: eventlog.ActorSystem, CreatedAt: time.Now()}
}

func (s *streamRig) open(t *testing.T, header map[string]string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.srv.URL+"/v1/runs/"+s.runID.String()+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+resttest.Token)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := s.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ids returns the `id:` lines of a whole stream, which ends at the terminal event.
func ids(t *testing.T, resp *http.Response) []string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	var out []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "id: "); ok {
			out = append(out, v)
		}
	}
	return out
}

// The error frame carries a fixed message. The text of the failure (a database
// error, say) is logged, and never sent to a client.
func TestEvents_AnErrorFrameNeverCarriesTheCause(t *testing.T) {
	rig := newStreamRig(t)
	rig.reader.eventErr = errors.New("pq: password authentication failed for user nexus_app")

	raw, err := io.ReadAll(rig.open(t, nil).Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "event: error") || !strings.Contains(string(raw), "stream error") {
		t.Fatalf("stream = %q, want an error frame with the fixed message", raw)
	}
	if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "pq:") {
		t.Fatalf("the error frame leaked the cause: %q", raw)
	}
}

// A client that reconnects sends the id of the last frame it saw and is sent only
// what follows it.
func TestEvents_LastEventIDReplaysOnlyLaterEvents(t *testing.T) {
	rig := newStreamRig(t)
	rig.reader.set(rig.event(1, eventlog.EventUserMessage), rig.event(2, eventlog.EventContent), rig.event(3, eventlog.EventContent),
		rig.event(4, eventlog.EventContent), rig.event(5, eventlog.EventTerminal))

	if got := ids(t, rig.open(t, nil)); !slices.Equal(got, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("a fresh stream carried ids %v, want 1 to 5", got)
	}
	if got := ids(t, rig.open(t, map[string]string{"Last-Event-ID": "3"})); !slices.Equal(got, []string{"4", "5"}) {
		t.Fatalf("a stream resumed after id 3 carried ids %v, want only 4 and 5", got)
	}
}

// The live feed drops messages under pressure. When a seq is skipped the stream
// reads the missing events from the store and sends them in order.
func TestEvents_AGapInTheLiveFeedIsFilledFromTheStore(t *testing.T) {
	rig := newStreamRig(t)
	rig.reader.set(rig.event(1, eventlog.EventUserMessage))
	resp := rig.open(t, nil)

	// The store has 2 and 3 by now, but the feed only delivers 3.
	third := rig.event(3, eventlog.EventTerminal)
	rig.reader.set(rig.event(1, eventlog.EventUserMessage), rig.event(2, eventlog.EventContent), third)
	rig.h.PublishEvent(rig.tenantID, rig.runID, third, nil)

	if got := ids(t, resp); !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Fatalf("stream carried ids %v, want the skipped 2 filled in before 3", got)
	}
}

// A terminal event the feed dropped entirely would leave the stream open for ever.
// The keepalive tick checks the store, and writes a comment so a proxy does not cut
// an idle stream.
func TestEvents_AKeepaliveRecoversADroppedTerminalEvent(t *testing.T) {
	rig := newStreamRig(t)
	rig.h.KeepAlive = 40 * time.Millisecond
	rig.reader.set(rig.event(1, eventlog.EventUserMessage))
	resp := rig.open(t, nil)

	time.Sleep(120 * time.Millisecond)
	rig.reader.set(rig.event(1, eventlog.EventUserMessage), rig.event(2, eventlog.EventTerminal)) // nothing is published

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), ": keepalive") {
		t.Errorf("an idle stream wrote no keepalive comment: %q", raw)
	}
	if !strings.Contains(string(raw), "event: terminal") {
		t.Fatalf("the dropped terminal event was never delivered: %q", raw)
	}
}

// http.Server.Shutdown does not cancel a handler's context, so the handler listens
// for the server stopping.
func TestEvents_AStreamEndsWhenTheServerShutsDown(t *testing.T) {
	rig := newStreamRig(t)
	shutting := make(chan struct{})
	rig.h.ShuttingDown = shutting
	rig.reader.set(rig.event(1, eventlog.EventUserMessage))
	resp := rig.open(t, nil)

	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(done) }()
	close(shutting)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived the server's shutdown signal")
	}
}
