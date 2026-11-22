package cost

import (
	"time"

	"github.com/google/uuid"
)

// now is the Gate's clock.
func (g *Gate) now() time.Time {
	if g.cfg.Now != nil {
		return g.cfg.Now()
	}
	return time.Now()
}

// stale reports whether something read at loaded is due to be read again.
func (g *Gate) stale(loaded time.Time) bool {
	return g.cfg.CacheTTL > 0 && g.now().Sub(loaded) >= g.cfg.CacheTTL
}

// SweepIdle drops the per-session ceiling state last used before the given time
// and returns how many entries went. The state is rebuilt from the ledger the
// next time the session reserves, so this only bounds memory: without it every
// session the process ever served stays in the map for the life of the process.
func (g *Gate) SweepIdle(before time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	evicted := 0
	for id, st := range g.sessionState {
		if st.used.Before(before) {
			delete(g.sessionState, id)
			evicted++
		}
	}
	return evicted
}

// ForgetSession drops the in-memory ceiling state of one session, so the next
// reservation on this process sums the session's spend from the ledger again.
//
// That state is only valid while this process is the one driving the session. Any
// worker in any process can lease the session's next job, so a total kept past
// the end of a job is missing what the other workers spent in between, and the
// session could pass its ceiling by that much. The worker calls this when a job
// ends; summing once per job is one indexed query.
func (g *Gate) ForgetSession(sessionID uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sessionState, sessionID)
}
