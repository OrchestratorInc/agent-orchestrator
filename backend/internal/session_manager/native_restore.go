package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

const nativeRestoreTimeout = 30 * time.Second

func (m *Manager) initializeNativeRestore(parent context.Context, original domain.SessionRecord, handle ports.RuntimeHandle, launchID string, plan ports.NativeRestoreInitialization) error {
	ctx, cancel := context.WithTimeout(parent, nativeRestoreTimeout)
	defer cancel()
	poll := func(check func(context.Context, string, string) (bool, error)) error {
		for {
			rec, err := m.getRecord(ctx, original.ID)
			if err != nil {
				return err
			}
			if rec.IsTerminated || rec.Metadata.RuntimeHandleID != handle.ID || rec.Metadata.RuntimeLaunchID != launchID {
				return errors.New("native restore generation lost")
			}
			alive, err := m.exactTargetGenerationAlive(ctx, handle, original.ID, domain.AgentGenerationID(launchID))
			if err != nil {
				return err
			}
			if !alive {
				return errors.New("native restore process exited")
			}
			output, err := m.runtime.GetOutput(ctx, handle, 200)
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
	fence := m.exactGenerationPreWrite(original.ID, original.Harness, handle, domain.AgentGenerationID(launchID), errors.New("native restore generation lost before input"))
	outcome, err := m.messenger.DeliverUnderMutationChecked(ctx, original.ID, plan.ResumeInput(), func(writeCtx context.Context, current domain.SessionRecord) error {
		if err := fence(writeCtx, current); err != nil {
			return err
		}
		output, err := m.runtime.GetOutput(writeCtx, handle, 200)
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
	if err := m.runtime.Destroy(ctx, handle); err != nil {
		return fmt.Errorf("native restore cleanup: %w", err)
	}
	if original.IsTerminated {
		return m.lcm.MarkTerminated(ctx, original.ID)
	}
	return m.recordAgentExited(ctx, rec)
}
