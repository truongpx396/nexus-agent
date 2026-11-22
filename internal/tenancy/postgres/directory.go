// Package postgres is the Postgres adapter for the tenants table: the
// operator's directory of tenants (Directory) and the registration of a new one
// (Register).
package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/truongpx396/nexus-agent/internal/tenancy"
)

// Directory implements tenancy.Directory over the tenants table.
//
// The runtime role (nexus_app, through PgBouncer) is RLS-restricted to whatever
// app.tenant_id the current transaction set, and there is no tenant to scope to
// when the question is "which tenants exist". So this connects directly as the
// administrative role, like the migrate command does for admin-only DDL.
//
// A long-running process hands it the pool it already holds (Pool), so the
// loops that list tenants every few seconds borrow a connection and open none. A
// one-shot command leaves Pool nil, and the directory opens a pool for the call
// and closes it after.
type Directory struct {
	// DSN is the administrative connection string, used when Pool is nil.
	DSN string

	// Pool is an open administrative pool the directory uses and never closes.
	Pool *pgxpool.Pool
}

// pool returns the pool to use and the function that releases it: a no-op for a
// pool the caller owns.
func (d Directory) pool(ctx context.Context) (*pgxpool.Pool, func(), error) {
	if d.Pool != nil {
		return d.Pool, func() {}, nil
	}
	pool, err := pgxpool.New(ctx, d.DSN)
	if err != nil {
		return nil, nil, fmt.Errorf("tenancy: connect as admin: %w", err)
	}
	return pool, pool.Close, nil
}

var (
	_ tenancy.Directory      = Directory{}
	_ tenancy.SessionLocator = Directory{}
)

func (d Directory) ListTenantIDs(ctx context.Context) ([]uuid.UUID, error) {
	pool, release, err := d.pool(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	rows, err := pool.Query(ctx, `SELECT tenant_id FROM tenants`)
	if err != nil {
		return nil, fmt.Errorf("tenancy: list tenants: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("tenancy: scan tenant id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// TenantOfSession implements tenancy.SessionLocator. It reads sessions as the
// administrative role, since no tenant scope exists yet to read it under.
func (d Directory) TenantOfSession(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	pool, release, err := d.pool(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer release()

	var tenantID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT tenant_id FROM sessions WHERE session_id = $1`, sessionID).Scan(&tenantID); err != nil {
		return uuid.Nil, fmt.Errorf("tenancy: resolve tenant for session %s: %w", sessionID, err)
	}
	return tenantID, nil
}
