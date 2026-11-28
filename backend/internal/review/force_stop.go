package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/procmem"
)

// defaultForceStopWait bounds how long ForceStop waits for a killed process
// tree to disappear before it reports the stop as unconfirmed.
const defaultForceStopWait = 3 * time.Second

// forceStopHooks are the operating-system edges of ForceStop, injectable so
// tests can model a runtime that leaves processes behind.
type forceStopHooks struct {
	identities func(context.Context) (map[int]procmem.Identity, error)
	kill       func(pid int) error
	// freeze stops a process without ending it (SIGSTOP), so it can no longer
	// spawn anything while its tree is captured. Nil where the platform has no
	// such signal (Windows, which keeps a dead parent's pid on its children
	// instead).
	freeze func(pid int) error
	wait   time.Duration
	poll   time.Duration
}

// maxFreezeRounds bounds how often ForceStop re-reads a tree to catch
// descendants spawned while it was freezing the rest.
const maxFreezeRounds = 32

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
		freeze: freezeProcess,
		wait:   defaultForceStopWait,
		poll:   100 * time.Millisecond,
	}
}

// capturedProcess is one reviewer process, identified by pid and start
// identity. The start identity survives exec (so a process that changes its
// command line is still ours) and changes when the pid is reused (so another
// process never is).
type capturedProcess struct {
	pid   int
	start string
	// startedAt is the wall-clock start, used to tell a captured process's
	// real children from a later process that reused a pid.
	startedAt time.Time
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
		// Nothing trustworthy is known about the runtime's current tree, and
		// tearing it down now would lose its roots for good. That holds even
		// with survivors from an earlier attempt: a torn-down runtime reports
		// no roots rather than an error, so a runtime that errors may be a
		// replacement reviewer on the same handle. Stop only the survivors
		// already known by identity, and leave the runtime for a later pass.
		if _, err := l.stopCaptured(ctx, handleID, previous, hooks); err != nil {
			captureErr = errors.Join(captureErr, err)
		}
		return false, fmt.Errorf("capture reviewer process tree: %w", captureErr)
	}
	tree = mergeCaptured(previous, tree)
	if hooks.freeze != nil {
		// The reviewer keeps running between the capture and its teardown and
		// may spawn a tool there, possibly in its own session. Once its
		// parent dies the tool is reparented and its ancestry lost, so freeze
		// the whole tree first: stopped processes cannot spawn, and their
		// children keep a live parent, so re-reading until nothing new shows
		// up yields every descendant.
		frozen, err := freezeTree(ctx, tree, hooks)
		if err != nil {
			l.stops.keep(handleID, frozen)
			return false, fmt.Errorf("freeze reviewer process tree: %w", err)
		}
		tree = frozen
	}
	// Kill the whole tree before tearing the runtime down, so nothing it
	// spawns can outlive the ownership roots unseen.
	stopped, err := l.stopCaptured(ctx, handleID, tree, hooks)
	destroyErr := l.runtime.Destroy(ctx, handle)
	if err != nil || !stopped {
		return false, errors.Join(err, destroyErr)
	}
	if destroyErr != nil {
		return false, destroyErr
	}
	return true, nil
}

// freezeTree stops every process in tree and keeps re-reading until no new
// descendant appears. A process is frozen only while its current start
// identity matches the captured one, so a pid that was reused (a survivor kept
// from an earlier attempt, say) is never stopped. A round that freezes nothing
// new read the tree with every live member already stopped, so nothing can
// have spawned since. It returns the frozen tree, or an error when the tree
// cannot be trusted or does not settle.
func freezeTree(ctx context.Context, tree []capturedProcess, hooks forceStopHooks) ([]capturedProcess, error) {
	frozen := map[int]bool{}
	for range maxFreezeRounds {
		identities, err := hooks.identities(ctx)
		if err != nil {
			return tree, fmt.Errorf("read process identities: %w", err)
		}
		if tree, err = extendTree(tree, identities); err != nil {
			return tree, err
		}
		froze := false
		for _, p := range tree {
			current, ok := identities[p.pid]
			if frozen[p.pid] || !ok || !current.Known() || current.Start != p.start {
				continue
			}
			frozen[p.pid] = true
			froze = true
			_ = hooks.freeze(p.pid)
		}
		if !froze {
			return tree, nil
		}
	}
	return tree, errors.New("reviewer process tree kept growing while being frozen")
}

