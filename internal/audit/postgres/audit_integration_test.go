//go:build integration

package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/truongpx396/nexus-agent/internal/audit"
	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/store"
	"github.com/truongpx396/nexus-agent/migrations"
)

// testSigner signs with a key it owns and says so under keyID.
type testSigner struct {
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
	keyID string
}

func newTestSigner(t *testing.T, keyID string) *testSigner {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testSigner{pub: pub, priv: priv, keyID: keyID}
}

func (s *testSigner) Sign(_ context.Context, digest []byte) ([]byte, string, error) {
	return ed25519.Sign(s.priv, digest), s.keyID, nil
}

func (s *testSigner) PublicKey(context.Context) (ed25519.PublicKey, string, error) {
	return s.pub, s.keyID, nil
}

type auditRig struct {
	st       *store.Store
	tenantID uuid.UUID
}

func newAuditRig(t *testing.T) *auditRig {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "pgvector/pgvector:pg17",
			ExposedPorts: []string{"5432/tcp"},
			Env:          map[string]string{"POSTGRES_USER": "nexus", "POSTGRES_PASSWORD": "nexus", "POSTGRES_DB": "nexus"},
			WaitingFor:   wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")

	admin, err := pgxpool.New(ctx, fmt.Sprintf("postgres://nexus:nexus@%s:%s/nexus", host, port.Port()))
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}
	defer admin.Close()
	if _, err := store.Migrate(ctx, admin, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	app, err := pgxpool.New(ctx, fmt.Sprintf("postgres://nexus_app:nexus_app@%s:%s/nexus", host, port.Port()))
	if err != nil {
		t.Fatalf("connect as nexus_app: %v", err)
	}
	t.Cleanup(app.Close)

	r := &auditRig{st: store.New(app), tenantID: uuid.New()}
	r.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tenants (tenant_id, name) VALUES ($1, 'audit-test')`, r.tenantID)
		return err
	})
	return r
}

func (r *auditRig) inTx(t *testing.T, fn func(context.Context, pgx.Tx) error) {
	t.Helper()
	if err := r.st.InTenantTx(context.Background(), r.tenantID, fn); err != nil {
		t.Fatal(err)
	}
}

func (r *auditRig) session(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	r.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		return store.CreateSession(ctx, tx, eventlog.Session{
			SessionID: id, SessionKey: id.String(), TenantID: r.tenantID, SurfaceID: "test", UserID: uuid.New(),
			AgentVersion: 1, HarnessDigest: []byte("d"), DataLabel: "internal", RouteModelID: "m",
		})
	})
	return id
}

// event appends one event and its receipt in one transaction, as the event log does.
func (r *auditRig) event(t *testing.T, chain *Chain, sessionID uuid.UUID) {
	t.Helper()
	r.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		e, err := store.Append(ctx, tx, eventlog.Event{
			EventID: uuid.New(), SessionID: sessionID, TenantID: r.tenantID, SchemaVersion: eventlog.CurrentSchemaVersion,
			Type: eventlog.EventUserMessage, Payload: []byte("sealed"), PayloadDigest: []byte("digest"), KeyID: "k", Actor: eventlog.ActorUser,
		})
		if err != nil {
			return err
		}
		_, err = chain.Append(ctx, tx, r.tenantID, sessionID, e.Seq, e.EventID, string(e.Type), e.PayloadDigest)
		return err
	})
}

func (r *auditRig) verify(t *testing.T, chain *Chain, from Watermark) (audit.Report, Watermark) {
	t.Helper()
	var report audit.Report
	var mark Watermark
	r.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		report, mark, err = chain.VerifyFrom(ctx, tx, r.tenantID, from)
		return err
	})
	return report, mark
}

// Every replica runs the anchor pass. Passes that overlap for one tenant must
// produce one anchor between them, not a fork that the verifier reports as a break.
func TestAnchor_ConcurrentPassesForOneTenantProduceOneAnchor(t *testing.T) {
	r := newAuditRig(t)
	chain := NewChain(newTestSigner(t, "k1"))
	sid := r.session(t)
	for range 3 {
		r.event(t, chain, sid)
	}

	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if err := r.st.InTenantTx(context.Background(), r.tenantID, func(ctx context.Context, tx pgx.Tx) error {
				_, _, err := chain.Anchor(ctx, tx, r.tenantID)
				return err
			}); err != nil {
				t.Errorf("Anchor: %v", err)
			}
		})
	}
	wg.Wait()

	var n int
	r.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_anchors WHERE tenant_id = $1`, r.tenantID).Scan(&n)
	})
	if n != 1 {
		t.Fatalf("6 concurrent passes wrote %d anchors, want exactly 1", n)
	}
	if report, _ := r.verify(t, chain, Watermark{}); !report.OK() {
		t.Fatalf("the chain does not verify after concurrent anchoring: %+v", report)
	}
}

