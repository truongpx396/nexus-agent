package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Exclusive runs fn only if no other process is running the pass called name,
// and reports whether it ran. Every replica runs every background loop, and a
// pass that sweeps every tenant (anchor, team backstop, idle sweep) is wasted
// work, or worse a race, when N replicas do it at once. The pass takes a
// Postgres session-level advisory lock on a connection of its own, so it is held
// for exactly as long as fn runs and is released even if the process dies, since
// the lock belongs to the connection.
//
// The pool must connect to Postgres directly: through a transaction-pooling
// PgBouncer a session-level lock would stay with whichever connection the pooler
// had reassigned.
func Exclusive(ctx context.Context, pool *pgxpool.Pool, name string, fn func(context.Context)) (ran bool, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("tenancy: acquire a connection for pass %q: %w", name, err)
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, "nexus:pass:"+name).Scan(&got); err != nil {
		return false, fmt.Errorf("tenancy: lock pass %q: %w", name, err)
	}
	if !got {
		return false, nil
	}
	defer func() {
		// The caller's context may be what ended fn; the unlock must not depend on it.
		if _, uerr := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext($1))`, "nexus:pass:"+name); uerr != nil && err == nil {
			err = fmt.Errorf("tenancy: unlock pass %q: %w", name, uerr)
		}
	}()
	fn(ctx)
	return true, nil
}
