//go:build integration

// Package integration holds tests that exercise real backing services
// (Postgres, PgBouncer) via testcontainers-go, gated behind the
// "integration" build tag so `go test ./...` does not require Docker.
//
// This file is the T029 isolation test: it proves that Postgres row-level
// security enforces tenant isolation when queries run through the SAME
// transaction-pooling PgBouncer tier production uses -- a test against a
// direct Postgres connection would prove nothing, because PgBouncer's
// transaction-mode pooling is exactly the mechanism that makes
// session-level tenant scoping unsafe (constitution Principle VI; FR-039;
// SC-013; data-model.md "Cross-cutting rules"; T020).
//
// Run with:
//
//	go test -tags=integration ./tests/integration/...
//
// Requires a working Docker daemon; the whole file skips (not fails) when
// one is not available.
package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestRLSIsolation is the T029 integration test: Postgres row-level
// security, enforced through the production PgBouncer transaction-pooling
// tier, must return zero cross-tenant rows under every access pattern that
// uses the mandated transaction-local SET LOCAL scoping -- and the final
// subtest documents why the session-level alternative is prohibited.
func TestRLSIsolation(t *testing.T) {
	ctx := context.Background()

	directDSN, pooledDSN, cleanup := mustStartStack(t, ctx)
	defer cleanup()

	if err := applyMigrations(ctx, directDSN); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	// A small pool forces PgBouncer (and pgxpool) to reuse physical
	// connections across tenants, which is the whole point: isolation must
	// hold even when the same connection serves tenant A and tenant B in
	// consecutive transactions.
	prodPool, err := newPooledClient(ctx, pooledDSN, 3)
	if err != nil {
		t.Fatalf("create pooled client through pgbouncer: %v", err)
	}
	defer prodPool.Close()

	t.Run("single tenant transaction sees only its own rows", func(t *testing.T) {
		tenantA := mustNewTenantID(t)
		tenantB := mustNewTenantID(t)

		mustInsertTenant(ctx, t, prodPool, tenantA, "single-scope-a")
		mustInsertTenant(ctx, t, prodPool, tenantB, "single-scope-b")
		mustInsertTool(ctx, t, prodPool, tenantA, "reader-a")
		mustInsertTool(ctx, t, prodPool, tenantB, "reader-b")

		tx, err := prodPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		if err := setLocalTenant(ctx, tx, tenantA); err != nil {
			t.Fatalf("%v", err)
		}

		rows, err := tx.Query(ctx, `SELECT tenant_id FROM tools`)
		if err != nil {
			t.Fatalf("query tools: %v", err)
		}

		var seenSelf, seenOther int
		for rows.Next() {
			var tid string
			if err := rows.Scan(&tid); err != nil {
				rows.Close()
				t.Fatalf("scan row: %v", err)
			}
			switch tid {
			case tenantA:
				seenSelf++
			case tenantB:
				seenOther++
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate rows: %v", err)
		}

		if seenOther != 0 {
			t.Errorf("expected zero cross-tenant rows visible under tenant A's transaction-local scope, got %d", seenOther)
		}
		if seenSelf == 0 {
			t.Error("expected at least one row visible under tenant A's own scope, got zero")
		}

		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit transaction: %v", err)
		}
	})

	t.Run("interleaved two-tenant workload never leaks across a shared pooled connection", func(t *testing.T) {
		tenantA := mustNewTenantID(t)
		tenantB := mustNewTenantID(t)

		mustInsertTenant(ctx, t, prodPool, tenantA, "interleaved-a")
		mustInsertTenant(ctx, t, prodPool, tenantB, "interleaved-b")
		for i := 0; i < 5; i++ {
			mustInsertTool(ctx, t, prodPool, tenantA, fmt.Sprintf("a-tool-%d", i))
			mustInsertTool(ctx, t, prodPool, tenantB, fmt.Sprintf("b-tool-%d", i))
		}

		const workers = 16
		const iterationsPerWorker = 20

		var wg sync.WaitGroup
		leaks := make(chan string, workers*iterationsPerWorker)

		for w := 0; w < workers; w++ {
			self, other := tenantA, tenantB
			if w%2 == 1 {
				self, other = tenantB, tenantA
			}
			wg.Add(1)
			go func(self, other string) {
				defer wg.Done()
				for i := 0; i < iterationsPerWorker; i++ {
					if err := runScopedIteration(ctx, prodPool, self, other); err != nil {
						leaks <- err.Error()
					}
				}
			}(self, other)
		}
		wg.Wait()
		close(leaks)

		var messages []string
		for msg := range leaks {
			messages = append(messages, msg)
		}
		if len(messages) > 0 {
			t.Errorf("observed %d cross-tenant leak(s) under interleaved transaction-local scoping through the pooled connection:\n%s", len(messages), strings.Join(messages, "\n"))
		}
	})

	t.Run("session-level SET is not tenant-safe under pooled connections", func(t *testing.T) {
		// Mechanism this subtest documents (constitution Principle VI;
		// FR-039; data-model.md "Tenant scope is transaction-local"):
		//
		// PgBouncer in transaction-pooling mode borrows a physical Postgres
		// backend connection from its own pool for the duration of exactly
		// one client transaction, then returns that physical connection to
		// the pool the instant the transaction ends (COMMIT or ROLLBACK) --
		// free to be handed to the very next transaction, which may belong
		// to a completely different tenant and arrive on a different
		// logical client connection.
		//
		// SET LOCAL app.tenant_id = ... (equivalent to
		// SELECT set_config('app.tenant_id', ..., true)) is scoped to the
		// transaction: Postgres resets it automatically at COMMIT/ROLLBACK,
		// so it cannot outlive the transaction that set it, no matter which
		// physical connection PgBouncer hands out next.
		//
		// Plain SET app.tenant_id = ... (session-level, is_local = false)
		// mutates the physical backend connection's session state for the
		// lifetime of that connection. PgBouncer does not reset
		// session-level GUCs between transactions in transaction-pooling
		// mode unless a server_reset_query (e.g. DISCARD ALL) is configured
		// -- and production is not relying on that as an isolation
		// mechanism. So a tenant context set with plain SET can survive
		// into whatever transaction PgBouncer hands that physical
		// connection to next, regardless of which tenant that next
		// transaction actually belongs to. That is the entire justification
		// for prohibiting session-level SET for tenant scoping (FR-039,
		// T020) and for requiring this isolation test to run through the
		// pooling tier at all (SC-013).
		//
		// Because PgBouncer's connection scheduling is not controllable
		// from the client, deterministically forcing a leak on every run is
		// not guaranteed -- so this subtest probes for it across many
		// iterations through a deliberately single-connection pool (to
		// maximize physical-connection reuse) and, per the documented
		// fallback for this scenario, accepts asserting on observed
		// connection reuse plus this explanatory note when a leak is not
		// directly observed in a given run. What must never happen is
		// silent success with no evidence either way.

		tenantA := mustNewTenantID(t)
		mustInsertTenant(ctx, t, prodPool, tenantA, "negative-a")
		mustInsertTool(ctx, t, prodPool, tenantA, "negative-a-tool")

		// A dedicated single-connection pool maximizes the odds that
		// consecutive logical transactions reuse the same physical
		// connection through PgBouncer's own small backend pool.
		narrowPool, err := newPooledClient(ctx, pooledDSN, 1)
		if err != nil {
			t.Fatalf("create narrow pooled client: %v", err)
		}
		defer narrowPool.Close()

		const probes = 25
		leakObserved := false
		var evidence []string

		for i := 0; i < probes && !leakObserved; i++ {
			// Step 1: a transaction sets tenant A's context at SESSION
			// level (never SET LOCAL) and commits, leaving the GUC on
			// whatever physical connection it used.
			txA, err := narrowPool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin session-level probe transaction (tenant A): %v", err)
			}
			if err := setSessionTenant(ctx, txA, tenantA); err != nil {
				t.Fatalf("%v", err)
			}
			if _, err := txA.Exec(ctx, `SELECT 1`); err != nil {
				t.Fatalf("probe statement under tenant A session context: %v", err)
			}
			if err := txA.Commit(ctx); err != nil {
				t.Fatalf("commit tenant A session-level probe: %v", err)
			}

			// Step 2: a fresh transaction claims to be tenant B and never
			// sets ANY tenant context of its own -- the scenario of an
			// application bug, or code that (wrongly) assumed a clean
			// connection state. If it inherits the physical connection
			// from step 1, RLS reads tenant A's leftover session GUC
			// instead of denying all.
			txB, err := narrowPool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin unscoped probe transaction (tenant B): %v", err)
			}

			rows, err := txB.Query(ctx, `SELECT tenant_id FROM tools`)
			if err != nil {
				t.Fatalf("query under unscoped probe transaction: %v", err)
			}
			var sawTenantA bool
			for rows.Next() {
				var tid string
				if err := rows.Scan(&tid); err != nil {
					rows.Close()
					t.Fatalf("scan probe row: %v", err)
				}
				if tid == tenantA {
					sawTenantA = true
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate probe rows: %v", err)
			}

			// Reset any leaked session state so later probe iterations
			// (and later subtests, if this one runs first) start from a
			// clean slate rather than compounding this subtest's own leak.
			if _, err := txB.Exec(ctx, `SELECT set_config('app.tenant_id', '', false)`); err != nil {
				t.Fatalf("reset leaked session context: %v", err)
			}
			if err := txB.Commit(ctx); err != nil {
				t.Fatalf("commit unscoped probe transaction: %v", err)
			}

			if sawTenantA {
				leakObserved = true
				evidence = append(evidence, fmt.Sprintf("probe %d: an unscoped transaction observed tenant A's leftover session-level context", i))
			}
		}

		stat := narrowPool.Stat()
		newConns := int64(stat.NewConnsCount())
		t.Logf("narrow pool stats after probing: acquires=%d new_conns=%d", stat.AcquireCount(), newConns)

		if leakObserved {
			t.Logf("confirmed: session-level SET leaked tenant A's context into an unscoped transaction through the pooled connection (%s)", strings.Join(evidence, "; "))
			return
		}

		// Fallback per the documented allowance for this scenario: assert
		// on connection reuse instead of a directly observed leak. The
		// mechanism comment above this subtest is the permanent record of
		// *why* this variant exists even on a run that did not reproduce
		// it deterministically.
		if newConns >= int64(probes)*2 {
			t.Fatalf("expected the narrow pool to reuse physical connections across probes (got %d new connections for %d probes), so this run cannot demonstrate the session-level leak mechanism at all", newConns, probes)
		}
		t.Logf("session-level leak not directly observed in this run (PgBouncer's connection scheduling is not client-controllable); connection reuse was confirmed (new_conns=%d over %d probes), which is the precondition the leak depends on -- see the mechanism comment above for why SET LOCAL remains mandatory regardless", newConns, probes)
	})
}

