package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// defaultForceStopWait bounds how long ForceStop waits for a killed process
// tree to disappear before it reports the stop as unconfirmed.
const defaultForceStopWait = 3 * time.Second

// forceStopHooks are the operating-system edges of ForceStop, injectable so
// tests can model a runtime that leaves processes behind.
type forceStopHooks struct {
	identities func(context.Context) (map[int]procmem.Identity, error)
	kill       func(pid int) error
	wait       time.Duration
	poll       time.Duration
}

func defaultForceStopHooks() forceStopHooks {
	return forceStopHooks{
		identities: procmem.Identities,
		kill: func(pid int) error {
			process, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			// SIGKILL on Unix, TerminateProcess on Windows: neither can be
			// ignored, and SIGKILL also ends a stopped process.
			return process.Kill()
		},
		wait: defaultForceStopWait,
		poll: 100 * time.Millisecond,
	}
}

// capturedProcess is one reviewer process, identified by pid and start
// identity. The start identity survives exec (so a process that changes its
// command line is still ours) and changes when the pid is reused (so another
// process never is).
type capturedProcess struct {
	pid   int
	start string
}

// forceStopState remembers, per reviewer handle, the captured processes a
// force-stop could not confirm gone. A retry cannot rediscover them: the
// first attempt already tore down the runtime that knew their roots.
type forceStopState struct {
	mu        sync.Mutex
	survivors map[string][]capturedProcess
}

func (s *forceStopState) take(handleID string) []capturedProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.survivors[handleID]
}

func (s *forceStopState) keep(handleID string, survivors []capturedProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.survivors == nil {
		s.survivors = map[string][]capturedProcess{}
	}
	if len(survivors) == 0 {
		delete(s.survivors, handleID)
		return
	}
	s.survivors[handleID] = survivors
}

func (l *agentLauncher) forceStopHooks() forceStopHooks {
	if l.forceStop.identities == nil {
		return defaultForceStopHooks()
	}
	return l.forceStop
}

// ForceStop destroys a reviewer and every process in its tree, and reports
// whether the stop is confirmed. Destroying the runtime ends its host and AO's
// supervisor, but an agent process that cannot react (stopped, or ignoring
// its hangup) can outlive them, reparented and still working. So the tree is
// captured first, from one consistent reading of each process's parent and
// start identity; the runtime is torn down only once that capture is
// trustworthy; each survivor that is still the same process is killed; and the
// stop is confirmed only once every captured process is gone. A process whose
// identity cannot be read is never killed and never counted as gone.
// Processes not yet confirmed gone are carried into the next attempt.
func (l *agentLauncher) ForceStop(ctx context.Context, handleID string) (bool, error) {
	if handleID == "" {
		return true, nil
	}
	if reviewID, ok := reviewerChatID(handleID); ok && l.chat != nil {
		if err := l.chat.StopReviewChat(ctx, reviewID); err != nil {
			return false, err
		}
		return !l.chat.ReviewChatAlive(reviewID), nil
	}
	hooks := l.forceStopHooks()
	handle := ports.RuntimeHandle{ID: handleID}
	previous := l.stops.take(handleID)
	tree, captureErr := l.captureReviewerTree(ctx, handle, hooks)
	if captureErr != nil {
		if len(previous) == 0 {
			// Nothing trustworthy is known about this reviewer's processes:
			// tearing the runtime down now would lose its roots for good.
			return false, fmt.Errorf("capture reviewer process tree: %w", captureErr)
		}
		// An earlier attempt already tore the runtime down; what it captured
		// is what is left to stop.
		tree = nil
	}
	tree = mergeCaptured(previous, tree)
	destroyErr := l.runtime.Destroy(ctx, handle)
	stopped, err := l.stopCaptured(ctx, handleID, tree, hooks)
	if err != nil || !stopped {
		return false, errors.Join(err, destroyErr)
	}
	if destroyErr != nil {
		return false, destroyErr
	}
	return true, nil
}

