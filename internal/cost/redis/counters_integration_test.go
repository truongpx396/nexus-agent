//go:build integration

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func newTestClient(t *testing.T) *goredis.Client {
	t.Helper()
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7")
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	connStr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	opts, err := goredis.ParseURL(connStr)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	client := goredis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// Both keys of a budget carry a TTL from the moment they are created, refreshed
// by every call, so a budget abandoned for good (a replaced epoch's spend key)
// does not stay in Redis forever; while it is in use it never expires.
func TestCounters_EveryScriptGivesBothKeysATTL(t *testing.T) {
	client := newTestClient(t)
	c := New(client, time.Second)
	ctx := context.Background()
	id := uuid.New()

	ttls := func() (time.Duration, time.Duration) {
		t.Helper()
		e, err1 := client.PTTL(ctx, epochKey(id)).Result()
		s, err2 := client.PTTL(ctx, spendKey(id, 1)).Result()
		if err1 != nil || err2 != nil {
			t.Fatalf("PTTL: %v, %v", err1, err2)
		}
		return e, s
	}
	check := func(step string) {
		t.Helper()
		e, s := ttls()
		if e <= 0 || e > keyTTL || s <= 0 || s > keyTTL {
			t.Fatalf("after %s the epoch key has TTL %v and the spend key %v, want both in (0, %v]", step, e, s, keyTTL)
		}
	}

	if err := c.Arm(ctx, id, 1, 0); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	check("Arm")

	// Wind both down, then show a reservation and a release put them back.
	client.PExpire(ctx, epochKey(id), time.Minute)
	client.PExpire(ctx, spendKey(id, 1), time.Minute)
	if _, _, err := c.Reserve(ctx, id, 1, 10, 1000); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	check("Reserve")

	client.PExpire(ctx, epochKey(id), time.Minute)
	client.PExpire(ctx, spendKey(id, 1), time.Minute)
	if err := c.Release(ctx, id, 1, 5); err != nil {
		t.Fatalf("Release: %v", err)
	}
	check("Release")
}