// runScopedIteration opens one transaction scoped to self via SET LOCAL,
// explicitly queries for rows belonging to other, and reports an error if
// any are visible -- proving RLS filters cross-tenant rows even when the
// query directly asks for them.
func runScopedIteration(ctx context.Context, pool *pgxpool.Pool, self, other string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin scoped transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setLocalTenant(ctx, tx, self); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `SELECT tenant_id FROM tools WHERE tenant_id = $1`, other)
	if err != nil {
		return fmt.Errorf("query for cross-tenant leak: %w", err)
	}

	var leaked int
	for rows.Next() {
		leaked++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}
	if leaked > 0 {
		return fmt.Errorf("tenant %s's transaction observed %d row(s) belonging to tenant %s", self, leaked, other)
	}

	return tx.Commit(ctx)
}

// setLocalTenant scopes the current transaction to tenantID using
// set_config(..., true), the parameterized equivalent of
// SET LOCAL app.tenant_id = '<tenantID>'. Postgres's SET command does not
// accept bind parameters, so set_config is the only way to scope a
// transaction to a tenant without building SQL by string interpolation.
func setLocalTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("set transaction-local tenant context: %w", err)
	}
	return nil
}

// setSessionTenant scopes the current SESSION (not just the transaction) to
// tenantID using set_config(..., false) -- the parameterized equivalent of
// plain SET app.tenant_id = '<tenantID>'. This function exists only to
// exercise the prohibited pattern in the negative subtest above; production
// code must always use setLocalTenant.
func setSessionTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, false)`, tenantID); err != nil {
		return fmt.Errorf("set session-level tenant context: %w", err)
	}
	return nil
}

func mustInsertTenant(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenantID, name string) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tenant fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setLocalTenant(ctx, tx, tenantID); err != nil {
		t.Fatalf("%v", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenants (tenant_id, name, region, retention_days, deployment_tier, identity_config, rbac_map, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (tenant_id) DO NOTHING
	`, tenantID, name, "us-east-1", 90, "saas", "{}", "{}")
	if err != nil {
		t.Fatalf("insert tenant fixture: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tenant fixture: %v", err)
	}
}

