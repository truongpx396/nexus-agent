package email

import (
	"context"
	"encoding/json"
	"fmt"
	"net/smtp"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// NewSender returns the surfaces.SenderFor of the email surface.
func NewSender(channels ChannelPort) surfaces.SenderFor {
	return func(tenantID uuid.UUID) surfaces.Sender {
		return &Sender{Channels: channels, TenantID: tenantID}
	}
}

// Sender implements surfaces.Sender via stdlib net/smtp — no external SMTP
// library dependency, matching this codebase's minimal-dependency style.
// SMTP has no context-cancellation hook of its own; ctx is accepted (the
// interface requires it) but net/smtp.SendMail runs to completion or
// error, uninterruptible mid-call — the same honest limitation any
// stdlib-only SMTP client has.
type Sender struct {
	Channels ChannelPort
	TenantID uuid.UUID
}

// Send implements surfaces.Sender. recipient is the original sender's own
// address (webhook.go's own recipientAddress) — a reply goes back to
// whoever wrote in.
func (s *Sender) Send(ctx context.Context, surfaceID, recipient string, payload []byte) error {
	cfg, err := s.Channels.SMTPConfig(ctx, s.TenantID)
	if err != nil {
		return &surfaces.SendError{Reason: "SMTP config unavailable", Err: err}
	}

	subject, text := renderNotification(payload)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", cfg.FromAddress, recipient, subject, text)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	if err := smtp.SendMail(addr, auth, cfg.FromAddress, []string{recipient}, []byte(msg)); err != nil {
		return &surfaces.SendError{Reason: "smtp send failed", Err: err}
	}
	return nil
}

// renderNotification is Send's own pure discriminator — factored out so it
// can be tested directly: net/smtp.SendMail dials a real connection with
// no client-injection point (unlike telegram/zalo's http.Client field), so
// this is the one piece of Send's logic this package CAN unit-test without
// a live SMTP server.
func renderNotification(payload []byte) (subject, text string) {
	subject = "Approval needed"
	var n surfaces.Notification
	if err := json.Unmarshal(payload, &n); err == nil && n.Kind == surfaces.NotifyContent {
		subject = "Re: your message"
	}
	return subject, surfaces.Render(payload)
}
