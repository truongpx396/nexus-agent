package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseKeyring_ReadsRetiredPublicKeysAndRefusesMalformedOnes(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := json.Marshal(map[string]string{"key-2025": base64.StdEncoding.EncodeToString(pub)})
	keys, err := ParseKeyring(good)
	if err != nil || !keys["key-2025"].Equal(pub) {
		t.Fatalf("ParseKeyring = %v, %v, want the key back", keys, err)
	}
	for name, in := range map[string]string{
		"not json":      `nope`,
		"not base64":    `{"k": "%%%"}`,
		"wrong length":  `{"k": "AAAA"}`,
		"not an object": `["k"]`,
	} {
		if _, err := ParseKeyring([]byte(in)); err == nil {
			t.Errorf("%s: ParseKeyring accepted %q", name, in)
		}
	}
	if keys, err := ParseKeyring([]byte(`{}`)); err != nil || len(keys) != 0 {
		t.Errorf("an empty keyring = %v, %v, want no keys and no error", keys, err)
	}
}
