package postgres

import (
	"errors"
	"testing"

	"github.com/truongpx396/nexus-agent/internal/crypto"
)

func wrapUnder(t *testing.T, kek crypto.KEK, keyID string) (crypto.DEK, []byte) {
	t.Helper()
	dek, err := crypto.GenerateDEK(keyID)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := crypto.WrapDEK(kek, dek)
	if err != nil {
		t.Fatal(err)
	}
	return dek, wrapped
}

// After a rotation the old KEK is retired, not deleted: a data key wrapped under it
// still opens, found by the id its row recorded, and a key wrapped under the new one
// opens too. Without the old KEK on file the failure says so by name.
func TestUnwrapWith_FindsTheKEKARowNames(t *testing.T) {
	oldKEK, _ := crypto.GenerateKEK()
	newKEK, _ := crypto.GenerateKEK()
	oldDEK, oldWrapped := wrapUnder(t, oldKEK, "key-old")
	newDEK, newWrapped := wrapUnder(t, newKEK, "key-new")

	rotated := NewKeyStore(newKEK).WithRetired(oldKEK)
	oldID, newID := oldKEK.ID(), newKEK.ID()
	if got, err := rotated.unwrapWith("key-old", oldWrapped, &oldID); err != nil || string(got.Key) != string(oldDEK.Key) {
		t.Fatalf("a key wrapped under the retired KEK = %v, want it unwrapped", err)
	}
	if got, err := rotated.unwrapWith("key-new", newWrapped, &newID); err != nil || string(got.Key) != string(newDEK.Key) {
		t.Fatalf("a key wrapped under the current KEK = %v, want it unwrapped", err)
	}

	forgotten := NewKeyStore(newKEK) // the old KEK was not listed
	if _, err := forgotten.unwrapWith("key-old", oldWrapped, &oldID); !errors.Is(err, crypto.ErrKEKUnavailable) {
		t.Fatalf("a key whose KEK is not loaded = %v, want ErrKEKUnavailable", err)
	}
}

// A row written before KEK ids were recorded names none, so each loaded KEK is tried;
// the authentication tag says which one wrapped it.
func TestUnwrapWith_ARowWithNoRecordedKEKIsTriedAgainstEachLoadedOne(t *testing.T) {
	oldKEK, _ := crypto.GenerateKEK()
	newKEK, _ := crypto.GenerateKEK()
	dek, wrapped := wrapUnder(t, oldKEK, "legacy")

	if got, err := NewKeyStore(newKEK).WithRetired(oldKEK).unwrapWith("legacy", wrapped, nil); err != nil || string(got.Key) != string(dek.Key) {
		t.Fatalf("a legacy row under the retired KEK = %v, want it unwrapped", err)
	}
	if _, err := NewKeyStore(newKEK).unwrapWith("legacy", wrapped, nil); !errors.Is(err, crypto.ErrKEKUnavailable) {
		t.Fatalf("a legacy row under an unloaded KEK = %v, want ErrKEKUnavailable", err)
	}
}
