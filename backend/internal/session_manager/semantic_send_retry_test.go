package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Issue #6931: when AO typed a report but the agent never acknowledged it, the
// report coordinator must be able to tell that apart from "nothing was sent",
// because only the first case puts another copy in front of the agent.
func TestSendSemanticTUITimeoutSaysTheMessageWasWritten(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)
	m.semanticAcceptTimeout = 20 * time.Millisecond

	err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:unaccepted")
	if !errors.Is(err, domain.ErrReportDeliveryUnacknowledged) {
		t.Fatalf("SendSemantic error = %v, want ErrReportDeliveryUnacknowledged", err)
	}
	if !errors.Is(err, ErrSemanticAcceptanceUnsupported) {
		t.Fatalf("SendSemantic error = %v, want it to keep ErrSemanticAcceptanceUnsupported", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("pane writes = %d, want 1", len(msg.msgs))
	}
}