// StopSurvivors kills only the processes an earlier force-stop of handleID
// captured and could not confirm gone, without touching the runtime: a newer
// reviewer may own that handle now. It reports whether none remain.
func (l *agentLauncher) StopSurvivors(ctx context.Context, handleID string) (bool, error) {
	return l.stopCaptured(ctx, handleID, l.stops.take(handleID), l.forceStopHooks())
}

func (l *agentLauncher) stopCaptured(ctx context.Context, handleID string, tree []capturedProcess, hooks forceStopHooks) (bool, error) {
	remaining, err := killTreeSurvivors(ctx, tree, hooks)
	l.stops.keep(handleID, remaining)
	if err != nil {
		return false, err
	}
	if len(remaining) > 0 {
		pids := make([]int, 0, len(remaining))
		for _, p := range remaining {
			pids = append(pids, p.pid)
		}
		return false, fmt.Errorf("reviewer processes not confirmed gone after force-stop: %v", pids)
	}
	return true, nil
}

// captureReviewerTree returns every process at or below the runtime's roots,
// from one Identities reading so tree membership and start identity describe
// the same processes. A runtime that reports no roots has nothing running. A
// member whose identity cannot be read makes the capture untrustworthy.
func (l *agentLauncher) captureReviewerTree(ctx context.Context, handle ports.RuntimeHandle, hooks forceStopHooks) ([]capturedProcess, error) {
	inspector, ok := l.runtime.(ports.RuntimeProcessRootInspector)
	if !ok {
		return nil, errors.New("runtime cannot report its process roots")
	}
	roots, err := inspector.ProcessRootPIDs(ctx, handle)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, nil
	}
	identities, err := hooks.identities(ctx)
	if err != nil {
		return nil, err
	}
	children := map[int][]int{}
	for pid, identity := range identities {
		children[identity.PPID] = append(children[identity.PPID], pid)
	}
	var tree []capturedProcess
	seen := map[int]bool{}
	pending := append([]int{}, roots...)
	for len(pending) > 0 {
		pid := pending[0]
		pending = pending[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		identity, ok := identities[pid]
		if !ok {
			continue // it exited
		}
		if !identity.Known() {
			return nil, fmt.Errorf("cannot read the identity of reviewer process %d", pid)
		}
		tree = append(tree, capturedProcess{pid: pid, start: identity.Start})
		pending = append(pending, children[pid]...)
	}
	return tree, nil
}

func mergeCaptured(previous, current []capturedProcess) []capturedProcess {
	seen := map[capturedProcess]bool{}
	var out []capturedProcess
	for _, p := range append(append([]capturedProcess{}, previous...), current...) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// killTreeSurvivors kills every captured process still running as the same
// process and waits for all of them to go. It returns the processes not
// confirmed gone: survivors, and any whose identity cannot be read now (never
// signalled, since they cannot be proven to be ours).
func killTreeSurvivors(ctx context.Context, tree []capturedProcess, hooks forceStopHooks) ([]capturedProcess, error) {
	if len(tree) == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(hooks.wait)
	killed := map[int]bool{}
	for {
		identities, err := hooks.identities(ctx)
		if err != nil {
			return tree, fmt.Errorf("read process identities: %w", err)
		}
		var remaining []capturedProcess
		for _, captured := range tree {
			current, ok := identities[captured.pid]
			switch {
			case !ok:
				continue // gone
			case !current.Known():
				remaining = append(remaining, captured) // unknown: neither ours to kill nor gone
				continue
			case current.Start != captured.start:
				continue // the pid was reused: ours is gone
			}
			remaining = append(remaining, captured)
			if !killed[captured.pid] {
				killed[captured.pid] = true
				_ = hooks.kill(captured.pid)
			}
		}
		if len(remaining) == 0 {
			return nil, nil
		}
		if !time.Now().Before(deadline) {
			return remaining, nil
		}
		timer := time.NewTimer(hooks.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return remaining, ctx.Err()
		case <-timer.C:
		}
	}
}
