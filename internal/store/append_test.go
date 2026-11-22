package store

import (
	"testing"

	"github.com/google/uuid"
)

// The append lock's key space is shared by every tenant. It is taken on two
// disjoint 32-bit words of the session id, so two ids that agree on one word
// still do not share a lock.
func TestAdvisoryKey_UsesTwoIndependentWordsOfTheSessionID(t *testing.T) {
	a := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	sameFirst := uuid.MustParse("11111111-2222-4333-8444-5555555555ff") // differs only in the last bytes
	sameSecond := uuid.MustParse("99999999-2222-4333-8444-555555555555")

	a1, a2 := advisoryKey(a)
	b1, b2 := advisoryKey(sameFirst)
	c1, c2 := advisoryKey(sameSecond)
	if a1 != b1 || a2 != b2 {
		t.Fatalf("ids that agree on bytes 0-3 and 8-11 got different keys: (%d,%d) vs (%d,%d)", a1, a2, b1, b2)
	}
	if a1 == c1 || a2 != c2 {
		t.Fatalf("changing only the first word should change only the first key: (%d,%d) vs (%d,%d)", a1, a2, c1, c2)
	}
	if k1, k2 := advisoryKey(a); k1 != a1 || k2 != a2 {
		t.Fatal("the key is not a pure function of the session id")
	}
}
