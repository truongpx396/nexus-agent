package main

import (
	"fmt"
	"os"

	"github.com/truongpx396/nexus-agent/internal/audit"
	auditpg "github.com/truongpx396/nexus-agent/internal/audit/postgres"
)

// newAuditChain builds the audit chain over signer and loads the retired signer
// public keys the verifier also trusts. NEXUS_AUDIT_RETIRED_KEYS_FILE names a JSON
// object of key id to the base64 of an Ed25519 public key (audit.ParseKeyring); when
// the signer's key is rotated, the old key's public half goes in here, so receipts
// and anchors signed under it still verify. Unset trusts only the signer's current
// key. A file that is named but unreadable is an error, not a silent fallback: a
// verifier that quietly trusted fewer keys would report a break for every older
// receipt.
func newAuditChain(signer audit.Signer) (*auditpg.Chain, error) {
	chain := auditpg.NewChain(signer)
	path := envOr("NEXUS_AUDIT_RETIRED_KEYS_FILE", "")
	if path == "" {
		return chain, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is operator configuration, like the key paths loaded beside it
	if err != nil {
		return nil, fmt.Errorf("read retired audit signer keys: %w", err)
	}
	if chain.Retired, err = audit.ParseKeyring(data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return chain, nil
}
