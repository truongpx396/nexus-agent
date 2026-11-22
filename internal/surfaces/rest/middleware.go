package rest

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// requestIDKey is where the request id is stored on the Gin context.
const requestIDKey = "nexus.request_id"

// requestIDHeader carries the id in and out.
const requestIDHeader = "X-Request-Id"

// wellFormedID accepts an id a caller or a proxy supplied: short, and nothing that
// could forge a log line.
var wellFormedID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestID is the id of the request c is serving, for a handler that logs.
func RequestID(c *gin.Context) string { return c.GetString(requestIDKey) }

// requestID takes X-Request-Id from the request when it is well formed and makes
// one otherwise, echoes it on the response, and keeps it for the log lines this
// request writes.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if !wellFormedID.MatchString(id) {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Header(requestIDHeader, id)
		logger := log.With().Str("request_id", id).Logger()
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context()))
		c.Next()
	}
}

// recovery turns a panic in a handler into a logged 500 and keeps the server
// serving. Left to net/http, a panic writes its stack to stderr outside the
// structured log and drops the connection without a response.
func recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler { //nolint:errorlint // net/http documents this sentinel as the panic value, compared by identity
				panic(rec) // the deliberate way for a handler to abort a response
			}
			log.Error().Str("request_id", RequestID(c)).Str("method", c.Request.Method).Str("route", c.FullPath()).
				Str("panic", fmt.Sprint(rec)).Bytes("stack", debug.Stack()).Msg("rest: handler panicked")
			if c.Writer.Written() {
				c.Abort()
				return
			}
			Error(c, "internal error", http.StatusInternalServerError)
		}()
		c.Next()
	}
}

// accessLog writes one line per request: method, route template, status, duration,
// tenant and request id. The route is the template ("/v1/runs/:id"), not the path,
// so the log and the metrics agree and carry no identifiers; there is no query
// string and no body, because telemetry is content-free.
func accessLog(m *RouteMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		d := time.Since(start)
		status := c.Writer.Status()
		m.observe(c.Request.Method, route, status, d)

		ev := log.Info()
		if status >= http.StatusInternalServerError {
			ev = log.Warn()
		}
		ev = ev.Str("request_id", RequestID(c)).Str("method", c.Request.Method).Str("route", route).Int("status", status).Dur("duration", d)
		if tenantID, _, ok := principalFrom(c); ok {
			ev = ev.Str("tenant_id", tenantID.String())
		}
		ev.Msg("rest: request")
	}
}

// RouteMetrics counts requests and times them per method, route template and
// status, for /metrics. It is safe for concurrent use; a nil *RouteMetrics records
// nothing.
type RouteMetrics struct {
	mu   sync.Mutex
	rows map[routeKey]*routeStat
}

type routeKey struct {
	method, route string
	status        int
}

type routeStat struct {
	count   uint64
	sum     float64
	buckets [len(latencyBuckets)]uint64 // cumulative: the count of requests at or under each bound
}

// latencyBuckets are the histogram's upper bounds in seconds.
var latencyBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// NewRouteMetrics returns an empty RouteMetrics.
func NewRouteMetrics() *RouteMetrics { return &RouteMetrics{rows: map[routeKey]*routeStat{}} }

func (m *RouteMetrics) observe(method, route string, status int, d time.Duration) {
	if m == nil {
		return
	}
	secs := d.Seconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	k := routeKey{method, route, status}
	st := m.rows[k]
	if st == nil {
		st = &routeStat{}
		m.rows[k] = st
	}
	st.count++
	st.sum += secs
	for i, le := range latencyBuckets {
		if secs <= le {
			st.buckets[i]++
		}
	}
}

// WritePrometheus writes the counters and the latency histogram in Prometheus text
// exposition format.
func (m *RouteMetrics) WritePrometheus(w io.Writer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	keys := make([]routeKey, 0, len(m.rows))
	for k := range m.rows {
		keys = append(keys, k)
	}
	snapshot := make(map[routeKey]routeStat, len(m.rows))
	for k, v := range m.rows {
		snapshot[k] = *v
	}
	m.mu.Unlock()
	slices.SortFunc(keys, func(a, b routeKey) int {
		return strings.Compare(fmt.Sprintf("%s %s %d", a.route, a.method, a.status), fmt.Sprintf("%s %s %d", b.route, b.method, b.status))
	})

	_, _ = fmt.Fprint(w, "# HELP nexus_http_requests_total Requests served, by method, route template and status.\n# TYPE nexus_http_requests_total counter\n")
	for _, k := range keys {
		_, _ = fmt.Fprintf(w, "nexus_http_requests_total{method=%q,route=%q,status=\"%d\"} %d\n", k.method, k.route, k.status, snapshot[k].count)
	}
	_, _ = fmt.Fprint(w, "# HELP nexus_http_request_duration_seconds Request latency, by method and route template.\n# TYPE nexus_http_request_duration_seconds histogram\n")
	for _, k := range keys {
		st := snapshot[k]
		for i, le := range latencyBuckets {
			_, _ = fmt.Fprintf(w, "nexus_http_request_duration_seconds_bucket{method=%q,route=%q,status=\"%d\",le=\"%g\"} %d\n", k.method, k.route, k.status, le, st.buckets[i])
		}
		_, _ = fmt.Fprintf(w, "nexus_http_request_duration_seconds_bucket{method=%q,route=%q,status=\"%d\",le=\"+Inf\"} %d\n", k.method, k.route, k.status, st.count)
		_, _ = fmt.Fprintf(w, "nexus_http_request_duration_seconds_sum{method=%q,route=%q,status=\"%d\"} %g\n", k.method, k.route, k.status, st.sum)
		_, _ = fmt.Fprintf(w, "nexus_http_request_duration_seconds_count{method=%q,route=%q,status=\"%d\"} %d\n", k.method, k.route, k.status, st.count)
	}
}