// extendTree adds every descendant of tree that identities shows. A child of
// a captured process that is still alive (same start identity) is genuine.
// Windows keeps a dead parent's pid on its children, so a child whose
// captured parent is gone counts too, if it started at or after that parent
// did. When another process has since taken the parent's pid, the child must
// also have started before that replacement, or it is the replacement's. A
// possible child whose identity or parent cannot be read makes the tree
// untrustworthy.
func extendTree(tree []capturedProcess, identities map[int]procmem.Identity) ([]capturedProcess, error) {
	members := make(map[int]capturedProcess, len(tree))
	for _, p := range tree {
		members[p.pid] = p
	}
	for {
		added := false
		for pid, identity := range identities {
			if _, ok := members[pid]; ok {
				continue
			}
			if identity.PPID == procmem.UnknownPPID {
				return tree, fmt.Errorf("cannot read the parent of process %d", pid)
			}
			parent, ok := members[identity.PPID]
			if !ok {
				continue
			}
			if !identity.Known() {
				return tree, fmt.Errorf("cannot read the identity of reviewer process %d", pid)
			}
			if !childOf(parent, identity, identities) {
				continue
			}
			child := capturedProcess{pid: pid, start: identity.Start, startedAt: identity.StartedAt}
			members[pid] = child
			tree = append(tree, child)
			added = true
		}
		if !added {
			return tree, nil
		}
	}
}

// childOf reports whether a process naming parent's pid as its parent is
// really the captured parent's child, rather than a child of a later process
// that reused that pid.
func childOf(parent capturedProcess, child procmem.Identity, identities map[int]procmem.Identity) bool {
	parentNow, present := identities[parent.pid]
	if present && parentNow.Start == parent.start {
		return true
	}
	if child.StartedAt.IsZero() || parent.startedAt.IsZero() || child.StartedAt.Before(parent.startedAt) {
		return false
	}
	if !present {
		return true
	}
	// The pid belongs to a replacement now: only a child that started before
	// the replacement existed can be the original's.
	return !parentNow.StartedAt.IsZero() && child.StartedAt.Before(parentNow.StartedAt)
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
	bounded, err := l.reviewerRoots(ctx, handle)
	if err != nil {
		return nil, err
	}
	if len(bounded) == 0 {
		return nil, nil
	}
	identities, err := hooks.identities(ctx)
	if err != nil {
		return nil, err
	}
	var roots []int
	for _, root := range bounded {
		identity, ok := identities[root.PID]
		if !ok {
			continue // it exited
		}
		if !root.StartedBy.IsZero() {
			if identity.StartedAt.IsZero() {
				return nil, fmt.Errorf("cannot read when reviewer root %d started", root.PID)
			}
			if identity.StartedAt.After(root.StartedBy.Add(rootStartSlack)) {
				// The runtime's process exited and another one took its pid:
				// nothing below it is the reviewer's.
				continue
			}
		}
		roots = append(roots, root.PID)
	}
	children := map[int][]int{}
	for pid, identity := range identities {
		if identity.PPID == procmem.UnknownPPID {
			// It could be one of the reviewer's processes, and nothing would
			// stop it: the tree cannot be trusted.
			return nil, fmt.Errorf("cannot read the parent of process %d", pid)
		}
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
		tree = append(tree, capturedProcess{pid: pid, start: identity.Start, startedAt: identity.StartedAt})
		pending = append(pending, children[pid]...)
	}
	return tree, nil
}

// rootStartSlack absorbs the coarseness of start-time sources (Linux derives
// wall-clock start from the boot time; registry bounds are whole seconds)
// when checking a root against the latest moment it can have started.
const rootStartSlack = 2 * time.Second

// reviewerRoots returns the runtime's process-tree roots, with start bounds
// when the runtime can vouch for them.
func (l *agentLauncher) reviewerRoots(ctx context.Context, handle ports.RuntimeHandle) ([]ports.ProcessRoot, error) {
	if inspector, ok := l.runtime.(ports.RuntimeProcessRootBoundInspector); ok {
		return inspector.BoundedProcessRoots(ctx, handle)
	}
	inspector, ok := l.runtime.(ports.RuntimeProcessRootInspector)
	if !ok {
		return nil, errors.New("runtime cannot report its process roots")
	}
	pids, err := inspector.ProcessRootPIDs(ctx, handle)
	if err != nil {
		return nil, err
	}
	roots := make([]ports.ProcessRoot, 0, len(pids))
	for _, pid := range pids {
		roots = append(roots, ports.ProcessRoot{PID: pid})
	}
	return roots, nil
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
		// Anything a captured process spawned before it died is the
		// reviewer's too.
		if tree, err = extendTree(tree, identities); err != nil {
			return tree, err
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
