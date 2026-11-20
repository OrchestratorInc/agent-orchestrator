package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// budgetRecordingChat records how long the transition let each Chat start run.
type budgetRecordingChat struct {
	*transitionChat
	budgets []time.Duration
	modes   []ports.ChatHistoryMode
}

func (c *budgetRecordingChat) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	budget := time.Duration(-1)
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
	}
	c.budgets = append(c.budgets, budget)
	c.modes = append(c.modes, cfg.HistoryMode)
	return c.transitionChat.StartChat(ctx, cfg)
}

// A TUI -> Chat target imports native history. Its settle wait starts only
// after the provider is up and has made its first load, so the target start
// must get that wait on top of the ordinary step budget. Sharing one step let
// a slow launch spend the wait, and the refresh that would have settled was
// cancelled mid-load (#7697).
func TestInterfaceTransitionNativeHistoryStartGetsSettleWaitOnTopOfStep(t *testing.T) {
	manager, store, _, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	recording := &budgetRecordingChat{transitionChat: chat}
	manager.chat = recording

	transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeChat, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, store, transition.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, error = %s", settled.Phase, settled.ErrorDetail)
	}
	if len(recording.budgets) != 1 || recording.modes[0] != ports.ChatHistoryRequired {
		t.Fatalf("chat starts = %v (history modes %v), want one native-history start", recording.budgets, recording.modes)
	}
	// The start itself runs well within a second, so anything above the bare
	// step budget proves the settle wait was added on top of it.
	if got, floor := recording.budgets[0], interfaceTransitionStepLimit+ports.ChatNativeHistorySettleLimit-5*time.Second; got < floor {
		t.Fatalf("native-history target start budget = %v, want at least %v (step %v + settle %v)",
			got, floor, interfaceTransitionStepLimit, ports.ChatNativeHistorySettleLimit)
	}
}

// A target with nothing to replay keeps the ordinary step budget: only the
// native-history import has a settle wait to make room for.
func TestInterfaceTransitionFreshChatStartKeepsStepBudget(t *testing.T) {
	manager, store, runtime, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	useFastInterfaceTransitionTimings(manager)
	// An untouched native session proves there is nothing to import, so the
	// Chat target starts fresh.
	manager.agents = singleAgent{agent: untouchedEmptyTransitionAgent{}}
	runtime.aliveByHandle = map[string]bool{"runtime-1": true}
	recording := &budgetRecordingChat{transitionChat: chat}
	manager.chat = recording

	transition, err := manager.StartInterfaceTransition(context.Background(), "session-1",
		domain.SessionModeChat, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, store, transition.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s, error = %s", settled.Phase, settled.ErrorDetail)
	}
	if len(recording.budgets) != 1 || recording.modes[0] == ports.ChatHistoryRequired {
		t.Fatalf("chat starts = %v (history modes %v), want one fresh start", recording.budgets, recording.modes)
	}
	if got := recording.budgets[0]; got <= 0 || got > interfaceTransitionStepLimit {
		t.Fatalf("fresh target start budget = %v, want the step budget %v", got, interfaceTransitionStepLimit)
	}
}
