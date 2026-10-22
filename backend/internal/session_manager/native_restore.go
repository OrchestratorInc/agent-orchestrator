package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

const nativeRestoreTimeout = 30 * time.Second

func (m *Manager) initializeNativeRestore(parent context.Context, original domain.SessionRecord, handle ports.RuntimeHandle, launchID string, plan ports.NativeRestoreInitialization) error {
	ctx, cancel := context.WithTimeout(parent, nativeRestoreTimeout)
	defer cancel()
	rendered, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return errors.New("native restore requires current rendered terminal output")
	}
	poll := func(check func(context.Context, string, string) (bool, error)) error {
		for {
			rec, err := m.getRecord(ctx, original.ID)
			if err != nil {
				return err
			}
			if rec.IsTerminated || rec.Metadata.RuntimeHandleID != handle.ID || rec.Metadata.RuntimeLaunchID != launchID {
				return errors.New("native restore generation lost")
			}
			alive, err := m.exactLaunchAlive(ctx, handle, original.ID, launchID)
			if err != nil {
				return err
			}
			if !alive {
				return errors.New("native restore process exited")
			}
			output, err := rendered.GetStyledOutput(ctx, handle, 200)
			if err != nil {
				return fmt.Errorf("native restore terminal: %w", err)
			}
			ready, err := check(ctx, launchID, output)
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
			if err := sleepContext(ctx, 100*time.Millisecond); err != nil {
				return err
			}
		}
	}
	if err := poll(plan.Ready); err != nil {
		return err
	}
	fence := m.exactLaunchPreWrite(original.ID, original.Harness, handle, launchID, errors.New("native restore generation lost before input"))
	outcome, err := m.messenger.DeliverUnderMutationChecked(ctx, original.ID, plan.ResumeInput(), func(writeCtx context.Context, current domain.SessionRecord) error {
		if err := fence(writeCtx, current); err != nil {
			return err
		}
		output, err := rendered.GetStyledOutput(writeCtx, handle, 200)
		if err != nil {
			return err
		}
		ready, err := plan.Ready(writeCtx, launchID, output)
		if err != nil {
			return err
		}
		if !ready {
			return errors.New("native readiness lost before resume input")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if outcome != sessionguard.Sent {
		return fmt.Errorf("native resume input suppressed: %v", outcome)
	}
	if err := poll(plan.Restored); err != nil {
		return err
	}
	if err := m.lcm.ApplyActivitySignal(ctx, original.ID, ports.ActivitySignal{ExpectedHarness: original.Harness, LaunchID: launchID, AgentSessionID: plan.TargetID(), Event: "session-start", Valid: true, State: domain.ActivityIdle, Timestamp: m.clock()}); err != nil {
		return err
	}
	rec, err := m.getRecord(ctx, original.ID)
	if err != nil {
		return err
	}
	if rec.IsTerminated || rec.Metadata.RuntimeHandleID != handle.ID || rec.Metadata.RuntimeLaunchID != launchID || rec.Metadata.AgentSessionID != plan.TargetID() || rec.Metadata.AgentSessionIDLaunchID != launchID {
		return errors.New("native restore identity publication not confirmed")
	}
	return nil
}

func (m *Manager) rollbackNativeInitialization(ctx context.Context, original domain.SessionRecord, handle ports.RuntimeHandle, launchID string) error {
	rec, err := m.getRecord(ctx, original.ID)
	if err != nil {
		return err
	}
	if rec.Metadata.RuntimeHandleID != handle.ID || rec.Metadata.RuntimeLaunchID != launchID {
		return errors.New("native restore cleanup ownership lost")
	}
	destroyErr := m.runtime.Destroy(ctx, handle)
	if destroyErr != nil {
		destroyErr = fmt.Errorf("native restore cleanup: %w", destroyErr)
	}
	var rollbackErr error
	if original.IsTerminated {
		rollbackErr = m.lcm.MarkTerminated(ctx, original.ID)
	} else {
		rollbackErr = m.recordAgentExited(ctx, rec)
	}
	if rollbackErr != nil {
		rollbackErr = fmt.Errorf("native restore quarantine: %w", rollbackErr)
	}
	return errors.Join(destroyErr, rollbackErr)
}

// exactLaunchAlive reports whether the exact AO supervisor for launchID, and
// its managed agent child, are still alive in handle.
func (m *Manager) exactLaunchAlive(ctx context.Context, handle ports.RuntimeHandle, id domain.SessionID, launchID string) (bool, error) {
	if strings.TrimSpace(handle.ID) == "" || id == "" || launchID == "" {
		return false, errors.New("target runtime handle, session, and launch are required")
	}
	inspector, ok := m.runtime.(ports.ExactSupervisedProcessInspector)
	if !ok {
		return false, errors.New("runtime cannot inspect an exact supervised launch")
	}
	return inspector.IsExactSupervisedProcessAlive(ctx, handle, ports.SupervisedProcessRef{SessionID: id, LaunchID: launchID})
}

// launchPollInterval paces the checks for a supervised launch's liveness.
const launchPollInterval = 150 * time.Millisecond

// waitForExactLaunch waits, up to launchStartWait, for the supervisor of one
// exact launch to report its agent alive. Seeing the runtime accept the start
// only proves the supervisor was created.
func (m *Manager) waitForExactLaunch(ctx context.Context, handle ports.RuntimeHandle, id domain.SessionID, launchID string) (bool, error) {
	if strings.TrimSpace(handle.ID) == "" || launchID == "" {
		return false, errors.New("target runtime handle and launch are required")
	}
	deadline := time.NewTimer(max(m.launchStartWait, launchPollInterval))
	defer deadline.Stop()
	ticker := time.NewTicker(launchPollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: handle, SessionID: id, Generation: launchID})
		if probe.Liveness == ports.FencedAlive {
			return true, nil
		}
		lastErr = nil
		if probe.Liveness == ports.FencedUnknown {
			lastErr = fmt.Errorf("launch ownership probe is unknown: %s", probe.Reason)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, lastErr
		case <-ticker.C:
		}
	}
}

// exactLaunchPreWrite closes the final check-then-write gap: the guard calls
// it after its durable read and immediately before the runtime write, and a
// matching row is not enough, the exact launch must still be alive.
func (m *Manager) exactLaunchPreWrite(id domain.SessionID, harness domain.AgentHarness, handle ports.RuntimeHandle, launchID string, ownershipErr error) func(context.Context, domain.SessionRecord) error {
	return func(ctx context.Context, current domain.SessionRecord) error {
		if current.ID != id || current.Harness != harness || current.Metadata.RuntimeHandleID != handle.ID || current.Metadata.RuntimeLaunchID != launchID {
			return ownershipErr
		}
		alive, err := m.exactLaunchAlive(ctx, handle, id, launchID)
		if err == nil && !alive {
			err = ownershipErr
		}
		return err
	}
}
