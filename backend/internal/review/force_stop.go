package review

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	kill     func(pid int) error
	wait     time.Duration
	poll     time.Duration
}

func defaultForceStopHooks() forceStopHooks {
	return forceStopHooks{
		snapshot: func(ctx context.Context) (*procmem.Table, error) { return procmem.Snapshot(ctx, nil) },
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

// ForceStop destroys a reviewer and every process in its tree, and reports
// whether the stop is confirmed. Destroying the runtime ends its host and AO's
// supervisor, but an agent process that cannot react (stopped, or ignoring
// its hangup) can outlive them, reparented and still working. So the tree is
// captured before teardown, each survivor that is still the same process (same
// pid and command line) is killed outright, and the stop is confirmed only
// once none of them remains. When the tree cannot be captured the stop is not
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
	tree, captureErr := l.captureReviewerTree(ctx, handle, hooks)
	destroyErr := l.runtime.Destroy(ctx, handle)
	if captureErr != nil {
		return false, errors.Join(fmt.Errorf("capture reviewer process tree: %w", captureErr), destroyErr)
	}
	survivors, err := killTreeSurvivors(ctx, tree, hooks)
	if err != nil {
		return false, errors.Join(err, destroyErr)
	}
	if len(survivors) > 0 {
		return false, errors.Join(destroyErr, fmt.Errorf("reviewer processes still alive after force-stop: %v", survivors))
	}
	if destroyErr != nil {
		return false, destroyErr
	}
	return true, nil
}

// captureReviewerTree returns every process at or below the runtime's roots.
// A runtime that reports no roots has nothing running.
func (l *agentLauncher) captureReviewerTree(ctx context.Context, handle ports.RuntimeHandle, hooks forceStopHooks) ([]procmem.Process, error) {
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
	return table.Tree(roots...).Processes, nil
}

// killTreeSurvivors kills every captured process still running as the same
// process and waits for all of them to leave the process table. It returns
// the pids that never did.
func killTreeSurvivors(ctx context.Context, tree []procmem.Process, hooks forceStopHooks) ([]int, error) {
	if len(tree) == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(hooks.wait)
	killed := map[int]bool{}
	for {
		table, err := hooks.snapshot(ctx)
		if err != nil {
			return nil, fmt.Errorf("read process table: %w", err)
		}
		current := map[int]procmem.Process{}
		for _, process := range table.All() {
			current[process.PID] = process
		}
		var survivors []int
		for _, captured := range tree {
			now, ok := current[captured.PID]
			// A pid now running another command was reused: not ours.
			if !ok || now.Command != captured.Command {
				continue
			}
			survivors = append(survivors, captured.PID)
			if !killed[captured.PID] {
				killed[captured.PID] = true
				_ = hooks.kill(captured.PID)
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