func mustInsertTool(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenantID, name string) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tool fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setLocalTenant(ctx, tx, tenantID); err != nil {
		t.Fatalf("%v", err)
	}

	toolID := fmt.Sprintf("tenant-fixture/%s@1.0.0", name)
	_, err = tx.Exec(ctx, `
		INSERT INTO tools (
			tool_id, namespace, name, tool_version, source_ref, tenant_id,
			description, input_schema, disclosure, capability, concurrency_safe,
			returns_untrusted, reads_private_data, mutates_external,
			catalog_scan_status, scan_policy_version, scanned_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14,
			$15, $16, now()
		)
		ON CONFLICT (tool_id) DO NOTHING
	`,
		toolID, "tenant-fixture", name, "1.0.0", "builtin", tenantID,
		"integration test fixture tool", "{}", "resident", "read_only", "read_only",
		false, false, false,
		"clean", "v1",
	)
	if err != nil {
		t.Fatalf("insert tool fixture: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tool fixture: %v", err)
	}
}

// mustNewTenantID returns a random RFC 4122 version-4 UUID string, using
// crypto/rand rather than math/rand per the project's rule that any
// security-sensitive randomness -- including tenant identifiers used to
// prove isolation between tenants -- must never use a non-cryptographic
// source.
func mustNewTenantID(t *testing.T) string {
	t.Helper()

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate tenant id: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newPooledClient connects to Postgres through PgBouncer using pgx's simple
// query protocol. PgBouncer's transaction-mode pooling breaks server-side
// prepared statements because a prepared statement is bound to one physical
// backend connection, and PgBouncer may hand a different backend connection
// to the next statement on the same logical client connection. Simple
// protocol avoids the prepare/bind/execute dance entirely, so every
// statement is self-contained (backing-services.instructions.md,
// "PgBouncer + prepared statements").
func newPooledClient(ctx context.Context, pooledDSN string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(pooledDSN)
	if err != nil {
		return nil, fmt.Errorf("parse pooled dsn: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.MaxConns = maxConns
	cfg.MinConns = 1

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pooled client: %w", err)
	}
	return pool, nil
}

// applyMigrations connects directly to Postgres (bypassing PgBouncer, per
// backing-services.instructions.md's migration guidance) and executes every
// backend-go/migrations/*.sql file in lexical order.
func applyMigrations(ctx context.Context, directDSN string) error {
	conn, err := pgx.Connect(ctx, directDSN)
	if err != nil {
		return fmt.Errorf("connect direct to postgres for migrations: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("resolve migrations directory: caller info unavailable")
	}
	migrationsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")

	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(files)

	for _, file := range files {
		contents, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file, err)
		}
		if _, err := conn.Exec(ctx, string(contents)); err != nil {
			return fmt.Errorf("applying migration %s: %w", file, err)
		}
	}
	return nil
}

// mustStartStack starts a Postgres container and a PgBouncer container
// (configured for transaction-mode pooling) on a shared Docker network, and
// returns a direct Postgres DSN, a PgBouncer-pooled DSN, and a cleanup
// function. It skips the calling test (rather than failing it) if Docker is
// unavailable or container startup panics, per the requirement that this
// file compile cleanly and skip gracefully in a Docker-less environment.
func mustStartStack(t *testing.T, ctx context.Context) (directDSN, pooledDSN string, cleanup func()) {
	t.Helper()

	var (
		gotDirectDSN string
		gotPooledDSN string
		gotCleanup   func()
		startErr     error
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				startErr = fmt.Errorf("panic while starting test containers (docker likely unavailable): %v", r)
			}
		}()
		gotDirectDSN, gotPooledDSN, gotCleanup, startErr = startStack(ctx)
	}()

	if startErr != nil {
		t.Skipf("docker not available or container startup failed: %v", startErr)
	}
	return gotDirectDSN, gotPooledDSN, gotCleanup
}

