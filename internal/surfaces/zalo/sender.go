package zalo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// NewSender returns the surfaces.SenderFor of the Zalo surface.
func NewSender(channels ChannelPort, client *http.Client) surfaces.SenderFor {
	return func(tenantID uuid.UUID) surfaces.Sender {
		return &Sender{Channels: channels, TenantID: tenantID, Client: client}
	}
}

// Sender implements surfaces.Sender by POSTing to Zalo OA's send-message
// API, using the tenant's own current access token — resolved per send,
// constructed per-delivery (webhook.go's own doc comment on why).
type Sender struct {
	Channels ChannelPort
	TenantID uuid.UUID
	Client   *http.Client
}

// Send implements surfaces.Sender. recipient is the Zalo user id
// (webhook.go's own recipientID).
func (s *Sender) Send(ctx context.Context, surfaceID, recipient string, payload []byte) error {
	token, err := s.Channels.AccessToken(ctx, s.TenantID)
	if err != nil {
		return &surfaces.SendError{Reason: "access token unavailable", Err: err}
	}

	text := surfaces.Render(payload)

	reqBody, err := json.Marshal(map[string]any{
		"recipient": map[string]string{"user_id": recipient},
		"message":   map[string]string{"text": text},
	})
	if err != nil {
		return fmt.Errorf("zalo: marshal send body: %w", err)
	}
	const sendURL = "https://openapi.zalo.me/v3.0/oa/message/cs"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("zalo: build send request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("access_token", token)

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		reason := "request failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		}
		return &surfaces.SendError{Reason: reason, Err: err}
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response body; nothing to flush
	if resp.StatusCode >= 300 {
		return &surfaces.SendError{Reason: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	return nil
}
