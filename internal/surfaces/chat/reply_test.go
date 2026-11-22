package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/runs"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
)

// fakeReader serves one session and its events, counting session reads so a
// test can show that an event never delivered costs no read at all.
type fakeReader struct {
	run     runs.RunView
	events  []eventlog.Event
	getRuns int
}

func (f *fakeReader) GetRun(context.Context, uuid.UUID, uuid.UUID) (runs.RunView, error) {
	f.getRuns++
	return f.run, nil
}
func (f *fakeReader) ListRuns(context.Context, uuid.UUID, uuid.UUID, int) ([]runs.RunView, error) {
	return nil, nil
}
func (f *fakeReader) FindByKey(context.Context, uuid.UUID, string) (runs.RunView, bool, error) {
	return runs.RunView{}, false, nil
}
func (f *fakeReader) Events(context.Context, uuid.UUID, uuid.UUID) ([]eventlog.Event, error) {
	return f.events, nil
}
func (f *fakeReader) EventOpener(uuid.UUID) runs.EventOpener { return plainOpener{} }

// plainOpener treats a payload as already plain, which is all these tests need.
type plainOpener struct{}

func (plainOpener) Open(_ context.Context, e eventlog.Event) ([]byte, error) { return e.Payload, nil }

type sentTo struct{ surface, recipient, text string }

type captureSender struct{ sent *[]sentTo }

func (c captureSender) Send(_ context.Context, surfaceID, recipient string, payload []byte) error {
	*c.sent = append(*c.sent, sentTo{surfaceID, recipient, surfaces.Render(payload)})
	return nil
}

func contentEvent(seq int64, body string) eventlog.Event {
	raw, _ := json.Marshal(map[string]string{"body": body})
	return eventlog.Event{Seq: seq, Type: eventlog.EventContent, Payload: raw}
}

func newReplier(reader *fakeReader, ledger *fakeLedger, sent *[]sentTo) *chat.Replier {
	return &chat.Replier{
		Reader: reader,
		Outbox: &surfaces.Outbox{Ledger: ledger},
		Senders: map[string]surfaces.SenderFor{
			"telegram": func(uuid.UUID) surfaces.Sender { return captureSender{sent} },
		},
	}
}

// A reply the worker appends is sent to the person the session's key names, from
// the worker's own process, with no subscription anywhere.
func TestReplier_OnEvent_SendsAReplyToTheKeysRecipient(t *testing.T) {
	var sent []sentTo
	reader := &fakeReader{run: runs.RunView{RunID: uuid.New(), SurfaceID: "telegram", SessionKey: "telegram:999"}}
	r := newReplier(reader, &fakeLedger{delivery: surfaces.Delivery{ID: uuid.New(), Status: surfaces.DeliveryPending}}, &sent)

	r.OnEvent(context.Background(), uuid.New(), reader.run.RunID, contentEvent(7, "here is my reply"))

	if len(sent) != 1 || sent[0] != (sentTo{"telegram", "999", "here is my reply"}) {
		t.Fatalf("sent = %+v, want one reply to chat 999", sent)
	}
}

func TestReplier_OnEvent_LeavesEveryOtherEventAndSurfaceAlone(t *testing.T) {
	var sent []sentTo
	reader := &fakeReader{run: runs.RunView{RunID: uuid.New(), SurfaceID: "telegram", SessionKey: "telegram:999"}}
	r := newReplier(reader, &fakeLedger{delivery: surfaces.Delivery{ID: uuid.New(), Status: surfaces.DeliveryPending}}, &sent)

	r.OnEvent(context.Background(), uuid.New(), reader.run.RunID, eventlog.Event{Seq: 1, Type: eventlog.EventUserMessage})
	if reader.getRuns != 0 {
		t.Fatalf("an event that is never delivered cost %d session reads, want none", reader.getRuns)
	}

	reader.run.SurfaceID, reader.run.SessionKey = "rest", "rest:abc"
	r.OnEvent(context.Background(), uuid.New(), reader.run.RunID, contentEvent(2, "not for a chat"))
	if len(sent) != 0 {
		t.Fatalf("a session of a surface with no sender was sent %+v", sent)
	}
}

// A delivery that failed, or was opened and never attempted, is sent again by the
// retry pass from the event it was opened for.
func TestReplier_RetryDue_SendsWhatAnEarlierAttemptLeft(t *testing.T) {
	var sent []sentTo
	sessionID := uuid.New()
	reader := &fakeReader{
		run:    runs.RunView{RunID: sessionID, SurfaceID: "telegram", SessionKey: "telegram:999"},
		events: []eventlog.Event{contentEvent(3, "first"), contentEvent(7, "the one owed")},
	}
	ledger := &fakeLedger{
		delivery: surfaces.Delivery{ID: uuid.New(), Status: surfaces.DeliveryFailed, AttemptCount: 1},
		due:      []surfaces.DueDelivery{{ID: uuid.New(), SessionID: sessionID, Seq: 7, SurfaceID: "telegram", Recipient: "999"}},
	}
	r := newReplier(reader, ledger, &sent)

	if err := r.RetryDue(context.Background(), uuid.New()); err != nil {
		t.Fatalf("RetryDue: %v", err)
	}
	if len(sent) != 1 || sent[0] != (sentTo{"telegram", "999", "the one owed"}) {
		t.Fatalf("sent = %+v, want exactly the event the delivery was opened for", sent)
	}
	if len(ledger.recorded) != 1 || ledger.recorded[0].status != surfaces.DeliveryDelivered {
		t.Fatalf("recorded = %+v, want the retry recorded delivered", ledger.recorded)
	}

	// The ledger leased it, so a second pass finds nothing to send.
	if err := r.RetryDue(context.Background(), uuid.New()); err != nil || len(sent) != 1 {
		t.Fatalf("second pass: err %v, sent %d, want no further send", err, len(sent))
	}
}

// fakeLedger answers Open from a field, leases what is due once, and records
// what Record is told, so these tests pin the replier's own policy apart from
// what the Postgres ledger stores.
type fakeLedger struct {
	due      []surfaces.DueDelivery
	delivery surfaces.Delivery
	recorded []recorded
}

type recorded struct {
	status surfaces.DeliveryStatus
	reason string
}

func (f *fakeLedger) Open(context.Context, uuid.UUID, uuid.UUID, int64, string, string) (surfaces.Delivery, error) {
	return f.delivery, nil
}

func (f *fakeLedger) Record(_ context.Context, _, _, _ uuid.UUID, status surfaces.DeliveryStatus, reason string) error {
	f.recorded = append(f.recorded, recorded{status, reason})
	return nil
}

func (f *fakeLedger) ClaimDue(context.Context, uuid.UUID, int) ([]surfaces.DueDelivery, error) {
	due := f.due
	f.due = nil // a claimed delivery is leased: the next pass does not see it
	return due, nil
}
