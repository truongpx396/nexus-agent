//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/store"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

func deliveryTestSession(t *testing.T) (*store.Store, uuid.UUID, uuid.UUID) {
	t.Helper()
	s := store.New(setupInboundDeliveryEnv(t))
	tenantID, sessionID := uuid.New(), uuid.New()
	if err := s.InTenantTx(context.Background(), tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (tenant_id, name) VALUES ($1, 'delivery-test')`, tenantID); err != nil {
			return err
		}
		return store.CreateSession(ctx, tx, eventlog.Session{
			SessionID: sessionID, SessionKey: "telegram:1", TenantID: tenantID, SurfaceID: "telegram",
			UserID: uuid.New(), AgentID: uuid.New(), AgentVersion: 1, HarnessDigest: []byte("d"),
			DataLabel: "internal", RouteModelID: "m", RouteReason: map[string]string{},
		})
	}); err != nil {
		t.Fatalf("seed tenant and session: %v", err)
	}
	return s, tenantID, sessionID
}

func inTenant(t *testing.T, s *store.Store, tenantID uuid.UUID, fn func(context.Context, pgx.Tx) error) {
	t.Helper()
	if err := s.InTenantTx(context.Background(), tenantID, fn); err != nil {
		t.Fatal(err)
	}
}

// A delivery is retried only when it is due, is taken by one pass at a time, and
// a failed attempt moves its next try out: this is what makes the outbox
// at-least-once without sending a reply twice at once.
func TestClaimDueDeliveries_LeasesWhatIsDueAndNothingElse(t *testing.T) {
	s, tenantID, sessionID := deliveryTestSession(t)
	var id uuid.UUID
	inTenant(t, s, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		d, _, err := openOrFindDelivery(ctx, tx, tenantID, sessionID, 7, "telegram", "1")
		id = d.DeliveryID
		return err
	})

	claim := func() []surfaces.DueDelivery {
		var due []surfaces.DueDelivery
		inTenant(t, s, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			due, err = claimDueDeliveries(ctx, tx, 10)
			return err
		})
		return due
	}

	// Just opened: its first attempt is still in flight, so it is not due.
	if due := claim(); len(due) != 0 {
		t.Fatalf("a delivery opened a moment ago was claimed: %+v", due)
	}

	// The process died before the attempt finished: once the minute lapses the
	// delivery is due, and exactly one pass gets it.
	inTenant(t, s, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() - interval '1 second' WHERE delivery_id = $1`, id)
		return err
	})
	due := claim()
	if len(due) != 1 || due[0].ID != id || due[0].Seq != 7 || due[0].Recipient != "1" || due[0].SurfaceID != "telegram" {
		t.Fatalf("claimed = %+v, want the one overdue delivery", due)
	}
	if again := claim(); len(again) != 0 {
		t.Fatalf("a leased delivery was claimed by a second pass: %+v", again)
	}

	// A failed attempt moves the next try out by a backoff; a delivered one is
	// never due again.
	inTenant(t, s, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := updateDeliveryOutcome(ctx, tx, id, surfaces.DeliveryFailed); err != nil {
			return err
		}
		var wait float64
		if err := tx.QueryRow(ctx, `SELECT extract(epoch FROM next_attempt_at - now()) FROM deliveries WHERE delivery_id = $1`, id).Scan(&wait); err != nil {
			return err
		}
		if wait < 25 || wait > 40 {
			t.Errorf("first retry is due in %.0f s, want about 30", wait)
		}
		_, err := updateDeliveryOutcome(ctx, tx, id, surfaces.DeliveryDelivered)
		return err
	})
	inTenant(t, s, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() - interval '1 hour'`)
		return err
	})
	if due := claim(); len(due) != 0 {
		t.Fatalf("a delivered delivery was claimed: %+v", due)
	}
}

func TestClaimDueDeliveries_TwoPassesNeverTakeTheSameRow(t *testing.T) {
	s, tenantID, sessionID := deliveryTestSession(t)
	inTenant(t, s, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for seq := int64(1); seq <= 6; seq++ {
			if _, _, err := openOrFindDelivery(ctx, tx, tenantID, sessionID, seq, "telegram", "1"); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() - interval '1 second'`)
		return err
	})

	seen := map[int64]int{}
	results := make(chan []surfaces.DueDelivery, 2)
	for range 2 {
		go func() {
			var due []surfaces.DueDelivery
			if err := s.InTenantTx(context.Background(), tenantID, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				due, err = claimDueDeliveries(ctx, tx, 3)
				return err
			}); err != nil {
				t.Error(err)
			}
			results <- due
		}()
	}
	for range 2 {
		for _, d := range <-results {
			seen[d.Seq]++
		}
	}
	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("delivery %d was taken %d times", seq, n)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("two passes of 3 took %d distinct deliveries, want 6", len(seen))
	}
}

// A dispatch that failed gives its claim back, so the provider's retry of the
// same delivery id is claimed and processed.
func TestDedupRelease_LetsTheProvidersRetryBeClaimedAgain(t *testing.T) {
	s, tenantID, _ := deliveryTestSession(t)
	d := NewDedup(s)
	ctx := context.Background()

	claim := func() bool {
		ok, err := d.Claim(ctx, tenantID, "telegram", "update-9", "telegram:1")
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		return ok
	}
	if !claim() {
		t.Fatal("first claim refused")
	}
	if claim() {
		t.Fatal("a repeat claim succeeded before the first was released")
	}
	if err := d.Release(ctx, tenantID, "telegram", "update-9"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !claim() {
		t.Fatal("the retry of a released delivery was refused as a duplicate")
	}
	if err := d.Release(ctx, tenantID, "telegram", ""); err != nil {
		t.Fatalf("releasing an id-less delivery: %v", err)
	}
}
