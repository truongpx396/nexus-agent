package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/truongpx396/nexus-agent/internal/queue"
)

// Every transition that moves a job between structures is ONE Lua script, so a
// crash or a Redis error part-way cannot leave the job in neither place. A job
// that was retired from the stream and then not yet re-added, or removed from the
// delay set and not yet added to the stream, was simply lost; only the orphan
// sweep, which looks for sessions still marked running and runs every five
// minutes, could bring such a run back.
var (
	// settleScript retires a leased entry (XACK, XDEL, drop the claim record) and
	// puts the job where its mode says, atomically.
	//
	// KEYS: 1 stream, 2 claim hash, 3 dead-letter stream, 4 delay set.
	// ARGV: 1 group, 2 entry id, 3 mode (dead | now | delay), 4 dead-letter
	// MAXLEN, 5 delay score (ms), 6 delay member (job JSON), 7... the job's
	// stream fields as name, value pairs.
	settleScript = goredis.NewScript(`
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
redis.call('DEL', KEYS[2])
if ARGV[3] == 'dead' then
  redis.call('XADD', KEYS[3], 'MAXLEN', ARGV[4], '*', unpack(ARGV, 7))
elseif ARGV[3] == 'now' then
  redis.call('XADD', KEYS[1], '*', unpack(ARGV, 7))
elseif ARGV[3] == 'delay' then
  redis.call('ZADD', KEYS[4], ARGV[5], ARGV[6])
end
return 1
`)

	// promoteScript moves one due member of the delay set onto the stream. The
	// ZREM's result decides which of several workers racing for the member wins it,
	// and the XADD is in the same script, so the winner cannot lose it.
	//
	// KEYS: 1 delay set, 2 stream. ARGV: 1 member, 2... the job's stream fields.
	promoteScript = goredis.NewScript(`
if redis.call('ZREM', KEYS[1], ARGV[1]) == 1 then
  redis.call('XADD', KEYS[2], '*', unpack(ARGV, 2))
  return 1
end
return 0
`)
)

type settleMode string

const (
	settleDead  settleMode = "dead"  // dead-letter it
	settleNow   settleMode = "now"   // back on the stream at once
	settleDelay settleMode = "delay" // into the delay set until retryAt
)

// flatFields is a job's stream fields as name, value, name, value, in a fixed order.
func flatFields(fields map[string]any) []any {
	out := make([]any, 0, 2*len(fields))
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		out = append(out, k, fields[k])
	}
	return out
}

// settle retires jobID's leased entry and files the job as mode says, in one script.
func (r *Streams) settle(ctx context.Context, jobID uuid.UUID, entryID string, mode settleMode, job queue.Job, lastErr string, retryAt time.Time) error {
	fields := jobFields(job)
	var member []byte
	var score int64
	switch mode {
	case settleDead:
		fields["last_error"] = lastErr
	case settleDelay:
		raw, err := json.Marshal(job) //nolint:gosec // SessionKey is the per-session serial-lock routing key, not a secret
		if err != nil {
			return fmt.Errorf("marshal delayed job %s: %w", jobID, err)
		}
		member, score = raw, retryAt.UnixMilli()
	case settleNow:
	}
	args := append([]any{groupName, entryID, string(mode), r.deadMaxLen, score, string(member)}, flatFields(fields)...)
	keys := []string{streamKey, entryKeyPrefix + jobID.String(), deadKey, delayedKey}
	if err := settleScript.Run(ctx, r.client, keys, args...).Err(); err != nil {
		return fmt.Errorf("settle job %s as %s: %w", jobID, mode, err)
	}
	return nil
}

// Fail retires the job's original entry and files it: dead-lettered
// (nexus:queue:dead, a plain inspectable stream) when permanent, back on the
// stream at once when retryAt has already passed, or parked in the delay set until
// then, all in one script. The entry is acked and deleted so it is never left in
// the pending list to be reclaimed again by XAUTOCLAIM: retry scheduling from here
// on is this method's job, not idle-timeout's.
func (r *Streams) Fail(ctx context.Context, jobID uuid.UUID, reason string, permanent bool, retryAt time.Time) error {
	entryID, job, err := r.claimedEntry(ctx, jobID)
	if err != nil {
		return fmt.Errorf("queue: fail job %s: %w", jobID, err)
	}
	mode := settleDelay
	switch {
	case permanent:
		mode = settleDead
	case !retryAt.After(time.Now()):
		mode = settleNow
	}
	if err := r.settle(ctx, jobID, entryID, mode, job, reason, retryAt); err != nil {
		return fmt.Errorf("queue: fail job %s: %w", jobID, err)
	}
	return nil
}

var _ queue.Releaser = (*Streams)(nil)

// Release hands a leased job back for another worker without counting the delivery
// as an attempt: it was never run (the session's lock was held by another worker,
// the process was stopping), so it is not a failure and must not move the job
// toward its attempt limit or lengthen its backoff.
func (r *Streams) Release(ctx context.Context, jobID uuid.UUID, retryAt time.Time) error {
	entryID, job, err := r.claimedEntry(ctx, jobID)
	if err != nil {
		return fmt.Errorf("queue: release job %s: %w", jobID, err)
	}
	job.Attempts = max(0, job.Attempts-1) // claim counted this delivery
	mode := settleDelay
	if !retryAt.After(time.Now()) {
		mode = settleNow
	}
	if err := r.settle(ctx, jobID, entryID, mode, job, "", retryAt); err != nil {
		return fmt.Errorf("queue: release job %s: %w", jobID, err)
	}
	return nil
}

// promoteDelayed moves every delay-set member whose retry-at has elapsed onto the
// live stream, each in one script.
func (r *Streams) promoteDelayed(ctx context.Context) error {
	dueBy := strconv.FormatInt(time.Now().UnixMilli(), 10)
	// ZRANGE ... BYSCORE (Redis 6.2+, the deployment and tests run redis:7)
	// replaces the deprecated ZRANGEBYSCORE; same range, same LIMIT 0 N.
	due, err := r.client.ZRangeArgs(ctx, goredis.ZRangeArgs{Key: delayedKey, Start: "-inf", Stop: dueBy, ByScore: true, Count: promoteBatch}).Result()
	if err != nil {
		return fmt.Errorf("list due delayed jobs: %w", err)
	}
	for _, raw := range due {
		var job queue.Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			// A malformed member can never be promoted: drop it rather than have every
			// future Lease trip over it.
			if zerr := r.client.ZRem(ctx, delayedKey, raw).Err(); zerr != nil {
				return fmt.Errorf("drop malformed delayed job: %w", zerr)
			}
			continue
		}
		args := append([]any{raw}, flatFields(jobFields(job))...)
		if err := promoteScript.Run(ctx, r.client, []string{delayedKey, streamKey}, args...).Err(); err != nil {
			return fmt.Errorf("promote delayed job %s: %w", job.JobID, err)
		}
	}
	return nil
}
