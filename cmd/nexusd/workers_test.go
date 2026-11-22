package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueWorkers(t *testing.T) {
	for name, tc := range map[string]struct {
		env  string
		want int
	}{
		"unset":        {"", queueWorkerCount},
		"set":          {"8", 8},
		"the ceiling":  {"64", 64},
		"over":         {"65", queueWorkerCount},
		"zero":         {"0", queueWorkerCount},
		"negative":     {"-3", queueWorkerCount},
		"not a number": {"many", queueWorkerCount},
	} {
		t.Setenv("NEXUS_QUEUE_WORKERS", tc.env)
		if got := queueWorkers(); got != tc.want {
			t.Errorf("%s: queueWorkers() = %d, want %d", name, got, tc.want)
		}
	}
}

func TestShutdownGrace(t *testing.T) {
	for name, tc := range map[string]struct {
		env  string
		want time.Duration
	}{
		"unset":          {"", defaultShutdownGrace},
		"seconds":        {"40s", 40 * time.Second},
		"minutes":        {"2m", 2 * time.Minute},
		"zero":           {"0s", defaultShutdownGrace},
		"negative":       {"-5s", defaultShutdownGrace},
		"not a duration": {"soon", defaultShutdownGrace},
		"a bare number":  {"30", defaultShutdownGrace}, // ParseDuration needs a unit
	} {
		t.Setenv("NEXUS_SHUTDOWN_GRACE", tc.env)
		if got := shutdownGrace(); got != tc.want {
			t.Errorf("%s: shutdownGrace() = %v, want %v", name, got, tc.want)
		}
	}
}

// Runs that finish inside the grace period are never cancelled.
func TestDrainWorkers_WaitsForRunsThatFinishInsideTheGrace(t *testing.T) {
	var workers sync.WaitGroup
	release := make(chan struct{})
	workers.Go(func() { <-release })
	var cancelled atomic.Bool

	go func() { time.Sleep(30 * time.Millisecond); close(release) }()
	forced := drainWorkers(&workers, 5*time.Second, func() { cancelled.Store(true) })

	if forced || cancelled.Load() {
		t.Fatalf("forced=%v cancelled=%v: a run that finished in time was cancelled", forced, cancelled.Load())
	}
}

// Past the grace period the runs are cancelled, and the drain still waits for the
// workers to unwind before returning, so the pool is not closed under them.
func TestDrainWorkers_CancelsWhenTheGraceRunsOutAndThenWaitsForTheWorkersToUnwind(t *testing.T) {
	var workers sync.WaitGroup
	jobCtx, cancelJobs := context.WithCancel(context.Background())
	var unwound atomic.Bool
	workers.Go(func() {
		<-jobCtx.Done()
		time.Sleep(30 * time.Millisecond) // winding down takes a moment
		unwound.Store(true)
	})

	forced := drainWorkers(&workers, 20*time.Millisecond, cancelJobs)

	if !forced {
		t.Fatal("the grace period ran out but drainWorkers did not report it")
	}
	if !unwound.Load() {
		t.Fatal("drainWorkers returned before the cancelled worker had unwound")
	}
}

func TestMaxConcurrentRuns_DefaultsToTheWorkerCountAndNeverExceedsIt(t *testing.T) {
	cases := []struct {
		set     string
		workers int
		want    int
	}{
		{"", 4, 4},   // unset: no throttle
		{"2", 4, 2},  // lowered
		{"9", 4, 4},  // above the worker count means nothing: a worker runs one job at a time
		{"0", 4, 4},  // out of range
		{"-1", 4, 4}, // out of range
	}
	for _, tc := range cases {
		t.Setenv("NEXUS_QUEUE_MAX_CONCURRENT_RUNS", tc.set)
		if got := maxConcurrentRuns(tc.workers); got != tc.want {
			t.Errorf("NEXUS_QUEUE_MAX_CONCURRENT_RUNS=%q with %d workers = %d, want %d", tc.set, tc.workers, got, tc.want)
		}
	}
}