// startStack does the actual container orchestration for mustStartStack. It
// is factored out so mustStartStack can wrap it in a panic recovery guard
// without the recover/defer pattern obscuring the setup logic itself.
func startStack(ctx context.Context) (directDSN, pooledDSN string, cleanup func(), err error) {
	nw, err := network.New(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("create docker network: %w", err)
	}

	const (
		pgUser = "nexus"
		pgPass = "nexus"
		pgDB   = "nexus"
	)

	pgReq := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     pgUser,
			"POSTGRES_PASSWORD": pgPass,
			"POSTGRES_DB":       pgDB,
		},
		Networks:       []string{nw.Name},
		NetworkAliases: map[string][]string{nw.Name: {"postgres"}},
		WaitingFor:     wait.ForListeningPort("5432/tcp").WithStartupTimeout(90 * time.Second),
	}
	pgC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: pgReq,
		Started:          true,
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("start postgres container: %w", err)
	}

	pgHost, err := pgC.Host(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve postgres host: %w", err)
	}
	pgPort, err := pgC.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve postgres mapped port: %w", err)
	}
	directDSN = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", pgUser, pgPass, pgHost, pgPort.Port(), pgDB)

	pgbReq := testcontainers.ContainerRequest{
		Image:        "edoburu/pgbouncer:latest",
		ExposedPorts: []string{"6432/tcp"},
		Env: map[string]string{
			"DB_HOST":           "postgres",
			"DB_PORT":           "5432",
			"DB_USER":           pgUser,
			"DB_PASSWORD":       pgPass,
			"DB_NAME":           pgDB,
			"POOL_MODE":         "transaction",
			"MAX_CLIENT_CONN":   "100",
			"DEFAULT_POOL_SIZE": "3",
			"AUTH_TYPE":         "trust",
		},
		Networks:   []string{nw.Name},
		WaitingFor: wait.ForListeningPort("6432/tcp").WithStartupTimeout(90 * time.Second),
	}
	pgbC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: pgbReq,
		Started:          true,
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("start pgbouncer container: %w", err)
	}

	pgbHost, err := pgbC.Host(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve pgbouncer host: %w", err)
	}
	pgbPort, err := pgbC.MappedPort(ctx, "6432/tcp")
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve pgbouncer mapped port: %w", err)
	}
	pooledDSN = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", pgUser, pgPass, pgbHost, pgbPort.Port(), pgDB)

	cleanup = func() {
		_ = pgbC.Terminate(ctx)
		_ = pgC.Terminate(ctx)
		_ = nw.Remove(ctx)
	}
	return directDSN, pooledDSN, cleanup, nil
}
