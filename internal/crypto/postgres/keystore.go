// Package postgres is the Postgres adapter for per-tenant data keys: the
// wrapped DEKs in encryption_keys and the crypto-shredding that makes erasure
// real. The envelope itself (DEK, KEK, Seal, Open) is internal/crypto, which
// has no storage.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/truongpx396/nexus-agent/internal/crypto"
)

// KeyStore persists wrapped per-tenant DEKs in encryption_keys. Every method
// must run inside a tenant-scoped transaction (store.Store.InTenantTx) — the
// table is RLS'd like every other tenant-owned row.
//
// A new DEK is wrapped under KEK, the current key-encryption key, and the row records
// its id. A DEK is unwrapped with the KEK whose id its row names, looked up in KEK and
// Retired, so a KEK can be rotated without downtime: deploy the new one as KEK and the
// old as Retired, and everything written before is still readable. A row with no id (one
// written before ids were recorded) is tried against each key in turn.
type KeyStore struct {
	KEK     crypto.KEK
	Retired []crypto.KEK
}

func NewKeyStore(kek crypto.KEK) *KeyStore {
	return &KeyStore{KEK: kek}
}

// WithRetired returns ks with the KEKs that wrapped keys before the current one.
func (ks *KeyStore) WithRetired(keks ...crypto.KEK) *KeyStore {
	ks.Retired = append(ks.Retired, keks...)
	return ks
}

// NewDEK generates a fresh crypto.DEK for tenantID, persists its wrapped form, and
// returns the usable (unwrapped) key for immediate use sealing this turn's
// events.
func (ks *KeyStore) NewDEK(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (crypto.DEK, error) {
	keyID := uuid.New().String()
	dek, err := crypto.GenerateDEK(keyID)
	if err != nil {
		return crypto.DEK{}, err
	}
	wrapped, err := crypto.WrapDEK(ks.KEK, dek)
	if err != nil {
		return crypto.DEK{}, fmt.Errorf("wrap new crypto.DEK: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO encryption_keys (key_id, tenant_id, wrapped_dek, kek_id) VALUES ($1, $2, $3, $4)`,
		keyID, tenantID, wrapped, ks.KEK.ID(),
	)
	if err != nil {
		return crypto.DEK{}, fmt.Errorf("persist wrapped crypto.DEK: %w", err)
	}
	return dek, nil
}

// Unwrap loads and unwraps the crypto.DEK identified by keyID, within the caller's
// tenant-scoped transaction (RLS makes a cross-tenant keyID invisible rather
// than merely refused).
func (ks *KeyStore) Unwrap(ctx context.Context, tx pgx.Tx, keyID string) (crypto.DEK, error) {
	var wrapped []byte
	var status string
	var kekID *string
	err := tx.QueryRow(ctx,
		`SELECT wrapped_dek, status, kek_id FROM encryption_keys WHERE key_id = $1`,
		keyID,
	).Scan(&wrapped, &status, &kekID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return crypto.DEK{}, fmt.Errorf("encryption key %s (wrong tenant scope, or it does not exist): %w", keyID, crypto.ErrKeyNotFound)
		}
		return crypto.DEK{}, fmt.Errorf("load encryption key %s: %w", keyID, err)
	}
	if status == "shredded" {
		return crypto.DEK{}, crypto.ErrKeyShredded
	}
	return ks.unwrapWith(keyID, wrapped, kekID)
}

// unwrapWith opens wrapped with the KEK named by kekID, or, for a row that records
// none, with the first of the loaded KEKs that opens it (an AEAD tag says which).
func (ks *KeyStore) unwrapWith(keyID string, wrapped []byte, kekID *string) (crypto.DEK, error) {
	keks := append([]crypto.KEK{ks.KEK}, ks.Retired...)
	if kekID != nil {
		for _, k := range keks {
			if k.ID() == *kekID {
				return crypto.UnwrapDEK(k, keyID, wrapped)
			}
		}
		return crypto.DEK{}, fmt.Errorf("encryption key %s was wrapped under KEK %s: %w", keyID, *kekID, crypto.ErrKEKUnavailable)
	}
	for _, k := range keks {
		if dek, err := crypto.UnwrapDEK(k, keyID, wrapped); err == nil {
			return dek, nil
		}
	}
	return crypto.DEK{}, fmt.Errorf("encryption key %s opens under none of the %d loaded KEKs: %w", keyID, len(keks), crypto.ErrKEKUnavailable)
}
