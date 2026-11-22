//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/truongpx396/nexus-agent/internal/store"
	"github.com/truongpx396/nexus-agent/migrations"
)

// adminPool starts a migrated Postgres and returns a pool that connects to it
// directly as the superuser, the way the server's administrative pool does.
func adminPool(t *testing.T) *pgxpool.Pool {
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
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://nexus:nexus@%s:%s/nexus", host, port.Port()))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := store.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// Of two replicas running the same pass at once, one does the work and the other
// skips it, whichever got there first; and once the first has finished the pass is
// free to run again.
func TestExclusive_OnlyOneRunnerHoldsAPassAtATime(t *testing.T) {
	pool := adminPool(t)
	ctx := context.Background()

	inside, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	var firstRan bool
	wg.Go(func() {
		firstRan, _ = Exclusive(ctx, pool, "sweep", func(context.Context) {
			close(inside)
			<-release
		})
	})
	<-inside

	ran, err := Exclusive(ctx, pool, "sweep", func(context.Context) { t.Error("the body ran while another runner held the pass") })
	if err != nil || ran {
		t.Fatalf("second runner: ran = %v, err = %v, want it skipped", ran, err)
	}
	if other, err := Exclusive(ctx, pool, "another pass", func(context.Context) {}); err != nil || !other {
		t.Fatalf("a different pass was blocked: ran = %v, err = %v", other, err)
	}

	close(release)
	wg.Wait()
	if !firstRan {
		t.Fatal("the first runner did not run")
	}
	if again, err := Exclusive(ctx, pool, "sweep", func(context.Context) {}); err != nil || !again {
		t.Fatalf("the pass was not released after its runner finished: ran = %v, err = %v", again, err)
	}
}

// A directory handed a pool uses it and leaves it open, so a loop that lists the
// tenants every few seconds opens no pool per tick.
func TestDirectory_ReusesThePoolItIsGivenAndNeverClosesIt(t *testing.T) {
	pool := adminPool(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (tenant_id, name) VALUES ($1, 'dir-test')`, id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	dir := Directory{DSN: "postgres://unreachable.invalid/none", Pool: pool} // the DSN must never be dialled
	for range 3 {
		ids, err := dir.ListTenantIDs(ctx)
		if err != nil {
			t.Fatalf("ListTenantIDs: %v", err)
		}
		found := false
		for _, got := range ids {
			found = found || got == id
		}
		if !found {
			t.Fatalf("the tenant was not listed: %v", ids)
		}
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("the shared pool was closed by the directory: %v", err)
	}
}
