// Package postgres is the Postgres adapter for the surfaces' shared
// persistence ports: inbound-delivery dedup and the delivery outbox.
package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/truongpx396/nexus-agent/internal/store"
	"github.com/truongpx396/nexus-agent/internal/surfaces"
)

// Dedup implements surfaces.InboundDedup over the inbound_deliveries table, in
// the tenant's own scope.
type Dedup struct {
	st *store.Store
}

var _ surfaces.InboundDedup = (*Dedup)(nil)

// NewDedup returns a Dedup that opens its tenant-scoped transactions on st.
func NewDedup(st *store.Store) *Dedup { return &Dedup{st: st} }

func (d *Dedup) Claim(ctx context.Context, tenantID uuid.UUID, surfaceID, deliveryID, sessionKey string) (claimed bool, err error) {
	err = d.st.InTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var derr error
		claimed, derr = claimInboundDelivery(ctx, tx, tenantID, surfaceID, deliveryID, sessionKey)
		return derr
	})
	return claimed, err
}

func (d *Dedup) Release(ctx context.Context, tenantID uuid.UUID, surfaceID, deliveryID string) error {
	if deliveryID == "" {
		return nil // nothing was recorded for an id-less delivery
	}
	return d.st.InTenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM inbound_deliveries WHERE tenant_id = $1 AND surface_id = $2 AND delivery_id = $3`,
			tenantID, surfaceID, deliveryID)
		if err != nil {
			return fmt.Errorf("release inbound delivery %s/%s: %w", surfaceID, deliveryID, err)
		}
		return nil
	})
}
