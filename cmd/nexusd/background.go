package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	auditpg "github.com/truongpx396/nexus-agent/internal/audit/postgres"
	"github.com/truongpx396/nexus-agent/internal/cost"
	oversightpg "github.com/truongpx396/nexus-agent/internal/oversight/postgres"
	"github.com/truongpx396/nexus-agent/internal/queue"
	queueredis "github.com/truongpx396/nexus-agent/internal/queue/redis"
	"github.com/truongpx396/nexus-agent/internal/reliability"
	"github.com/truongpx396/nexus-agent/internal/runctl"
	runctlpg "github.com/truongpx396/nexus-agent/internal/runctl/postgres"
	runsrest "github.com/truongpx396/nexus-agent/internal/runs/rest"
	"github.com/truongpx396/nexus-agent/internal/surfaces/chat"
	"github.com/truongpx396/nexus-agent/internal/surfaces/cron"
	cronpg "github.com/truongpx396/nexus-agent/internal/surfaces/cron/postgres"
	"github.com/truongpx396/nexus-agent/internal/teams"
	"github.com/truongpx396/nexus-agent/internal/tenancy"
	tenancypg "github.com/truongpx396/nexus-agent/internal/tenancy/postgres"
)

const (
	// sessionLockTTL is the session-key serial lock's TTL. A worker renews it
	// every third of this while it runs (queue.WorkerConfig.RenewEvery), so it
	// is held for exactly as long as a worker is driving the session.
	sessionLockTTL = 30 * time.Second

	// orphanRecheckAfter is when the startup orphan sweep runs once more. A
	// worker that died mid-run leaves its lock behind until the TTL passes, so
	// the sweep at startup has to skip that session as if it were live; this
	// one, a TTL later, finds the lock gone and recovers it.
	orphanRecheckAfter = sessionLockTTL + 5*time.Second

	// orphanSweepEvery is how often the recurring orphan sweep runs, and
	// orphanReenqueueEvery how long recovery leaves a session alone after
	// re-enqueueing it. A job can be lost while every process stays up (Redis
	// restarted without its append-only file, a job dead-lettered by mistake), and
	// the startup sweep only runs at boot; the throttle keeps a session whose resume
	// keeps failing from being re-enqueued on every pass.
	orphanSweepEvery     = 5 * time.Minute
	orphanReenqueueEvery = 15 * time.Minute

	// deliveryRetryEvery is how often the chat-reply retry pass runs. A reply that
	// failed is due again after a backoff the ledger sets (a minute at the soonest),
	// so this only bounds how late after that it goes out.
	deliveryRetryEvery = 20 * time.Second

	// queueWorkerCount is how many workers this process runs to drain the job
	// queue; each takes one job at a time, so it is also this process's ceiling
	// on concurrently driven runs.
	queueWorkerCount = 2

	// sessionStateIdleFor and sessionStateSweepEvery bound the cost gate's
	// per-session ceiling state. It is rebuilt from the ledger the next time a
	// session reserves, so evicting an idle one loses nothing; without the sweep
	// the map grows by one entry per session for the life of the process.
	sessionStateIdleFor    = time.Hour
	sessionStateSweepEvery = 10 * time.Minute
)

// adminDSN is the administrative connection string, for the few operations
// that are genuinely cross-tenant and so cannot run as the RLS-restricted
// runtime role.
func adminDSN() string {
	return envOr("NEXUS_ADMIN_DATABASE_URL", envOr("NEXUS_MIGRATE_DATABASE_URL", defaultMigrateDSN))
}

// tenantDirectory is the directory for a one-shot command, which opens an
// administrative pool for the call and closes it. The server uses infra.directory,
// which shares the pool it opened at startup.
func tenantDirectory() tenancypg.Directory { return tenancypg.Directory{DSN: adminDSN()} }

