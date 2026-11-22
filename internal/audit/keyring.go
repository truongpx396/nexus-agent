package audit

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// ParseKeyring reads the public keys of signer keys that have been retired: a JSON
// object of key id to the base64 of its Ed25519 public key. A verifier trusts the
// signer's current key and these, so rotating the signer key does not make every
// receipt and anchor signed under an older one unverifiable. A retired key can
// only verify; nothing is ever signed with it.
func ParseKeyring(data []byte) (map[string]ed25519.PublicKey, error) {
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("audit: parse retired signer keys: %w", err)
	}
	keys := make(map[string]ed25519.PublicKey, len(raw))
	for id, enc := range raw {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, fmt.Errorf("audit: retired signer key %q is not base64: %w", id, err)
		}
		if len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("audit: retired signer key %q is %d bytes, want %d", id, len(b), ed25519.PublicKeySize)
		}
		keys[id] = ed25519.PublicKey(b)
	}
	return keys, nil
}