// A scheduled pass verifies what is new, and still catches a receipt that was
// altered inside the window it looks at.
func TestVerifyFrom_ChecksOnlyWhatIsNewAndStillCatchesATamper(t *testing.T) {
	old := overlapReceipts
	overlapReceipts = 2
	t.Cleanup(func() { overlapReceipts = old })

	r := newAuditRig(t)
	chain := NewChain(newTestSigner(t, "k1"))
	sid := r.session(t)
	for range 10 {
		r.event(t, chain, sid)
	}

	report, mark := r.verify(t, chain, Watermark{})
	if !report.OK() || report.ReceiptsChecked != 10 {
		t.Fatalf("the first pass checked %d receipts, OK = %v; want all 10 and clean", report.ReceiptsChecked, report.OK())
	}
	for range 3 {
		r.event(t, chain, sid)
	}
	report, mark = r.verify(t, chain, mark)
	if !report.OK() || report.ReceiptsChecked != 3+2 { // the 3 new, and the overlap
		t.Fatalf("the second pass checked %d receipts, want the 3 new and a 2-receipt overlap (5); OK = %v", report.ReceiptsChecked, report.OK())
	}

	// A receipt that does not follow from the chain, written into the window, is
	// caught by the next pass. (The table is append-only, so a forgery is an insert.)
	r.event(t, chain, sid)
	r.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_receipts (receipt_id, tenant_id, session_id, seq, event_id, event_type, payload_digest, prev_hash, hash, signature, signer_key_id)
			SELECT gen_random_uuid(), tenant_id, session_id, seq + 1, gen_random_uuid(), 'user_message', '\x01', '\x02', '\x03', '\x04', 'k1'
			FROM audit_receipts WHERE tenant_id = $1 ORDER BY seq DESC LIMIT 1`, r.tenantID)
		return err
	})
	if report, _ = r.verify(t, chain, mark); report.OK() {
		t.Fatal("a forged receipt inside the window was not reported")
	}
}

// Rotating the signer key must not turn every older receipt into a finding: the
// retired key's public half, on file, still verifies what it signed.
func TestVerify_TrustsRetiredSignerKeysOnFile(t *testing.T) {
	r := newAuditRig(t)
	oldSigner := newTestSigner(t, "key-2025")
	sid := r.session(t)
	for range 3 {
		r.event(t, NewChain(oldSigner), sid)
	}

	rotated := NewChain(newTestSigner(t, "key-2026"))
	report, _ := r.verify(t, rotated, Watermark{})
	if report.OK() || report.Breaks[0].Kind != "unverifiable_signer_key" {
		t.Fatalf("with no retired key on file, report = %+v, want unverifiable_signer_key findings", report)
	}

	rotated.Retired = map[string]ed25519.PublicKey{"key-2025": oldSigner.pub}
	if report, _ = r.verify(t, rotated, Watermark{}); !report.OK() {
		t.Fatalf("with the retired key on file the chain still reports %+v", report)
	}
}
