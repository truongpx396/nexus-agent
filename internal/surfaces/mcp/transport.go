package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// protocolVersion is the MCP revision this client speaks.
const protocolVersion = "2025-06-18"

// maxResponseBytes bounds what one response may carry, SSE stream included. A
// server that streams without end must not make the process allocate until it is
// killed.
const maxResponseBytes = 4 << 20

// Client speaks the MCP Streamable HTTP transport to one remote server's endpoint.
// One Client per admitted server and credential: it holds the session the server
// assigned, so the handshake happens once and not before every call.
//
// The transport, as this client follows it:
//   - every request accepts both application/json and text/event-stream, because a
//     server may answer any request with either;
//   - initialize is followed by the notifications/initialized notification;
//   - the Mcp-Session-Id the server assigns on initialize, if any, goes on every
//     later request, with MCP-Protocol-Version;
//   - a 404 to a request that carried a session id means the session ended, and the
//     client starts a new one and retries once.
type Client struct {
	BaseURL     string
	BearerToken string // static token (auth_kind='bearer_static') or a live OAuth access token (auth_kind='oauth_connector'), injected by the caller — it is never refreshed here
	HTTPClient  *http.Client

	nextID atomic.Int64

	mu          sync.Mutex // guards the session fields below, and serialises the handshake
	initialized bool
	sessionID   string
	protocol    string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"` // absent for a notification
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// Initialize performs the handshake: initialize, then the initialized
// notification. ListTools and CallTool run it when it has not happened, so a caller
// need not; it does nothing once the session is established.
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initializeLocked(ctx)
}

func (c *Client) initializeLocked(ctx context.Context) error {
	if c.initialized {
		return nil
	}
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "nexus-agent", "version": "1"},
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	hdr, err := c.send(ctx, "initialize", params, &result, false, session{})
	if err != nil {
		return err
	}
	c.sessionID = hdr.Get("Mcp-Session-Id")
	c.protocol = result.ProtocolVersion
	if c.protocol == "" {
		c.protocol = protocolVersion
	}
	c.initialized = true

	// A server that rejects the notification is logged, not failed: the notification
	// carries no answer, and a server that needs it says so on the next call.
	if _, err := c.send(ctx, "notifications/initialized", nil, nil, true, session{c.sessionID, c.protocol}); err != nil {
		if _, ok := errors.AsType[*statusError](err); !ok {
			c.initialized, c.sessionID = false, ""
			return err
		}
		log.Debug().Err(err).Str("server", c.BaseURL).Msg("mcp: server rejected notifications/initialized")
	}
	return nil
}

// statusError is a non-2xx answer, which is a refusal and not a result.
type statusError struct {
	method string
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("mcp: %s: server answered http status %d", e.method, e.status)
}

// do sends one request on the established session, starting it first when there is
// none, and starts a new one and retries once if the server says the session ended.
func (c *Client) do(ctx context.Context, method string, params, out any) error {
	sess, err := c.ensureSession(ctx, false)
	if err != nil {
		return err
	}
	_, err = c.send(ctx, method, params, out, false, sess)
	if se, ok := errors.AsType[*statusError](err); ok && se.status == http.StatusNotFound && sess.id != "" {
		// The server ended the session. Start a new one and ask again, once.
		sess, ierr := c.ensureSession(ctx, true)
		if ierr != nil {
			return ierr
		}
		_, err = c.send(ctx, method, params, out, false, sess)
	}
	return err
}

// session is what a request carries to say which session it belongs to.
type session struct{ id, protocol string }

// ensureSession returns the established session, running the handshake first when
// there is none (or when renew is set, to replace one the server ended).
func (c *Client) ensureSession(ctx context.Context, renew bool) (session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if renew {
		c.initialized, c.sessionID = false, ""
	}
	if err := c.initializeLocked(ctx); err != nil {
		return session{}, err
	}
	return session{c.sessionID, c.protocol}, nil
}

