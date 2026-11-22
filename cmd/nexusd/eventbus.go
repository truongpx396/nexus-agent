package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	runsrest "github.com/truongpx396/nexus-agent/internal/runs/rest"
)

// redisEventBus adapts *redis.Client to runsrest.EventBus — a single Publish
// call is all Redis Pub/Sub needs here; the consumer-group/XADD machinery
// a durable job queue needs belongs to internal/queue, not this.
type redisEventBus struct{ client *redis.Client }

func (b redisEventBus) Publish(ctx context.Context, channel string, payload []byte) error {
	return b.client.Publish(ctx, channel, payload).Err()
}

// startEventBus is this process's one static Redis Pub/Sub subscription,
// covering every session at once (runsrest.PublishDelta/PublishEvent's own
// "nexus:events:*"/"nexus:deltas:*" channel-naming convention) rather than
// a per-session dynamic subscribe/unsubscribe mirroring rest's own
// broker.subscribe/unsubscribe lifecycle: that would be the more
// scale-correct design (bounded to real local demand) but needs
// reference-counting bookkeeping this repo's actual scale (dozens
// of sessions, not millions) doesn't justify. One static
// pattern-subscription per process is O(1) regardless of session count and
// needs no lifecycle beyond process start/stop. If PSUBSCRIBE's
// O(all-processes x all-publishes) fan-out cost ever matters, per-session
// subscribe/unsubscribe is the thing to revisit first — named here the
// same way internal/queue.AdmissionController's own doc comment defers
// per-tenant fairness.
//
// This goroutine is the ONLY caller of runsrest.Handler.RelayEvent/RelayDelta in
// the whole binary: a same-process publish (runsrest.Handler.Bus nil) and a
// genuinely cross-process worker's publish both get republished to Redis
// and relayed back through here — one mechanism, not two.
func startEventBus(ctx context.Context, redisClient *redis.Client, runsAPI *runsrest.Handler) (stop func()) {
	sub := redisClient.PSubscribe(ctx, "nexus:events:*", "nexus:deltas:*")
	go func() {
		for msg := range sub.Channel() {
			sessionID, isDelta, ok := runsrest.ParseBusChannel(msg.Channel)
			if !ok {
				log.Warn().Str("channel", msg.Channel).Msg("nexusd: event bus: unrecognized channel")
				continue
			}
			if isDelta {
				var d runsrest.DeltaDTO
				if err := json.Unmarshal([]byte(msg.Payload), &d); err != nil {
					log.Error().Err(err).Str("channel", msg.Channel).Msg("nexusd: event bus: unmarshal delta")
					continue
				}
				runsAPI.RelayDelta(sessionID, d)
				continue
			}
			var wire runsrest.EventBusMessage
			if err := json.Unmarshal([]byte(msg.Payload), &wire); err != nil {
				log.Error().Err(err).Str("channel", msg.Channel).Msg("nexusd: event bus: unmarshal event")
				continue
			}
			var evErr error
			if wire.Err != "" {
				evErr = errors.New(wire.Err)
			}
			if wire.Event != nil {
				runsAPI.RelayEvent(sessionID, *wire.Event, evErr)
			}
		}
	}()
	return func() { _ = sub.Close() }
}
