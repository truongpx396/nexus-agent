// Package redis is the Redis adapter for cost.Counters: the tenant-scoped
// ceiling's atomic spend counter, driven entirely through Lua scripts.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/truongpx396/nexus-agent/internal/cost"
)

// The tenant-level ceiling (BudgetScopeTenant) is enforced across every
// worker/goroutine touching one tenant, so it needs one atomic counter
// shared outside any single process — Redis, driven entirely through Lua
// scripts so a check-then-increment-then-ceiling-compare is one atomic
// round trip, never three racy ones (task 4.4).
//
// Each budget owns two keys: an EPOCH key (a generation marker) and a
// SPEND key scoped to that epoch. Reserve's script refuses to act at all
// if the epoch key is missing or doesn't match the caller's expectation —
// "unknown epoch = unavailable = fail closed (never 'no spend yet')"
// (task 4.4) — rather than silently treating a cold/flushed Redis as zero
// spend. The epoch key is only ever created by Arm (cost.Gate), which
// rehydrates the spend counter from Postgres's own durable cost_records
// history first — so even a full Redis flush recovers to the TRUE spend,
// not to zero.

// Both keys carry the budget id in a hash tag, {id}, so Redis Cluster maps them
// to one slot: a script that names two keys fails with CROSSSLOT on a cluster
// otherwise. A deployment that moves from keys without the tag starts counting
// on the new ones: the first reservation finds no epoch, the gate arms it from
// Postgres's reconciled spend, and replicas of the old version, until they are
// replaced, keep counting on the old keys, so a ceiling is enforced on two
// counters for the length of one rolling deploy.
func epochKey(budgetID uuid.UUID) string {
	return "nexus:cost:budget:{" + budgetID.String() + "}:epoch"
}

func spendKey(budgetID uuid.UUID, epoch int64) string {
	return fmt.Sprintf("nexus:cost:budget:{%s}:spend:%d", budgetID, epoch)
}

// keyTTL is how long a budget's two keys live without being touched. Every
// script refreshes both, so a live budget never expires, and a budget idle for
// this long loses both together: its epoch is then missing, the gate fails closed
// and re-arms it from Postgres, which is the same path a flushed Redis takes. The
// spend key of an epoch that was replaced is never touched again and so expires,
// where before it stayed forever.
const keyTTL = 30 * 24 * time.Hour

// luaReserve: KEYS[1]=epoch key, KEYS[2]=spend key. ARGV[1]=expected
// epoch, ARGV[2]=amount to reserve, ARGV[3]=ceiling, ARGV[4]=key TTL in ms. Returns {code, total}
// where total is the resulting spend counter (or the epoch on a mismatch,
// or 0 when unavailable) — atomic: the ceiling compare-and-rollback can
// never race against a concurrent Reserve the way separate GET+INCR+GET
// calls would.
const luaReserve = `
local epoch = redis.call('GET', KEYS[1])
if not epoch then
  return {-1, 0}
end
if epoch ~= ARGV[1] then
  return {-2, tonumber(epoch)}
end
local newTotal = tonumber(redis.call('INCRBY', KEYS[2], ARGV[2]))
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[4])
if newTotal > tonumber(ARGV[3]) then
  redis.call('DECRBY', KEYS[2], ARGV[2])
  return {0, newTotal - tonumber(ARGV[2])}
end
return {1, newTotal}
`

// luaRelease: KEYS[1]=epoch key, KEYS[2]=spend key. ARGV[1]=expected
// epoch, ARGV[2]=delta to release (DECRBY; a negative delta increments,
// covering the "actual cost exceeded the reservation" case), ARGV[3]=key TTL
// in ms. A stale
// epoch is a documented no-op (redis.go's package doc comment) — nothing
// this phase ever advances a live budget's epoch, so it is never actually
// exercised outside a test that deliberately re-arms one.
const luaRelease = `
local epoch = redis.call('GET', KEYS[1])
if not epoch or epoch ~= ARGV[1] then
  return 0
end
redis.call('DECRBY', KEYS[2], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('PEXPIRE', KEYS[2], ARGV[3])
return 1
`

// luaArm: KEYS[1]=epoch key, KEYS[2]=spend key. ARGV[1]=epoch to
// establish, ARGV[2]=baseline spend (Postgres's own reconciled total —
// cost.Gate's Arm, never zero-on-faith), ARGV[3]=key TTL in ms. A no-op if the epoch key already
// exists: arming must never clobber a counter another still-live process
// is actively reserving against.
const luaArm = `
local existing = redis.call('GET', KEYS[1])
if existing then
  return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[3])
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
return 1
`

// The scripts are loaded once and run by their SHA (EVALSHA, falling back to
// EVAL when the server has not seen them), so a reservation sends 40 bytes of
// script identity and not the whole source.
var (
	reserveScript = goredis.NewScript(luaReserve)
	releaseScript = goredis.NewScript(luaRelease)
	armScript     = goredis.NewScript(luaArm)
)

// Counters implements cost.Counters over a real *redis.Client. The Lua
// atomicity is only proven against the real thing, so costtest.RunCounters
// runs the same contract here (integration) as against the in-memory
// adapter (unit) — that is what keeps the fake honest.
type Counters struct {
	client  *goredis.Client
	timeout time.Duration
}

// New wraps client; a non-positive timeout defaults to 200ms.
func New(client *goredis.Client, timeout time.Duration) *Counters {
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	return &Counters{client: client, timeout: timeout}
}

func (s *Counters) Reserve(ctx context.Context, budgetID uuid.UUID, epoch, amount, ceiling int64) (cost.ReserveOutcome, int64, error) {
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	res, err := reserveScript.Run(cctx, s.client, []string{epochKey(budgetID), spendKey(budgetID, epoch)}, epoch, amount, ceiling, keyTTL.Milliseconds()).Result()
	if err != nil {
		// A Redis error (timeout, connection refused, ...) is exactly as
		// fail-closed as a missing epoch key: the gate cannot prove
		// there's room, so it must not assume there is.
		return cost.ReserveUnavailable, 0, fmt.Errorf("redis reserve: %w", err)
	}
	pair, ok := res.([]any)
	if !ok || len(pair) != 2 {
		return cost.ReserveUnavailable, 0, fmt.Errorf("redis reserve: unexpected script result %#v", res)
	}
	code := toInt64(pair[0])
	total := toInt64(pair[1])
	switch code {
	case -1:
		return cost.ReserveUnavailable, 0, nil
	case -2:
		return cost.ReserveStaleEpoch, total, nil
	case 0:
		return cost.ReserveOverCeiling, total, nil
	case 1:
		return cost.ReserveOK, total, nil
	default:
		return cost.ReserveUnavailable, 0, fmt.Errorf("redis reserve: unexpected status code %d", code)
	}
}

func (s *Counters) Release(ctx context.Context, budgetID uuid.UUID, epoch, delta int64) error {
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := releaseScript.Run(cctx, s.client, []string{epochKey(budgetID), spendKey(budgetID, epoch)}, epoch, delta, keyTTL.Milliseconds()).Err(); err != nil {
		return fmt.Errorf("redis release: %w", err)
	}
	return nil
}

func (s *Counters) Arm(ctx context.Context, budgetID uuid.UUID, epoch, baseline int64) error {
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := armScript.Run(cctx, s.client, []string{epochKey(budgetID), spendKey(budgetID, epoch)}, epoch, baseline, keyTTL.Milliseconds()).Err(); err != nil {
		return fmt.Errorf("redis arm: %w", err)
	}
	return nil
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
