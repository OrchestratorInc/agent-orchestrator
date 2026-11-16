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
// tree to disappear from the process table before it reports the stop as
// unconfirmed.
const defaultForceStopWait = 3 * time.Second

// forceStopHooks are the operating-system edges of ForceStop, injectable so
// tests can model a runtime that leaves processes behind.
type forceStopHooks struct {
	snapshot func(context.Context) (*procmem.Table, error)
	starts   func(context.Context) (map[int]string, error)
	kill     func(pid int) error
	wait     time.Duration
	poll     time.Duration
}

func defaultForceStopHooks() forceStopHooks {
	return forceStopHooks{
		snapshot: func(ctx context.Context) (*procmem.Table, error) { return procmem.Snapshot(ctx, nil) },
		starts:   func(ctx context.Context) (map[int]string, error) { return procmem.StartTimes(ctx, nil) },
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

// capturedProcess is one reviewer process, identified by pid and start time.
// The start time survives exec (so a process that changes its command line is
// still ours) and changes when the pid is reused (so another process never
// is).
type capturedProcess struct {
	pid   int
	start string
}

// forceStopState remembers, per reviewer handle, the captured processes a
// force-stop could not confirm dead. A retry cannot rediscover them: the first
// attempt already tore down the runtime that knew their roots.
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

// ForceStop destroys a reviewer and every process in its tree, and reports
// whether the stop is confirmed. Destroying the runtime ends its host and AO's
// supervisor, but an agent process that cannot react (stopped, or ignoring
// its hangup) can outlive them, reparented and still working. So the tree is
// captured before teardown, each survivor that is still the same process (same
// pid and start time) is killed outright, and the stop is confirmed only once
// none of them remains. Processes it could not confirm dead are carried into
// the next attempt. When the tree cannot be captured the stop is not
// confirmed: wrapper liveness alone proves nothing about the agent.
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
	hooks := l.forceStop
	if hooks.snapshot == nil {
		hooks = defaultForceStopHooks()
	}
	handle := ports.RuntimeHandle{ID: handleID}
	previous := l.stops.take(handleID)
	tree, captureErr := l.captureReviewerTree(ctx, handle, hooks)
	if captureErr != nil && len(previous) > 0 {
		// An earlier attempt already tore the runtime down, so its roots are
		// gone; what that attempt captured is what is left to stop.
		captureErr = nil
	}
	tree = mergeCaptured(previous, tree)
	destroyErr := l.runtime.Destroy(ctx, handle)
	survivors, err := killTreeSurvivors(ctx, tree, hooks)
	l.stops.keep(handleID, survivors)
	if err != nil {
		return false, errors.Join(err, destroyErr)
	}
	if captureErr != nil {
		return false, errors.Join(fmt.Errorf("capture reviewer process tree: %w", captureErr), destroyErr)
	}
	if len(survivors) > 0 {
		pids := make([]int, 0, len(survivors))
		for _, p := range survivors {
			pids = append(pids, p.pid)
		}
		return false, errors.Join(destroyErr, fmt.Errorf("reviewer processes still alive after force-stop: %v", pids))
	}
	if destroyErr != nil {
		return false, destroyErr
	}
	return true, nil
}

// captureReviewerTree returns every process at or below the runtime's roots,
// with its start time. A runtime that reports no roots has nothing running.
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
	table, err := hooks.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	starts, err := hooks.starts(ctx)
	if err != nil {
		return nil, err
	}
	var tree []capturedProcess
	for _, p := range table.Tree(roots...).Processes {
		start, ok := starts[p.PID]
		if !ok {
			// It exited between the two readings.
			continue
		}
		tree = append(tree, capturedProcess{pid: p.PID, start: start})
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
// process and waits for all of them to go. It returns the ones that never did.
func killTreeSurvivors(ctx context.Context, tree []capturedProcess, hooks forceStopHooks) ([]capturedProcess, error) {
	if len(tree) == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(hooks.wait)
	killed := map[int]bool{}
	for {
		starts, err := hooks.starts(ctx)
		if err != nil {
			return tree, fmt.Errorf("read process start times: %w", err)
		}
		var survivors []capturedProcess
		for _, captured := range tree {
			// A pid with another start time was reused: not ours. The same
			// start time is the same process, whatever it has exec'd into.
			if starts[captured.pid] != captured.start {
				continue
			}
			survivors = append(survivors, captured)
			if !killed[captured.pid] {
				killed[captured.pid] = true
				_ = hooks.kill(captured.pid)
			}
		}
		if len(survivors) == 0 {
			return nil, nil
		}
		if !time.Now().Before(deadline) {
			return survivors, nil
		}
		timer := time.NewTimer(hooks.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return survivors, ctx.Err()
		case <-timer.C:
		}
	}
}
