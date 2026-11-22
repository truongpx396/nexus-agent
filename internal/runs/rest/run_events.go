package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/runs"
	restkit "github.com/truongpx396/nexus-agent/internal/surfaces/rest"
)

// streamErrorMessage is what an error frame says. The cause goes to the log: a
// response never carries an infrastructure error.
const streamErrorMessage = "stream error"

const defaultKeepAlive = 20 * time.Second

// eventsAfterReader is the optional part of a runs.Reader that reads only the events
// after a seq. A reader without it is read in full and filtered.
type eventsAfterReader interface {
	EventsAfter(ctx context.Context, tenantID, runID uuid.UUID, afterSeq int64) ([]eventlog.Event, error)
}

func (h *Handler) eventsAfter(ctx context.Context, tenantID, runID uuid.UUID, afterSeq int64) ([]eventlog.Event, error) {
	if r, ok := h.Reader.(eventsAfterReader); ok {
		return r.EventsAfter(ctx, tenantID, runID, afterSeq)
	}
	all, err := h.Reader.Events(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(e eventlog.Event) bool { return e.Seq <= afterSeq }), nil
}

// StreamRunEvents is GET /v1/runs/{id}/events. An SSE frame's data is a
// RunEvent: enough structure to render a client-side view, plus Body — the
// decrypted payload for the run's own audience (constitution: "audience-gated
// run output" is a distinct signal class from content-free telemetry;
// internal/obs never carries this). EventThought never gets a Body: reasoning
// is "round-tripped, never shown" (internal/provider's doc comment) even to
// the run's own audience.
//
// Each durable frame carries `id: <seq>`, and a client that reconnects with
// Last-Event-ID is sent only the events after it. The stream writes a `: keepalive`
// comment while idle so a proxy does not cut it, re-reads the store when the live
// feed skips a seq or goes quiet (Redis Pub/Sub and the broker both drop messages
// under pressure, and a dropped terminal event would otherwise leave the stream open
// for ever), and ends when the server shuts down.
func (h *Handler) StreamRunEvents(c *gin.Context, id RunID) {
	tenantID, userID, ok := restkit.Principal(c)
	if !ok {
		return
	}
	if _, ok := h.ownedRun(c, tenantID, userID, id); !ok {
		return
	}

	// c.Writer is always a Flusher (gin.ResponseWriter embeds http.Flusher), so
	// there is no "streaming unsupported" case to refuse here.
	w, ctx := c.Writer, c.Request.Context()
	// The server bounds every response's write time; this one is meant to last.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Warn().Err(err).Msg("rest: could not lift the write deadline for an event stream")
	}
	c.Header("content-type", "text/event-stream")
	c.Header("cache-control", "no-cache")
	c.Header("connection", "keep-alive")
	c.Status(http.StatusOK)

	// Subscribe BEFORE replaying history, so nothing published between the
	// subscribe call and the replay finishing is missed. That ordering, on
	// its own, can instead deliver an event TWICE (once via the replay,
	// once live) if it's appended in the window between subscribing and
	// finishing the replay — closed below by tracking the highest seq sent and
	// discarding anything from the live channel at or below it: seq is strictly
	// sequential per session (store.Append), so "already sent" is exactly
	// "seq <= lastSeq".
	ch, unsubscribe := h.broker.subscribe(id)
	defer unsubscribe()

	opener := h.Reader.EventOpener(tenantID)
	logger := zerolog.Ctx(ctx)
	fail := func(cause error, what string) {
		logger.Error().Err(cause).Any("run_id", id).Msg("rest: event stream: " + what)
		writeSSEFrame(w, "error", map[string]string{"error": streamErrorMessage}, 0)
		w.Flush()
	}

	var lastSeq int64
	if v := c.GetHeader("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			lastSeq = n
		}
	}
	// write sends one durable event; it reports false once the stream must end.
	write := func(e eventlog.Event) bool {
		dto, derr := toEventDTO(ctx, opener, e)
		if derr != nil {
			fail(derr, "open an event")
			return false
		}
		writeSSEFrame(w, string(e.Type), dto, e.Seq)
		w.Flush()
		lastSeq = e.Seq
		return e.Type != eventlog.EventTerminal
	}
	// catchUp sends what the store holds after lastSeq: the replay, and the repair for
	// an event the live feed dropped.
	catchUp := func() bool {
		missed, err := h.eventsAfter(ctx, tenantID, id, lastSeq)
		if err != nil {
			fail(err, "read events")
			return false
		}
		for _, e := range missed {
			if !write(e) {
				return false
			}
		}
		return true
	}

	if !catchUp() {
		return
	}

	keepAlive := h.KeepAlive
	if keepAlive <= 0 {
		keepAlive = defaultKeepAlive
	}
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.ShuttingDown:
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			w.Flush()
			if !catchUp() { // a terminal event the live feed dropped ends the stream here
				return
			}
		case p, chOK := <-ch:
			if !chOK {
				return
			}
			if p.Delta != nil {
				// Never replayed, never seq-checked against lastSeq — a
				// live preview has no seq at all (DeltaDTO's own doc
				// comment). The durable event it previews still arrives
				// right behind it on this same channel.
				writeSSEFrame(w, "delta", p.Delta, 0)
				w.Flush()
				continue
			}
			if p.Err != nil {
				fail(p.Err, "the run reported an error")
				return
			}
			if p.Event.Seq <= lastSeq {
				continue // already sent
			}
			if p.Event.Seq > lastSeq+1 {
				// A seq was skipped: read it from the store before sending this one.
				if !catchUp() {
					return
				}
				if p.Event.Seq <= lastSeq {
					continue
				}
			}
			if !write(p.Event) {
				return
			}
		}
	}
}

// toEventDTO decrypts one event's payload for display to the run's own
// audience (the principal check in StreamRunEvents has already established this
// request IS that audience). The opener is per-request, so a key unwrapped for
// one event is reused for the rest of the same session.
func toEventDTO(ctx context.Context, opener runs.EventOpener, e eventlog.Event) (RunEvent, error) {
	dto := RunEvent{
		EventID:   e.EventID,
		Seq:       e.Seq,
		Type:      string(e.Type),
		Actor:     string(e.Actor),
		ToolID:    e.ToolID,
		PairRef:   e.PairRef,
		ModelID:   e.ModelID,
		CreatedAt: e.CreatedAt,
	}
	if e.Type == eventlog.EventThought {
		return dto, nil // never shown, even to the run's own audience
	}

	plaintext, err := opener.Open(ctx, e)
	if err != nil {
		return RunEvent{}, err
	}
	dto.Body = json.RawMessage(plaintext)
	return dto, nil
}

// writeSSEFrame writes one frame. A positive seq becomes its `id:`, which is what a
// reconnecting client sends back as Last-Event-ID; a frame with no seq (a delta or an
// error) has none.
func writeSSEFrame(w io.Writer, event string, data any, seq int64) {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte(`{"error":"failed to marshal event"}`)
	}
	if seq > 0 {
		_, _ = fmt.Fprintf(w, "id: %d\n", seq)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw) // best-effort: a broken client connection is discovered on the next Flush, not here
}
