package main

import (
	"testing"
	"time"
)

// A failing Accept (out of file descriptors) fails again at once, so the loop
// pauses between tries: 5 ms doubling to a 1 s ceiling.
func TestNextAcceptBackoff_DoublesFromFiveMillisecondsToOneSecond(t *testing.T) {
	d := time.Duration(0)
	var got []time.Duration
	for range 12 {
		d = nextAcceptBackoff(d)
		got = append(got, d)
	}
	if got[0] != 5*time.Millisecond || got[1] != 10*time.Millisecond || got[2] != 20*time.Millisecond {
		t.Fatalf("start of the sequence = %v, want 5ms, 10ms, 20ms", got[:3])
	}
	if got[len(got)-1] != time.Second {
		t.Fatalf("the sequence ends at %v, want it capped at 1s", got[len(got)-1])
	}
}
