package chat

import (
	"sync"
	"time"
)

// TTLCache remembers the result of a lookup for a short time, so that a flood
// of webhook requests for the same tenant does not become a flood of database
// transactions. It stores whatever the loader returned without error, an answer
// of "no such channel" included, and never an error. The zero value is not
// usable; a nil *TTLCache is, and loads every time.
type TTLCache[V any] struct {
	ttl   time.Duration
	limit int
	now   func() time.Time

	mu sync.Mutex
	m  map[string]entry[V]
}

type entry[V any] struct {
	v       V
	expires time.Time
}

// NewTTLCache returns a cache that holds up to limit entries for ttl each. At the
// bound, expired entries go first and, if none have expired, the cache starts
// over, so a stream of distinct keys cannot grow it.
func NewTTLCache[V any](ttl time.Duration, limit int) *TTLCache[V] {
	return &TTLCache[V]{ttl: ttl, limit: limit, now: time.Now, m: map[string]entry[V]{}}
}

// Get returns the cached value for key, or the result of load, which is cached
// when it succeeds.
func (c *TTLCache[V]) Get(key string, load func() (V, error)) (V, error) {
	if c == nil {
		return load()
	}
	now := c.now()
	c.mu.Lock()
	if e, ok := c.m[key]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()

	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.limit {
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= c.limit {
			clear(c.m)
		}
	}
	c.m[key] = entry[V]{v: v, expires: now.Add(c.ttl)}
	return v, nil
}