// send posts one JSON-RPC message and, unless it is a notification, reads the
// response with the matching id out of a JSON or an event-stream body. It returns the
// response headers.
func (c *Client) send(ctx context.Context, method string, params, out any, notification bool, sess session) (http.Header, error) {
	msg := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	var id int64
	if !notification {
		id = c.nextID.Add(1)
		msg.ID = &id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	if c.BearerToken != "" {
		req.Header.Set("authorization", "Bearer "+c.BearerToken)
	}
	if sess.id != "" {
		req.Header.Set("Mcp-Session-Id", sess.id)
	}
	if sess.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", sess.protocol)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: request %s: %w", method, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response body; nothing to flush
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.Header, &statusError{method: method, status: resp.StatusCode}
	}
	if notification {
		return resp.Header, nil
	}

	raw, err := readResponse(io.LimitReader(resp.Body, maxResponseBytes+1), resp.Header.Get("content-type"), id)
	if err != nil {
		return resp.Header, fmt.Errorf("mcp: %s: %w", method, err)
	}
	if raw.Error != nil {
		return resp.Header, fmt.Errorf("mcp: %s refused: %s (code %d)", method, raw.Error.Message, raw.Error.Code)
	}
	if out == nil {
		return resp.Header, nil
	}
	if err := json.Unmarshal(raw.Result, out); err != nil {
		return resp.Header, fmt.Errorf("mcp: unmarshal result for %s: %w", method, err)
	}
	return resp.Header, nil
}

// readResponse returns the JSON-RPC response with the given id from a body that is
// either one JSON message or a text/event-stream of them. r reads at most
// maxResponseBytes+1 bytes: reaching the extra byte is the signal that the response
// was larger than allowed.
func readResponse(r io.Reader, contentType string, id int64) (rpcResponse, error) {
	counted := &countingReader{r: r}
	mt, _, _ := mime.ParseMediaType(contentType)
	var resp rpcResponse
	var err error
	if mt == "text/event-stream" {
		resp, err = readEventStream(counted, id)
	} else {
		err = json.NewDecoder(counted).Decode(&resp)
		if err != nil {
			err = fmt.Errorf("decode response: %w", err)
		}
	}
	if counted.n > maxResponseBytes {
		return rpcResponse{}, fmt.Errorf("response is larger than the %d byte limit", maxResponseBytes)
	}
	return resp, err
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readEventStream reads server-sent events until the one carrying the response to
// request id. Server-initiated requests and notifications in the same stream are
// skipped: this client declares no capabilities, so there is nothing to answer.
func readEventStream(r io.Reader, id int64) (rpcResponse, error) {
	br := bufio.NewReader(r)
	want := strconv.FormatInt(id, 10)
	var data []string
	flush := func() (rpcResponse, bool) {
		if len(data) == 0 {
			return rpcResponse{}, false
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		var m rpcResponse
		if err := json.Unmarshal([]byte(payload), &m); err != nil || m.Method != "" {
			return rpcResponse{}, false
		}
		if strings.TrimSpace(string(m.ID)) != want {
			return rpcResponse{}, false
		}
		return m, true
	}
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if m, ok := flush(); ok {
				return m, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if err != nil {
			if m, ok := flush(); ok {
				return m, nil
			}
			if errors.Is(err, io.EOF) {
				return rpcResponse{}, errors.New("event stream ended without a response")
			}
			return rpcResponse{}, fmt.Errorf("read event stream: %w", err)
		}
	}
}

// ListTools returns the server's currently-offered tool set.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.do(ctx, "tools/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool invokes name with arguments and returns the server's result.
// The result is untrusted external content by construction — callers never
// feed it back into anything that executes it as instructions.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (CallResult, error) {
	var args any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return CallResult{}, fmt.Errorf("mcp: unmarshal arguments: %w", err)
		}
	} else {
		args = map[string]any{}
	}
	var out CallResult
	if err := c.do(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &out); err != nil {
		return CallResult{}, err
	}
	return out, nil
}
