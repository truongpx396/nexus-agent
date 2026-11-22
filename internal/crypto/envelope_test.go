package crypto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	dek, err := GenerateDEK("key-1")
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}
	plaintext := []byte(`{"body":"hello"}`)

	sealed, err := Seal(dek, plaintext, "tenant-a", "session-1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("sealed output contains the plaintext verbatim")
	}

	got, err := Open(dek, sealed, "tenant-a", "session-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open = %q, want %q", got, plaintext)
	}
}

func TestOpenFailsUnderWrongAAD(t *testing.T) {
	dek, err := GenerateDEK("key-1")
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}
	sealed, err := Seal(dek, []byte("secret"), "tenant-a", "session-1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// A sealed payload bound to (tenant-a, session-1) must not open under a
	// different session — this is what stops a ciphertext being replayed
	// onto a different event row.
	if _, err := Open(dek, sealed, "tenant-a", "session-2"); err == nil {
		t.Fatal("Open succeeded under a mismatched AAD binding")
	}
}

func TestWrapUnwrapDEKRoundTrip(t *testing.T) {
	kek, err := GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	dek, err := GenerateDEK("key-42")
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}

	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if bytes.Contains(wrapped, dek.Key) {
		t.Fatal("wrapped DEK contains the raw key bytes verbatim")
	}

	got, err := UnwrapDEK(kek, "key-42", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got.Key, dek.Key) {
		t.Fatal("UnwrapDEK did not recover the original key bytes")
	}
}

func TestUnwrapDEKFailsUnderWrongKeyID(t *testing.T) {
	kek, err := GenerateKEK()
	if err != nil {
		t.Fatalf("GenerateKEK: %v", err)
	}
	dek, err := GenerateDEK("key-42")
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}
	wrapped, err := WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}

	if _, err := UnwrapDEK(kek, "some-other-key-id", wrapped); err == nil {
		t.Fatal("UnwrapDEK succeeded with a keyID the wrapped DEK was not bound to")
	}
}

func TestDigestIsDeterministicAndKeyIndependent(t *testing.T) {
	plaintext := []byte("the event payload, in the clear")

	d1 := Digest(plaintext)
	d2 := Digest(plaintext)
	if !bytes.Equal(d1, d2) {
		t.Fatal("Digest is not deterministic")
	}

	other := Digest([]byte("a different payload"))
	if bytes.Equal(d1, other) {
		t.Fatal("Digest did not distinguish different plaintexts")
	}
}

// The AAD binds a ciphertext to its tenant and session, not to its event, so two
// events of one session can have their ciphertexts swapped and both still decrypt.
// The digest each event records is what notices.
func TestOpenEvent_ASwappedCiphertextIsRefusedByTheEventsDigest(t *testing.T) {
	dek, err := GenerateDEK("k")
	if err != nil {
		t.Fatal(err)
	}
	const tenant, session = "tenant-1", "session-1"
	first, second := []byte(`{"body":"approve the refund"}`), []byte(`{"body":"deny the refund"}`)
	sealedFirst, err := Seal(dek, first, tenant, session)
	if err != nil {
		t.Fatal(err)
	}
	sealedSecond, err := Seal(dek, second, tenant, session)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := OpenEvent(dek, sealedFirst, Digest(first), tenant, session); err != nil || string(got) != string(first) {
		t.Fatalf("an untouched event = %q, %v, want its plaintext", got, err)
	}
	// Event one now holds event two's ciphertext. Plain Open is none the wiser.
	if _, err := Open(dek, sealedSecond, tenant, session); err != nil {
		t.Fatalf("Open of the swapped ciphertext failed (%v): the premise of the test is that it decrypts", err)
	}
	if _, err := OpenEvent(dek, sealedSecond, Digest(first), tenant, session); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("OpenEvent of a swapped ciphertext = %v, want ErrDigestMismatch", err)
	}
}

func TestOpenEvent_AnEventWithNoDigestIsNotChecked(t *testing.T) {
	dek, _ := GenerateDEK("k")
	sealed, _ := Seal(dek, []byte("x"), "t", "s")
	if got, err := OpenEvent(dek, sealed, nil, "t", "s"); err != nil || string(got) != "x" {
		t.Fatalf("OpenEvent with no digest = %q, %v, want the plaintext", got, err)
	}
}

func TestKEK_IDIsStableDistinctAndDoesNotRevealTheKey(t *testing.T) {
	a, _ := GenerateKEK()
	b, _ := GenerateKEK()
	again := a.ID()
	if a.ID() != again || a.ID() == b.ID() || len(a.ID()) != 16 {
		t.Fatalf("ids %q, %q: want a stable 16-character id that differs between keys", a.ID(), b.ID())
	}
	if strings.Contains(a.ID(), hex.EncodeToString(a.Bytes())[:8]) {
		t.Fatal("the id contains a prefix of the key")
	}
}