// singleton wraps one of the loops' passes so that, of the replicas running it,
// only the one holding the pass's advisory lock does the work this tick. The
// rest skip it.
func singleton(pool *pgxpool.Pool, name string, pass func(context.Context)) func(context.Context) {
	return func(ctx context.Context) {
		ran, err := tenancypg.Exclusive(ctx, pool, name, pass)
		if err != nil {
			log.Error().Err(err).Str("pass", name).Msg("nexusd: background pass")
			return
		}
		if !ran {
			log.Debug().Str("pass", name).Msg("nexusd: another replica is running this pass")
		}
	}
}

// startPeriodic runs fn every interval until the returned stop is called. It is
// the one ticker loop the platform's scheduled passes share; what a pass does,
// and for which tenants, is the owning package's business (tenancy.ForEach
// visits them), so this file only decides when. stop cancels the context fn
// runs under, waits for a pass already in flight to return, and is safe to call
// more than once.
func startPeriodic(ctx context.Context, interval time.Duration, fn func(context.Context)) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	var loop sync.WaitGroup
	loop.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fn(ctx)
			}
		}
	})
	return func() {
		cancel()
		loop.Wait()
	}
}

// startBackground starts every loop that runs beside the HTTP server: audit
// anchoring, the team backstop, the idle-conversation sweep, the queue workers,
// the event bus and the cron scheduler. The one stop it returns ends them in the
// reverse of the order they started, as a run of defers would, and waits for each.
func startBackground(ctx context.Context, in *infra, svc *services, runsAPI *runsrest.Handler) (stop func()) {
	var stops []func()
	stops = append(stops,
		startAnchorLoop(ctx, in.directory(), in.admin, auditpg.Scoped{St: in.st, Chain: in.chain, Marks: auditpg.NewWatermarks()}),
		startTeamBackstopLoop(ctx, in.directory(), in.admin, svc.teams),
		startIdleConversationSweepLoop(ctx, in.directory(), in.admin, svc.ctl),
		startSessionStateSweepLoop(ctx, svc.gate),
		startQueueWorkers(ctx, in.redis, in.admin, in.queue, runsAPI, svc.ctl, svc.resumer, svc.replier, svc.gate),
		startDeliveryRetryLoop(ctx, in.directory(), svc.replier),
		startEventBus(ctx, in.redis, runsAPI),
		startScheduler(ctx, in, svc),
	)
	return func() {
		for _, stop := range slices.Backward(stops) {
			stop()
		}
	}
}

