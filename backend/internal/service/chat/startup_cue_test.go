package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStartupCueReleasePreservesFIFOAgainstConcurrentNewSend(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	run := domain.StartupCueRun{Name: "Setup", State: "running", StartedAt: time.Now()}
	if _, err := h.st.ClaimStartupCue(ctx, testSession, run); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"opening", "queued"} {
		turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: text, Origin: domain.MessageOriginHuman})
		if err != nil || turn.State != domain.TurnStateQueued {
			t.Fatalf("queue: %+v %v", turn, err)
		}
	}
	if got := h.conv.sentTexts(); len(got) != 0 {
		t.Fatalf("provider got %v during hold", got)
	}
	run.State = "failed"
	if err := h.st.FinishStartupCue(ctx, testSession, run); err != nil {
		t.Fatal(err)
	}
	// Arrives after the durable hold clears, before the release worker drains.
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "new", Origin: domain.MessageOriginHuman})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("new send: %+v %v", turn, err)
	}
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "opening" {
		t.Fatalf("FIFO violated: %v", got)
	}
}
