package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// artifactsChangedTTL bounds how long a replica remembers that a session's
// committed artifacts changed, for a worker whose next WaitForWork lands
// elsewhere.
const artifactsChangedTTL = 10 * time.Minute

// workWaitTimeout bounds a single WaitForWork long-poll. A NOTIFY delivers work
// within milliseconds; this is only the fallback re-check cadence for the rare
// missed-notification or gated-turn case, keeping the loop correct without the
// old ~100ms busy-poll. It must stay well under the worker's HTTP client
// timeout (30s) and the control plane's per-request timeout.
const workWaitTimeout = 5 * time.Second

// workWaiters wakes workers that are blocked in WaitForWork the instant a turn
// or transport request is enqueued for their session. It mirrors the terminal
// output wake registry: notifications are accelerants, the durable claim query
// remains the source of truth.
type workWaiters struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
	// artifactsChanged holds when each session's committed artifacts last
	// changed without its worker, until its next WaitForWork reports it.
	artifactsChanged map[string]time.Time
}

func newWorkWaiters() *workWaiters {
	return &workWaiters{
		waiters:          make(map[string]map[chan struct{}]struct{}),
		artifactsChanged: make(map[string]time.Time),
	}
}

// markArtifactsChanged records that the session's committed artifacts changed
// without its worker, and forgets marks older than artifactsChangedTTL.
func (w *workWaiters) markArtifactsChanged(sessionID string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, at := range w.artifactsChanged {
		if now.Sub(at) > artifactsChangedTTL {
			delete(w.artifactsChanged, id)
		}
	}
	w.artifactsChanged[sessionID] = now
}

// takeArtifactsChanged reports and clears a recent mark for the session.
func (w *workWaiters) takeArtifactsChanged(sessionID string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	at, ok := w.artifactsChanged[sessionID]
	delete(w.artifactsChanged, sessionID)
	return ok && now.Sub(at) <= artifactsChangedTTL
}

// subscribe returns a coalescing wake channel for the session and a deregister
// func. The channel is buffered so a notify never blocks and never misses a
// wake that arrives between drains.
func (w *workWaiters) subscribe(sessionID string) (chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	w.mu.Lock()
	set := w.waiters[sessionID]
	if set == nil {
		set = make(map[chan struct{}]struct{})
		w.waiters[sessionID] = set
	}
	set[wake] = struct{}{}
	w.mu.Unlock()
	return wake, func() {
		w.mu.Lock()
		if set := w.waiters[sessionID]; set != nil {
			delete(set, wake)
			if len(set) == 0 {
				delete(w.waiters, sessionID)
			}
		}
		w.mu.Unlock()
	}
}

func (w *workWaiters) notify(sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for wake := range w.waiters[sessionID] {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// HandleWorkerWorkNotify is the Postgres NOTIFY callback for ao_worker_work; the
// payload is the session id whose turn/transport queue just gained a row, or
// that id after postgres.ArtifactsChangedNotifyPrefix when its committed
// artifacts changed without its worker. Every replica runs its own listener,
// so whichever one holds the waiting worker's long-poll wakes it; the others
// no-op, and remember an artifact change for a later wait.
func (s *Server) HandleWorkerWorkNotify(payload string) {
	if s.workWaiters == nil {
		return
	}
	sessionID, artifacts := strings.CutPrefix(payload, postgres.ArtifactsChangedNotifyPrefix)
	if artifacts {
		s.workWaiters.markArtifactsChanged(sessionID, time.Now())
	}
	s.workWaiters.notify(sessionID)
}

// workerWaitForWork blocks until the session has new work to claim (a turn or a
// transport request), or a short fallback timeout elapses. It replaces the
// worker's ~100ms busy-poll: the worker holds one request open and the control
// plane wakes it on enqueue. It never returns the work itself — the worker
// re-runs its atomic claim RPCs when this returns. ArtifactsChanged tells it
// to check its artifact files against the committed ones.
func (s *Server) workerWaitForWork(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	if s.workWaiters == nil {
		writeJSON(w, http.StatusOK, struct{}{})
		return
	}
	wake, cancel := s.workWaiters.subscribe(claims.SessionID)
	defer cancel()
	timer := time.NewTimer(workWaitTimeout)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
	case <-wake:
	case <-timer.C:
	}
	writeJSON(w, http.StatusOK, worker.WorkWaitResponse{
		ArtifactsChanged: s.workWaiters.takeArtifactsChanged(claims.SessionID, time.Now()),
	})
}
