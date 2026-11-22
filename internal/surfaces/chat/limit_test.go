package chat

import (
	"errors"
	"testing"
	"time"
)

func clockAt(t *time.Time) func() time.Time { return func() time.Time { return *t } }

// The old limiter reset its fill time on every refill and so dropped the partial
// interval. This one refills continuously: 10 tokens a minute is one every 6 s,
// however the requests are spaced.
func TestLimiter_RefillsAtTheConfiguredRate(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(10, time.Minute)
	l.now = clockAt(&now)

	for range 10 {
		if !l.Allow("k") {
			t.Fatal("a full bucket refused a request")
		}
	}
	if l.Allow("k") {
		t.Fatal("an empty bucket allowed a request")
	}
	// 5 s is less than one refill interval, then 5 s more is more than one: the
	// first 5 s must not be thrown away.
	now = now.Add(5 * time.Second)
	if l.Allow("k") {
		t.Fatal("allowed a request 5 s after the bucket emptied, before a token was due")
	}
	now = now.Add(5 * time.Second)
	if !l.Allow("k") {
		t.Fatal("refused a request 10 s after the bucket emptied, when one token (6 s) was due")
	}
}

func TestLimiter_ForgetsIdleKeys(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(5, time.Minute)
	l.now = clockAt(&now)
	for i := range 1000 {
		l.Allow(string(rune('a'+i%26)) + string(rune(i)))
	}
	if l.Len() < 900 {
		t.Fatalf("tracking %d keys, expected the burst of distinct keys to be held", l.Len())
	}
	now = now.Add(2 * time.Minute)
	l.Allow("fresh")
	if l.Len() != 1 {
		t.Fatalf("tracking %d keys after a quiet period, want only the live one", l.Len())
	}
}

func TestClientIP_IsThePeerAddressWithoutItsPort(t *testing.T) {
	if got := ClientIP("203.0.113.9:4444"); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want the connection's address", got)
	}
	if got := ClientIP("[2001:db8::1]:4444"); got != "2001:db8::1" {
		t.Fatalf("ClientIP = %q, want the IPv6 address without its port", got)
	}
}

func TestTTLCache_RemembersAnAnswerAndForgetsAnError(t *testing.T) {
	now := time.Unix(0, 0)
	c := NewTTLCache[string](time.Minute, 8)
	c.now = clockAt(&now)
	loads := 0
	load := func() (string, error) { loads++; return "v", nil }

	for range 3 {
		if v, err := c.Get("k", load); err != nil || v != "v" {
			t.Fatalf("Get = %q, %v", v, err)
		}
	}
	if loads != 1 {
		t.Fatalf("loaded %d times within the ttl, want 1", loads)
	}
	now = now.Add(2 * time.Minute)
	_, _ = c.Get("k", load)
	if loads != 2 {
		t.Fatalf("loaded %d times after the ttl, want 2", loads)
	}

	boom := errors.New("db down")
	fails := 0
	for range 2 {
		if _, err := c.Get("bad", func() (string, error) { fails++; return "", boom }); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the load's error", err)
		}
	}
	if fails != 2 {
		t.Fatalf("an error was cached: loader ran %d times, want 2", fails)
	}
}

func TestTTLCache_NeverGrowsPastItsBound(t *testing.T) {
	c := NewTTLCache[int](time.Hour, 16)
	for i := range 1000 {
		_, _ = c.Get(string(rune(i)), func() (int, error) { return i, nil })
	}
	if len(c.m) > 16 {
		t.Fatalf("cache holds %d entries, bound is 16", len(c.m))
	}
}

func TestNilTTLCache_LoadsEveryTime(t *testing.T) {
	var c *TTLCache[int]
	n := 0
	for range 3 {
		_, _ = c.Get("k", func() (int, error) { n++; return n, nil })
	}
	if n != 3 {
		t.Fatalf("a nil cache loaded %d times, want 3", n)
	}
}
