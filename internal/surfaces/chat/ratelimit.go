package chat

import (
	"net"
	"sync"
	"time"
)

// Limiter is a token bucket per key, shared by every webhook surface. A bucket
// holds up to burst tokens and refills continuously, so the long-run rate is
// burst per refillEvery however the requests are spaced; a key seen for the
// first time starts full. A bucket that has been idle long enough to be full
// again is the same as no bucket, so it is dropped, which keeps the map from
// growing with every key ever seen. The limit is per process.
type Limiter struct {
	burst       float64
	refillEvery time.Duration
	now         func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// NewLimiter returns a Limiter that allows burst requests per refillEvery for
// each key. Non-positive values take the defaults of 20 per minute.
func NewLimiter(burst int, refillEvery time.Duration) *Limiter {
	if burst <= 0 {
		burst = 20
	}
	if refillEvery <= 0 {
		refillEvery = time.Minute
	}
	return &Limiter{burst: float64(burst), refillEvery: refillEvery, now: time.Now, buckets: map[string]*bucket{}}
}

// Allow reports whether key may proceed now, spending one token if so.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, at: now}
		return true
	}
	b.tokens = min(l.burst, b.tokens+l.burst*float64(now.Sub(b.at))/float64(l.refillEvery))
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops the buckets that have been idle for a full refill period, at most
// once per period.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.refillEvery {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.at) >= l.refillEvery {
			delete(l.buckets, k)
		}
	}
}

// Len is how many keys the limiter is tracking.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ClientIP is the address a request arrived from, given its RemoteAddr, for
// limiting traffic before any lookup. It is the connection's peer, not a
// forwarded header: a header is whatever the caller says it is, and so is no key
// to limit on. Behind a reverse proxy every request shares the proxy's address,
// which then limits them together; terminate the limit at the proxy in that case.
func ClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