// startScheduler runs the cron surface's poll loop, which starts runs through
// the same starter every surface shares. Its stop cancels the loop and waits for
// it, so a pass that is mid-run finishes unwinding before the pool it uses closes.
func startScheduler(ctx context.Context, in *infra, svc *services) (stop func()) {
	scheduler := &cron.Scheduler{
		Schedules: cronpg.New(in.st, in.keys), Starter: svc.starter, Tenants: adminTenantLister{dir: in.directory()},
		CatalogManifestDigest: svc.catalogManifestDigest,
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scheduler.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// startDeliveryRetryLoop sends again every chat reply an earlier attempt left
// undelivered: one that failed, and one whose first attempt never happened
// because the process died between the event and the send. Each tenant's
// ledger leases what it takes, so replicas running this at once do not both
// send the same reply.
func startDeliveryRetryLoop(ctx context.Context, dir tenancy.Directory, replier *chat.Replier) (stop func()) {
	return startPeriodic(ctx, deliveryRetryEvery, func(ctx context.Context) {
		tenancy.ForEach(ctx, dir, "delivery retry", replier.RetryDue)
	})
}

// startAnchorLoop runs the scheduled verifier task 5.3 asks for: every
// anchorInterval, anchor every tenant's new receipts and verify the whole
// chain. A break or a gap is logged, not fatal — an alert a real deployment
// would ship to its paging system, not a reason to crash the process that is
// the source of truth for the very thing it is checking.
func startAnchorLoop(ctx context.Context, dir tenancy.Directory, pool *pgxpool.Pool, audit auditpg.Scoped) (stop func()) {
	return startPeriodic(ctx, anchorInterval, singleton(pool, "audit anchor/verify", func(ctx context.Context) {
		tenancy.ForEach(ctx, dir, "audit anchor/verify", audit.AnchorAndVerify)
	}))
}

// startTeamBackstopLoop runs README task 9.9's own wall-clock trigger: every
// teamBackstopSweepInterval, sweep every tenant for an 'active' team older than
// teamBackstopWindow and end it as 'aborted'.
func startTeamBackstopLoop(ctx context.Context, dir tenancy.Directory, pool *pgxpool.Pool, teamsSvc *teams.Service) (stop func()) {
	return startPeriodic(ctx, teamBackstopSweepInterval, singleton(pool, "team backstop", func(ctx context.Context) {
		tenancy.ForEach(ctx, dir, "team backstop", func(ctx context.Context, tenantID uuid.UUID) error {
			return teamsSvc.SweepBackstop(ctx, tenantID, teamBackstopWindow)
		})
	}))
}

// startIdleConversationSweepLoop runs the idle-conversation backstop: every
// idleConversationSweepInterval, sweep every tenant for a conversational session
// sitting in awaiting_input past idleConversationWindow and end it
// (runctl.Control.SweepIdle).
func startIdleConversationSweepLoop(ctx context.Context, dir tenancy.Directory, pool *pgxpool.Pool, ctl *runctl.Control) (stop func()) {
	return startPeriodic(ctx, idleConversationSweepInterval, singleton(pool, "idle conversation sweep", func(ctx context.Context) {
		olderThan := time.Now().Add(-idleConversationWindow)
		tenancy.ForEach(ctx, dir, "idle conversation sweep", func(ctx context.Context, tenantID uuid.UUID) error {
			return ctl.SweepIdle(ctx, tenantID, olderThan)
		})
	}))
}

// startSessionStateSweepLoop evicts the cost gate's per-session state that has
// sat idle for sessionStateIdleFor.
func startSessionStateSweepLoop(ctx context.Context, gate *cost.Gate) (stop func()) {
	return startPeriodic(ctx, sessionStateSweepEvery, func(context.Context) {
		if n := gate.SweepIdle(time.Now().Add(-sessionStateIdleFor)); n > 0 {
			log.Debug().Int("evicted", n).Msg("nexusd: cost gate: evicted idle session state")
		}
	})
}

// startQueueWorkers wires internal/queue's worker pool (README tasks
// 6.1-6.2) to internal/runctl.Control.Resume: the durable, crash-recoverable
// path a session's turn loop continues through, whether it's continuing
// after this process (or a prior one) died mid-turn (queue.KindResume), or
// picking up a fresh run/conversational follow-up some HTTP-handling
// process already seeded/appended and enqueued (queue.KindStart/
// KindConverse — kernelRunStarter.StartRun, internal/runctl.Control.
// ResumeConversation). By the time a worker leases any of these three, a
// fresh-started, crash-recovered, and human-followed-up session all look
// identical — their own seed/message event is already durably in the
// Postgres event log — so every Kind dispatches through the same
// ctl.Resume call; the switch below exists purely so logs/metrics can still
// tell them apart.
//
// It also sweeps for sessions a worker that died left stuck in "running"
// (runctl.StartRecovery) and enqueues a resume job for each — the
// concrete trigger behind README §6's demo line: "kill -9 the worker
// mid-tool-call -> the job re-queues and resumes from the checkpoint." A row
// still reading "running" is only an orphan if no worker is driving it, which
// with more than one process the row alone no longer says: so a session whose
// session lock is held (a live worker renews it every turn) is skipped, and a
// second sweep runs after the lock's TTL for the sessions a just-dead worker's
// lock was still covering. This sweep needs no changes for
// KindStart/KindConverse: kernel.Kernel.Seed's own updateStatus call (the
// same one Run always had) is what flips a session from "queued" to
// "running" in the first place, so a session only ever reaches "running"
// once it durably has at least its seed/message event — recovering an
// orphaned FRESH run is already exactly what this sweep covers.
func startQueueWorkers(ctx context.Context, redisClient *redis.Client, adminPool *pgxpool.Pool, port queue.Port, runsAPI *runsrest.Handler, ctl *runctl.Control, approvals approvalResumer, replier *chat.Replier, gate *cost.Gate) (stop func()) {
	lock := queueredis.NewSessionLock(redisClient, sessionLockTTL)
	runner := &queueRunner{ctl: ctl, approvals: approvals, runsAPI: runsAPI, replier: replier, gate: gate}

	// Two contexts, because stopping and cancelling are different things. leaseCtx
	// ends when the process is told to stop: workers stop leasing and the recovery
	// loops stop. jobCtx outlives it (it keeps ctx's values, not its cancellation)
	// and is what a leased job runs under, so a deploy lets runs already under way
	// finish instead of cutting every one off mid-turn; it is cancelled only when the
	// shutdown grace period runs out.
	workerCtx, cancel := context.WithCancel(ctx)
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	runctl.StartRecovery(workerCtx, func(ctx context.Context) ([]runctl.Orphan, error) { return listOrphans(ctx, adminPool) }, lock, port, orphanRecheckAfter)
	stopOrphanSweep := startOrphanSweep(workerCtx, adminPool, lock, port, queueredis.NewRecoveryThrottle(redisClient, orphanReenqueueEvery))

	// One breaker registry for every worker, so a session's failures are counted
	// together whichever worker ran them. Each worker used to build its own, and a
	// session's failures were split across them and never tripped any.
	breakers := reliability.NewBreakerRegistry(3)

	// One admission controller for every worker, so the process's concurrency limit
	// is a limit on the process and not on each worker.
	workerCount := queueWorkers()
	admission := queue.NewAdmissionController(maxConcurrentRuns(workerCount))

	var workers sync.WaitGroup
	for i := range workerCount {
		w := queue.NewWorker(queue.WorkerConfig{
			Port: port, Lock: lock, Runner: runner, Breakers: breakers, Admission: admission, RenewEvery: sessionLockTTL / 3, JobContext: jobCtx,
			Owner: fmt.Sprintf("nexusd-worker-%d-%d", os.Getpid(), i),
		})
		workers.Go(func() { w.Run(workerCtx) })
	}
	return func() {
		cancel() // stop leasing and stop the recovery loops
		stopOrphanSweep()
		// A worker mid-job finishes, up to the grace period, before the pool it leases
		// through closes; past it the runs are cancelled.
		drainWorkers(&workers, shutdownGrace(), cancelJobs)
		cancelJobs() // release jobCtx's resources; every job has returned by now
	}
}

// startOrphanSweep runs the recurring orphan sweep: every orphanSweepEvery, list the
// sessions a durable record says need a worker and re-enqueue the ones no worker is
// driving, at most once per orphanReenqueueEvery each. Its stop cancels the loop and
// waits for a pass in flight, so it is done before the pool it lists through closes.
func startOrphanSweep(ctx context.Context, adminPool *pgxpool.Pool, live runctl.Liveness, port queue.Port, throttle runctl.Throttle) (stop func()) {
	return startPeriodic(ctx, orphanSweepEvery, func(ctx context.Context) {
		candidates, err := listOrphans(ctx, adminPool)
		if err != nil {
			log.Error().Err(err).Str("pass", "periodic").Msg("runctl: list orphaned sessions failed")
			// listOrphans still returns what it found before the failure.
		}
		runctl.RecoverOrphans(ctx, "periodic", candidates, live, port, throttle)
	})
}

// staleJob reports whether err says the job's session is in a state that does not
// call for a worker: runctl.ErrSessionState, which Resume returns for a session
// that is terminal, suspended on an approval or awaiting the next message. A job
// that arrives at such a session (a copy a reclaimed lease made, a recovery sweep
// that raced a finish) has nothing to do, which is not a failure.
func staleJob(err error) bool {
	return err != nil && errors.Is(err, runctl.ErrSessionState)
}

// listOrphans is every session a durable record says needs a worker: the ones
// still marked running, and the ones whose approval was decided and never
// resumed.
func listOrphans(ctx context.Context, adminPool *pgxpool.Pool) ([]runctl.Orphan, error) {
	running, err := runctlpg.ListRunning(ctx, adminPool)
	if err != nil {
		return nil, err
	}
	unresumed, err := oversightpg.ListUnresumed(ctx, adminPool)
	if err != nil {
		return running, err // still recover what was found
	}
	decided, err := unresumedOrphans(unresumed)
	if err != nil {
		return running, err
	}
	return append(running, decided...), nil
}

// queueRunner implements queue.Runner over internal/runctl.Control.Resume —
// every Kind this queue carries dispatches through it identically (see
// startQueueWorkers' own doc comment); Fork/Cancel/TightenAutonomy/Steer
// are driven synchronously via REST instead of through the queue, since
// none of them drive the turn loop themselves.
type queueRunner struct {
	ctl       *runctl.Control
	approvals approvalResumer

	// runsAPI publishes every event this job's own turn loop produces — the
	// same PublishEvent call internal/runs/rest's own synchronous fast
	// path (publishUntilDone) makes, so an SSE client sees a queued run's
	// real output (and its eventual EventTerminal) regardless of which
	// process's worker actually drove it, and approval-outbox delivery/
	// terminal-span emission fire exactly once either way.
	runsAPI *runsrest.Handler

	// replier answers a chat session (telegram, zalo, email) with what its turn
	// produced, from here, the process that drove the turn, so a reply does not
	// depend on the process that took the webhook still being alive. Nil
	// delivers nothing.
	replier *chat.Replier

	// gate is the cost gate whose per-session spend this runner drops when a job
	// ends. Nil leaves it to the idle sweep.
	gate *cost.Gate
}

func (r *queueRunner) Run(ctx context.Context, job queue.Job) error {
	if r.gate != nil {
		// The gate's per-session spend is valid only while this process drives the
		// session. Another worker may lease its next job, so forget it here and let
		// the next reservation, wherever it runs, sum the ledger again.
		defer r.gate.ForgetSession(job.SessionID)
	}
	for ev, err := range jobEvents(ctx, r.ctl, r.approvals, job) {
		if staleJob(err) {
			// The session no longer needs a worker (it finished, or it is waiting on a
			// person, between this job being queued and it being leased). Nothing is
			// wrong, so no error goes to the session's event stream and the job
			// completes instead of being retried and dead-lettered.
			log.Info().Err(err).Any("job_id", job.JobID).Str("kind", string(job.Kind)).Any("session_id", job.SessionID).
				Msg("nexusd: queue: session no longer needs driving; completing the job without running it")
			return nil
		}
		if r.runsAPI != nil {
			r.runsAPI.PublishEvent(job.TenantID, job.SessionID, ev, err)
		}
		if r.replier != nil && err == nil {
			r.replier.OnEvent(ctx, job.TenantID, job.SessionID, ev)
		}
		if err != nil {
			return err
		}
	}

	// A crash-recovered session may ALSO be a delegation's child (README task
	// 8.10) or a team member (task 9.9) that just reached its own terminal
	// state via the resume above; whoever cares has registered as a
	// runctl.SettledObserver, and each is a no-op for every other session.
	r.ctl.NotifySettled(ctx, job.TenantID, job.SessionID)

	// A checkpoint after every leased job returns (README task 6.3) — a
	// durable pointer a FUTURE resume can consult fast, never the source of
	// truth for any one field.
	if err := r.ctl.SaveCheckpoint(ctx, job.TenantID, job.SessionID); err != nil {
		log.Error().Err(err).Any("job_id", job.JobID).Msg("nexusd: queue: save checkpoint after run failed")
	}
	return nil
}
