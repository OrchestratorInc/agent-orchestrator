package sessionmanager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// childProbeRuntime adds the child-process probe the production runtime has.
type childProbeRuntime struct {
	*fakeRuntime
	childAlive bool
}

func (r *childProbeRuntime) IsChildAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return r.childAlive, nil
}

func TestSwitchTargetThatExitsAtOnceIsReportedWithItsLastLine(t *testing.T) {
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })

	rec := domain.SessionRecord{ID: "s-1", Metadata: domain.SessionMetadata{RuntimeHandleID: "handle-1"}}
	dead := &childProbeRuntime{fakeRuntime: &fakeRuntime{outputs: []string{"starting\nError: Session ID 123 is already in use.\n\n"}}}
	err := (&Manager{runtime: dead}).confirmTUISwitchTargetAlive(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "Error: Session ID 123 is already in use.") {
		t.Fatalf("error = %v, want the agent's last line", err)
	}

	alive := &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}
	if err := (&Manager{runtime: alive}).confirmTUISwitchTargetAlive(context.Background(), rec); err != nil {
		t.Fatalf("a running target was reported as failed: %v", err)
	}
	// A runtime that cannot say whether the child is alive never fails a switch.
	if err := (&Manager{runtime: &fakeRuntime{}}).confirmTUISwitchTargetAlive(context.Background(), rec); err != nil {
		t.Fatalf("a runtime without the probe failed the switch: %v", err)
	}
}
