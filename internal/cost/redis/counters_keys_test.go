package redis

import (
	"regexp"
	"testing"

	"github.com/google/uuid"
)

// hashTag is what Redis Cluster hashes: the text between the first { and the next }.
var hashTag = regexp.MustCompile(`\{([^}]*)\}`)

// A script that names two keys needs them in one slot, or a cluster refuses it
// with CROSSSLOT. The budget id in a hash tag puts both in the slot of that id,
// for every epoch.
func TestKeys_OfOneBudgetShareAHashTagAndSoASlot(t *testing.T) {
	id := uuid.New()
	epoch := hashTag.FindStringSubmatch(epochKey(id))
	if epoch == nil || epoch[1] != id.String() {
		t.Fatalf("epoch key %q has no {budget id} hash tag", epochKey(id))
	}
	for _, e := range []int64{1, 2, 99} {
		spend := hashTag.FindStringSubmatch(spendKey(id, e))
		if spend == nil || spend[1] != epoch[1] {
			t.Fatalf("spend key %q does not share the epoch key's hash tag %q", spendKey(id, e), epoch[1])
		}
	}
	if other := hashTag.FindStringSubmatch(epochKey(uuid.New())); other[1] == epoch[1] {
		t.Fatal("two budgets share a hash tag, so they would share a slot")
	}
}
