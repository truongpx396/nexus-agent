package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// maxMessageRunes is the longest text the Bot API accepts in one message; a
// longer reply is sent as several, in order.
const maxMessageRunes = 4096

// maxDescriptionRunes bounds how much of Telegram's own explanation of a
// refusal is kept in a delivery's recorded reason.
const maxDescriptionRunes = 200

// Sender implements surfaces.Sender by POSTing to Telegram's Bot API
// sendMessage endpoint, using the tenant's own sealed bot token — resolved
// per send, never cached across sends. surfaces.Sender's interface carries no
// tenant parameter, so one Sender serves one tenant.
type Sender struct {
	Channels ChannelPort
	TenantID uuid.UUID
	Client   *http.Client
}

// NewSender returns the surfaces.SenderFor of the Telegram surface.
func NewSender(channels ChannelPort, client *http.Client) surfaces.SenderFor {
	return func(tenantID uuid.UUID) surfaces.Sender {
		return &Sender{Channels: channels, TenantID: tenantID, Client: client}
	}
}

// Send implements surfaces.Sender. recipient is the Telegram chat_id; payload is
// a surfaces.Notification, rendered as the text of one or more messages.
//
// A failure is returned as a *surfaces.SendError whose Reason is safe to record
// and to show to the session's audience. The request URL carries the bot token,
// so no error that leaves this function names it: Go's transport errors quote
// the URL they were dialling.
func (s *Sender) Send(ctx context.Context, _, recipient string, payload []byte) error {
	token, err := s.Channels.BotToken(ctx, s.TenantID)
	if err != nil {
		return &surfaces.SendError{Reason: "bot token unavailable", Err: err}
	}
	for _, chunk := range splitMessage(surfaces.Render(payload), maxMessageRunes) {
		if err := s.sendChunk(ctx, token, recipient, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) sendChunk(ctx context.Context, token, chatID, text string) error {
	reqBody, err := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
	if err != nil {
		return &surfaces.SendError{Reason: "request not built", Err: err}
	}
	endpoint := "https://api.telegram.org/bot" + token + "/sendMessage"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return &surfaces.SendError{Reason: "request not built", Err: redact(err, token)}
	}
	httpReq.Header.Set("content-type", "application/json")

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		reason := "request failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		} else if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
			reason = "timeout"
		}
		return &surfaces.SendError{Reason: reason, Err: redact(err, token)}
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response body; nothing to flush
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &surfaces.SendError{Reason: statusReason(resp.StatusCode, raw, token)}
	}
	return nil
}

// statusReason says what a non-2xx answer was: its status and, when Telegram
// gave one, why ("message is too long", "chat not found").
func statusReason(status int, body []byte, token string) string {
	reason := fmt.Sprintf("status %d", status)
	var e struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(body, &e) == nil && e.Description != "" {
		d := strings.ReplaceAll(e.Description, token, "<redacted>")
		if utf8.RuneCountInString(d) > maxDescriptionRunes {
			d = string([]rune(d)[:maxDescriptionRunes])
		}
		reason += ": " + d
	}
	return reason
}

// redact returns err without the request URL and without the token, whatever
// shape the transport gave the failure: a *url.Error is reduced to its cause,
// and any remaining occurrence of the token is masked.
func redact(err error, token string) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		err = ue.Err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<redacted>"))
}

// splitMessage cuts text into pieces of at most limit runes, ending a piece at
// a line break or a space when there is one in its second half, so a reply is
// not cut in the middle of a word.
func splitMessage(text string, limit int) []string {
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	var out []string
	for len(runes) > limit {
		cut := limit
		for i := limit; i > limit/2; i-- {
			if runes[i-1] == '\n' || runes[i-1] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}
