//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/store"
	"github.com/truongpx396/nexus-agent/migrations"
)

// The session list, the orphan scan and the session-key lookup each need an index
// that fits the query. With sequential scans switched off the planner uses an
// index for each query if one can serve it, and the plan names it, so a later
// change to a query or an index that breaks the fit is caught here. No table is
// seeded: whether an index fits is a property of the query and the index, not of
// the row count.
func TestSessionIndexes_TheThreeHotQueriesAreIndexBacked(t *testing.T) {
	ctx := context.Background()
	pool := bareAdminPool(t)
	if _, err := store.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}

	tenant, user := uuid.NewString(), uuid.NewString()
	cases := []struct{ name, query, index string }{
		{"session list", `SELECT session_id FROM sessions WHERE tenant_id = '` + tenant + `' AND user_id = '` + user + `' AND parent_session_id IS NULL ORDER BY created_at DESC LIMIT 20`, "sessions_user_recent_idx"},
		{"orphan scan", `SELECT session_id, tenant_id, session_key FROM sessions WHERE status = 'running'`, "sessions_running_idx"},
		{"session key", `SELECT session_id FROM sessions WHERE tenant_id = '` + tenant + `' AND session_key = 'telegram:1' ORDER BY created_at DESC LIMIT 1`, "sessions_tenant_session_key_recent_idx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := conn.Query(ctx, `EXPLAIN `+tc.query)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(line + "\n")
			}
			if !strings.Contains(plan.String(), tc.index) {
				t.Fatalf("the plan does not use %s:\n%s", tc.index, plan.String())
			}
		})
	}

	var old int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'sessions_tenant_session_key_idx'`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("the superseded session-key index still exists (count %d, err %v)", old, err)
	}
}
