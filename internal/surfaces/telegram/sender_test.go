package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// roundTripFunc lets a test provide Send's *http.Client with a fake
// transport that captures the outgoing request without any real network
// call — no httptest.Server needed, since Send's target URL
// (api.telegram.org) is a fixed constant, not configurable.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestSender_Send_RendersEachNotificationKind proves the two
// payload kinds (surfaces.Notification) render as the
// right message text — "approval" unchanged from before this feature, and
// the new "content" kind sent verbatim, replacing the pre-continuity
// approach of guessing the kind by attempting to unmarshal into one
// specific shape.
func TestSender_Send_RendersEachNotificationKind(t *testing.T) {
	cases := []struct {
		name    string
		payload surfaces.Notification
		want    string
	}{
		{
			name:    "approval",
			payload: surfaces.Notification{Kind: "approval", SessionID: "s-1", ToolID: "platform/shell@1"},
			want:    "Approval needed for session s-1 (tool: platform/shell@1) — review it in the run's own approval endpoint.",
		},
		{
			name:    "content",
			payload: surfaces.Notification{Kind: "content", Text: "here is my reply"},
			want:    "here is my reply",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]string
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				b, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				if err := json.Unmarshal(b, &gotBody); err != nil {
					t.Fatalf("unmarshal sendMessage body: %v", err)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			})}

			sender := &Sender{Channels: fakeChannels{}, TenantID: uuid.New(), Client: client}
			payload, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			if err := sender.Send(context.Background(), "telegram", "chat-1", payload); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if gotBody["text"] != tc.want {
				t.Fatalf("sent text = %q, want %q", gotBody["text"], tc.want)
			}
			if gotBody["chat_id"] != "chat-1" {
				t.Fatalf("sent chat_id = %q, want %q", gotBody["chat_id"], "chat-1")
			}
		})
	}
}

// The request URL is https://api.telegram.org/bot<token>/sendMessage, and Go's
// transport quotes the URL in the errors it returns. A failure must reach the
// delivery_failed event, which the session's audience can read, without it.
func TestSender_Send_AFailedRequestNeverCarriesTheBotToken(t *testing.T) {
	const token = "123456:AAH-SECRET-TOKEN" //nolint:gosec // a made-up token: the test checks that it never appears in an error
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: req.URL.String(), Err: errors.New("dial tcp: i/o timeout")}
	})}
	sender := &Sender{Channels: fakeChannelsWithToken{token}, TenantID: uuid.New(), Client: client}

	err := sender.Send(context.Background(), "telegram", "chat-1", []byte("hi"))
	if err == nil {
		t.Fatal("Send = nil, want the transport failure")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("the error carries the bot token: %v", err)
	}
	if se, ok := errors.AsType[*surfaces.SendError](err); !ok || strings.Contains(se.Reason, token) {
		t.Fatalf("error = %#v, want a *surfaces.SendError with a reason free of the token", err)
	}
}

func TestSender_Send_ARefusalCarriesTelegramsReasonAndNoBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"Bad Request: chat not found"}`))}, nil
	})}
	sender := &Sender{Channels: fakeChannels{}, TenantID: uuid.New(), Client: client}

	err := sender.Send(context.Background(), "telegram", "chat-1", []byte("hi"))
	se, ok := errors.AsType[*surfaces.SendError](err)
	if !ok || se.Reason != "status 400: Bad Request: chat not found" {
		t.Fatalf("error = %v, want status 400 with Telegram's description", err)
	}
}

// A reply over the 4,096-character limit used to fail with 400 every time.
func TestSender_Send_ALongReplyIsSentAsSeveralMessagesInOrder(t *testing.T) {
	long := strings.Repeat("word ", 2000) // 10,000 characters
	var texts []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]string
		b, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(b, &body)
		texts = append(texts, body["text"])
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}
	sender := &Sender{Channels: fakeChannels{}, TenantID: uuid.New(), Client: client}

	payload, _ := json.Marshal(surfaces.Notification{Kind: "content", Text: long})
	if err := sender.Send(context.Background(), "telegram", "chat-1", payload); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(texts) < 3 {
		t.Fatalf("sent %d messages, want the reply split into at least 3", len(texts))
	}
	for i, m := range texts {
		if n := utf8.RuneCountInString(m); n > maxMessageRunes {
			t.Fatalf("message %d is %d characters, over the %d limit", i, n, maxMessageRunes)
		}
	}
	if strings.Join(texts, "") != long {
		t.Fatal("the pieces do not add up to the reply")
	}
}

func TestSplitMessage_KeepsMultibyteTextWhole(t *testing.T) {
	text := strings.Repeat("xin chào thế giới ", 600)
	got := splitMessage(text, 100)
	if strings.Join(got, "") != text {
		t.Fatal("pieces do not rejoin to the original text")
	}
	for _, p := range got {
		if !utf8.ValidString(p) || utf8.RuneCountInString(p) > 100 {
			t.Fatalf("bad piece %q", p)
		}
	}
}

type fakeChannelsWithToken struct{ token string }

func (fakeChannelsWithToken) WebhookSecret(context.Context, uuid.UUID) (string, bool, error) {
	return "", false, nil
}
func (f fakeChannelsWithToken) BotToken(context.Context, uuid.UUID) (string, error) {
	return f.token, nil
}
