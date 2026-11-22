package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
)

type fakeDedup struct {
	claimed  bool
	released []string
}

func (d *fakeDedup) Claim(context.Context, uuid.UUID, string, string, string) (bool, error) {
	return d.claimed, nil
}
func (d *fakeDedup) Release(_ context.Context, _ uuid.UUID, _, deliveryID string) error {
	d.released = append(d.released, deliveryID)
	return nil
}

type fakeLauncher struct {
	launched []runs.LaunchRequest
	err      error
}

func (l *fakeLauncher) Launch(_ context.Context, req runs.LaunchRequest) (runs.Launched, error) {
	l.launched = append(l.launched, req)
	return runs.Launched{SessionID: uuid.New()}, l.err
}

type fakeResumer struct {
	to  []uuid.UUID
	err error
}

func (r *fakeResumer) SendMessage(_ context.Context, _, sessionID uuid.UUID, _ string) error {
	r.to = append(r.to, sessionID)
	return r.err
}

type findReader struct {
	fakeReader
	found  bool
	status string
	id     uuid.UUID
}

func (f *findReader) FindByKey(context.Context, uuid.UUID, string) (runs.RunView, bool, error) {
	return runs.RunView{RunID: f.id, Status: f.status}, f.found, nil
}

type busyLock struct{ held bool }

func (b busyLock) Acquire(context.Context, string) (string, bool, error) { return "t", !b.held, nil }
func (b busyLock) Release(context.Context, string, string) error         { return nil }

func conv(d *fakeDedup, r *findReader, l *fakeLauncher, res runs.Resumer) *chat.Conversation {
	return &chat.Conversation{SurfaceID: "telegram", Reader: r, Dedup: d, Launcher: l, Resume: res}
}

var inbound = chat.Inbound{TenantID: uuid.New(), UserID: uuid.New(), SessionKey: "telegram:1", DeliveryID: "u-1", Text: "hi"}

func TestConversation_ADuplicateDeliveryIsAcknowledgedWithoutDispatching(t *testing.T) {
	l := &fakeLauncher{}
	err := conv(&fakeDedup{claimed: false}, &findReader{}, l, &fakeResumer{}).Dispatch(context.Background(), inbound)
	if err != nil || len(l.launched) != 0 {
		t.Fatalf("err %v, launched %d: a redelivery must be acknowledged and not dispatched", err, len(l.launched))
	}
}

// The claim is the provider-retry dedup. A dispatch that fails with the process
// still up must give it back, or the retry is acknowledged as a duplicate of a
// message no session ever saw.
func TestConversation_AFailedDispatchReleasesItsClaim(t *testing.T) {
	d, l := &fakeDedup{claimed: true}, &fakeLauncher{err: errors.New("database down")}
	if err := conv(d, &findReader{}, l, &fakeResumer{}).Dispatch(context.Background(), inbound); err == nil {
		t.Fatal("Dispatch = nil, want the launch failure")
	}
	if len(d.released) != 1 || d.released[0] != "u-1" {
		t.Fatalf("released = %v, want the delivery's claim given back", d.released)
	}
}

func TestConversation_ASuccessfulDispatchKeepsItsClaim(t *testing.T) {
	d := &fakeDedup{claimed: true}
	if err := conv(d, &findReader{}, &fakeLauncher{}, &fakeResumer{}).Dispatch(context.Background(), inbound); err != nil || len(d.released) != 0 {
		t.Fatalf("err %v, released %v: a dispatched message keeps its claim", err, d.released)
	}
}

func TestConversation_AMessageGoesToTheOpenSessionAndNeverStartsARivalOne(t *testing.T) {
	for _, status := range []string{eventlog.SessionStatusAwaitingInput, eventlog.SessionStatusRunning, eventlog.SessionStatusSuspended, eventlog.SessionStatusQueued} {
		t.Run(status, func(t *testing.T) {
			id, res, l := uuid.New(), &fakeResumer{}, &fakeLauncher{}
			r := &findReader{found: true, status: status, id: id}
			if err := conv(&fakeDedup{claimed: true}, r, l, res).Dispatch(context.Background(), inbound); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if len(res.to) != 1 || res.to[0] != id || len(l.launched) != 0 {
				t.Fatalf("sent to %v, launched %d: want the message delivered to %s and no new session", res.to, len(l.launched), id)
			}
		})
	}
}

func TestConversation_AnEndedSessionGivesWayToANewOne(t *testing.T) {
	cases := map[string]struct {
		status string
		err    error
	}{
		"completed":                   {eventlog.SessionStatusCompleted, nil},
		"failed":                      {eventlog.SessionStatusFailed, nil},
		"ended between look and send": {eventlog.SessionStatusRunning, runs.ErrSessionEnded},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l := &fakeLauncher{}
			r := &findReader{found: true, status: tc.status, id: uuid.New()}
			if err := conv(&fakeDedup{claimed: true}, r, l, &fakeResumer{err: tc.err}).Dispatch(context.Background(), inbound); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if len(l.launched) != 1 || l.launched[0].SessionKey != "telegram:1" || !l.launched[0].Conversational {
				t.Fatalf("launched = %+v, want one conversational session under the same key", l.launched)
			}
		})
	}
}

func TestConversation_ABusyConversationIsRefusedForTheProviderToRetry(t *testing.T) {
	d, l := &fakeDedup{claimed: true}, &fakeLauncher{}
	c := conv(d, &findReader{}, l, &fakeResumer{})
	c.Lock = busyLock{held: true}
	err := c.Dispatch(context.Background(), inbound)
	if !errors.Is(err, chat.ErrBusy) || len(l.launched) != 0 || len(d.released) != 1 {
		t.Fatalf("err %v, launched %d, released %v: want ErrBusy, nothing started and the claim released", err, len(l.launched), d.released)
	}
}
