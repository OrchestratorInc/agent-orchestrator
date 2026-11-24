package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/chat"
)

// skillfulConversation is a provider double that can enumerate skills.
type skillfulConversation struct {
	*fakeConversation
	skills []ports.ChatSkill
	err    error
	calls  int
}

func (c *skillfulConversation) ListSkills(context.Context) ([]ports.ChatSkill, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.skills, nil
}

func TestSkillsComeFromTheLiveConversation(t *testing.T) {
	conv := &skillfulConversation{
		fakeConversation: newFakeConversation(),
		skills: []ports.ChatSkill{
			{Name: "review", DisplayName: "review", Description: "Look at the diff", Source: "repo"},
		},
	}
	h := newHarnessWithConversation(t, conv)

	skills, err := h.svc.Skills(context.Background(), testSession)
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "review" {
		t.Fatalf("got %+v, want the provider's own list", skills)
	}
	if conv.calls != 1 {
		t.Errorf("provider asked %d times, want 1: the list must not be served from a table in AO", conv.calls)
	}
}

// A driver that cannot enumerate skills has to be distinguishable from one that
// reported none, because only the first is permanent.
func TestSkillsReportsUnsupportedForADriverThatCannotList(t *testing.T) {
	h := newHarness(t)

	_, err := h.svc.Skills(context.Background(), testSession)
	if !errorsIs(err, chatsvc.ErrSkillsUnsupported) {
		t.Fatalf("err = %v, want ErrSkillsUnsupported", err)
	}
}

// Without a controller there is no provider to ask. Reporting that plainly is what
// lets a client explain the state instead of showing an empty menu.
func TestSkillsRequiresALiveController(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, err := h.svc.Skills(context.Background(), testSession)
	if !errorsIs(err, chatsvc.ErrNoController) {
		t.Fatalf("err = %v, want ErrNoController", err)
	}
}

// deadlineConversation records whether the provider was asked with a deadline.
type deadlineConversation struct {
	*fakeConversation
	deadline time.Time
	bounded  bool
}

func (c *deadlineConversation) ListSkills(ctx context.Context) ([]ports.ChatSkill, error) {
	c.deadline, c.bounded = ctx.Deadline()
	return nil, nil
}

// A provider that never answers must not hold the request, and the composer's poll
// behind it, open for as long as the client stays connected.
func TestSkillsBoundsTheProviderRead(t *testing.T) {
	conv := &deadlineConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, conv)

	if _, err := h.svc.Skills(context.Background(), testSession); err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if !conv.bounded {
		t.Fatal("provider asked without a deadline")
	}
	if left := time.Until(conv.deadline); left > 10*time.Second {
		t.Fatalf("deadline %s away, want a few seconds", left)
	}
}
