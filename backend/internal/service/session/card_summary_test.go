package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// summaryStore records card-summary writes and mirrors them into the record.
type summaryStore struct {
	*fakeStore
	mu     sync.Mutex
	writes []string
}

func (s *summaryStore) UpdateSessionCardSummary(_ context.Context, id domain.SessionID, summary string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok || rec.IsTerminated {
		return false, nil
	}
	rec.Metadata.CardSummary = summary
	s.sessions[id] = rec
	s.writes = append(s.writes, summary)
	return true, nil
}

func (s *summaryStore) written() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.writes...)
}

func (s *summaryStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.GetSession(ctx, id)
}

// summaryCommander answers background tasks from a script and records them.
type summaryCommander struct {
	*fakeCommander
	mu       sync.Mutex
	prompts  []string
	times    []time.Time
	reply    func(call int, prompt string) (string, error)
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (c *summaryCommander) RunBackgroundTask(_ context.Context, _ domain.SessionID, _, prompt string) (string, error) {
	c.mu.Lock()
	call := len(c.prompts)
	c.prompts = append(c.prompts, prompt)
	c.times = append(c.times, time.Now())
	c.mu.Unlock()
	n := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	return c.reply(call, prompt)
}

func (c *summaryCommander) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.prompts...)
}

type summaryHarness struct {
	svc *Service
	st  *summaryStore
	cmd *summaryCommander
}

func newSummaryHarness(t *testing.T, reply func(call int, prompt string) (string, error)) *summaryHarness {
	t.Helper()
	st := &summaryStore{fakeStore: newFakeStore()}
	st.sessions["w-1"] = domain.SessionRecord{ID: "w-1", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Prompt: "Build a todo CLI with tests"}}
	cmd := &summaryCommander{fakeCommander: &fakeCommander{}, reply: reply}
	svc := &Service{store: st, manager: cmd, clock: time.Now}
	svc.cardSummaries = newCardSummarizer(svc)
	svc.cardSummaries.minGap = 40 * time.Millisecond
	return &summaryHarness{svc: svc, st: st, cmd: cmd}
}

func (h *summaryHarness) signal(kind domain.CardSignalKind, text string) {
	h.svc.ObserveCardSignal(context.Background(), "w-1", domain.CardSignal{Kind: kind, Text: text})
}

func (h *summaryHarness) turn(kind domain.CardSignalKind, stopped bool) {
	h.svc.ObserveCardSignal(context.Background(), "w-1", domain.CardSignal{Kind: kind, Stopped: stopped})
}

func (h *summaryHarness) sessionCount() int {
	h.svc.cardSummaries.mu.Lock()
	defer h.svc.cardSummaries.mu.Unlock()
	return len(h.svc.cardSummaries.sessions)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func steady(reply string) func(int, string) (string, error) {
	return func(int, string) (string, error) { return reply, nil }
}

// settle gives a timer that should not fire time to prove it did not.
func settle() { time.Sleep(150 * time.Millisecond) }

func TestFirstSummaryUsesOnlyTheTaskPrompt(t *testing.T) {
	h := newSummaryHarness(t, steady("Making a todo app for the terminal"))
	h.svc.requestFirstCardSummary("w-1")
	waitFor(t, "write", func() bool { return len(h.st.written()) == 1 })
	prompt := h.cmd.calls()[0]
	for _, want := range []string{"Build a todo CLI with tests", "just starting"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("first prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "What the agent did") {
		t.Errorf("first prompt carries activity it cannot have:\n%s", prompt)
	}
}

func TestOnlyWorkersWithAPromptGetACard(t *testing.T) {
	h := newSummaryHarness(t, steady("x"))
	rec := h.st.sessions["w-1"]
	rec.Metadata.Prompt = ""
	h.st.sessions["w-1"] = rec
	h.svc.requestFirstCardSummary("w-1") // promptless
	settle()
	rec.Metadata.Prompt, rec.Kind = "A task", domain.KindOrchestrator
	h.st.mu.Lock() // a timer goroutine may still be reading the store
	h.st.sessions["w-1"] = rec
	h.st.mu.Unlock()
	h.svc.requestFirstCardSummary("w-1") // not a worker
	settle()
	if len(h.cmd.calls()) != 0 || len(h.st.written()) != 0 {
		t.Fatalf("calls=%d writes=%v", len(h.cmd.calls()), h.st.written())
	}
}

func TestRefreshOnlyWhileATurnRunsAndRespectsTheGap(t *testing.T) {
	h := newSummaryHarness(t, steady("Writing the tests"))
	h.signal(domain.CardSignalActivity, "ls")
	settle()
	if len(h.cmd.calls()) != 0 {
		t.Fatalf("an idle session triggered a call")
	}
	h.turn(domain.CardSignalTurnStarted, false) // the earlier activity now counts
	waitFor(t, "first refresh", func() bool { return len(h.cmd.calls()) == 1 })
	h.signal(domain.CardSignalActivity, "go test ./...")
	waitFor(t, "second refresh", func() bool { return len(h.cmd.calls()) == 2 })
	h.cmd.mu.Lock()
	gap := h.cmd.times[1].Sub(h.cmd.times[0])
	h.cmd.mu.Unlock()
	if gap < h.svc.cardSummaries.minGap-5*time.Millisecond {
		t.Fatalf("refreshes %v apart, want at least %v", gap, h.svc.cardSummaries.minGap)
	}
}

func TestFinalSummaryIsPastTenseAndNeverWaitsBehindARefresh(t *testing.T) {
	h := newSummaryHarness(t, steady("Built the todo CLI"))
	h.svc.cardSummaries.minGap = 500 * time.Millisecond
	h.turn(domain.CardSignalTurnStarted, false)
	h.signal(domain.CardSignalActivity, "first")
	waitFor(t, "first call", func() bool { return len(h.cmd.calls()) == 1 })
	h.signal(domain.CardSignalActivity, "second") // now waits for the gap
	settle()
	h.signal(domain.CardSignalAssistant, "All nine tests pass.")
	h.turn(domain.CardSignalTurnCompleted, false)
	started := time.Now()
	waitFor(t, "final call", func() bool { return len(h.cmd.calls()) == 2 })
	if waited := time.Since(started); waited > 300*time.Millisecond {
		t.Fatalf("final summary waited %v behind a refresh timer", waited)
	}
	if last := h.cmd.calls()[1]; !strings.Contains(last, "has finished") || !strings.Contains(last, "All nine tests pass.") {
		t.Fatalf("final prompt wrong:\n%s", last)
	}
}

func TestStoppedTurnDoesNotClaimCompletion(t *testing.T) {
	h := newSummaryHarness(t, steady("Got as far as the parser tests"))
	h.turn(domain.CardSignalTurnStarted, false)
	h.turn(domain.CardSignalTurnCompleted, true)
	waitFor(t, "call", func() bool { return len(h.cmd.calls()) == 1 })
	if p := h.cmd.calls()[0]; !strings.Contains(p, "ended before it finished") || strings.Contains(p, "has finished") {
		t.Fatalf("stopped turn prompt wrong:\n%s", p)
	}
}

func TestOneCallPerSessionAndLateEvidenceIsNotLost(t *testing.T) {
	release := make(chan struct{})
	h := newSummaryHarness(t, func(call int, _ string) (string, error) {
		if call == 0 {
			<-release
		}
		return "line " + string(rune('a'+call)), nil
	})
	h.svc.cardSummaries.minGap = 0
	h.turn(domain.CardSignalTurnStarted, false)
	h.signal(domain.CardSignalActivity, "first edit")
	waitFor(t, "call in flight", func() bool { return len(h.cmd.calls()) == 1 })
	h.signal(domain.CardSignalActivity, "second edit")
	h.turn(domain.CardSignalTurnCompleted, false) // the final summary is owed too
	settle()
	if len(h.cmd.calls()) != 1 || h.cmd.peak.Load() != 1 {
		t.Fatalf("calls=%d peak=%d, want one in flight", len(h.cmd.calls()), h.cmd.peak.Load())
	}
	close(release)
	waitFor(t, "follow-up call", func() bool { return len(h.cmd.calls()) == 2 })
	if f := h.cmd.calls()[1]; !strings.Contains(f, "second edit") || strings.Contains(f, "first edit") || !strings.Contains(f, "has finished") {
		t.Fatalf("follow-up should carry only the late evidence, as a final summary:\n%s", f)
	}
}

func TestGlobalConcurrencyIsCappedAndNothingIsDropped(t *testing.T) {
	var done atomic.Int32
	h := newSummaryHarness(t, func(int, string) (string, error) {
		time.Sleep(60 * time.Millisecond)
		done.Add(1)
		return "ok", nil
	})
	const sessions = cardSummaryConcurrency + 2
	ids := make([]domain.SessionID, sessions)
	for i := range ids { // seed every record before any call can read the store
		ids[i] = domain.SessionID("w-" + string(rune('a'+i)))
		h.st.sessions[ids[i]] = domain.SessionRecord{ID: ids[i], Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Prompt: "task"}}
	}
	for _, id := range ids {
		h.svc.requestFirstCardSummary(id)
	}
	waitFor(t, "every session summarized", func() bool { return int(done.Load()) == sessions })
	if peak := h.cmd.peak.Load(); peak > cardSummaryConcurrency {
		t.Fatalf("peak concurrency %d exceeds cap %d", peak, cardSummaryConcurrency)
	}
}

func TestFailedCallsAreRetriedThenGiveUp(t *testing.T) {
	h := newSummaryHarness(t, func(call int, _ string) (string, error) {
		if call < 2 {
			return "", errors.New("provider unavailable")
		}
		return "Making a todo app", nil
	})
	h.svc.cardSummaries.minGap = 10 * time.Millisecond
	h.svc.requestFirstCardSummary("w-1")
	waitFor(t, "write after retries", func() bool { return len(h.st.written()) == 1 })

	down := newSummaryHarness(t, func(int, string) (string, error) { return "", errors.New("down") })
	down.svc.cardSummaries.minGap = 10 * time.Millisecond
	down.svc.requestFirstCardSummary("w-1")
	time.Sleep(500 * time.Millisecond)
	if got := len(down.cmd.calls()); got != 1+cardSummaryMaxRetries {
		t.Fatalf("calls = %d, want the first plus %d retries", got, cardSummaryMaxRetries)
	}
}

func TestAPanickingCallIsContained(t *testing.T) {
	h := newSummaryHarness(t, func(call int, _ string) (string, error) {
		if call == 0 {
			panic("provider bug")
		}
		return "Recovered", nil
	})
	h.svc.cardSummaries.minGap = 10 * time.Millisecond
	h.svc.requestFirstCardSummary("w-1")
	waitFor(t, "write after the panic", func() bool { return len(h.st.written()) == 1 })
}

func TestEndedSessionsAreDropped(t *testing.T) {
	h := newSummaryHarness(t, steady("Too late"))
	rec := h.st.sessions["w-1"]
	rec.IsTerminated = true
	h.st.sessions["w-1"] = rec
	h.svc.requestFirstCardSummary("w-1")
	settle()
	if len(h.st.written()) != 0 || h.sessionCount() != 0 {
		t.Fatalf("terminated session: writes=%v state=%d", h.st.written(), h.sessionCount())
	}

	// Forgetting cancels work that is still waiting on its timer.
	live := newSummaryHarness(t, steady("ok"))
	live.svc.cardSummaries.minGap = 200 * time.Millisecond
	live.turn(domain.CardSignalTurnStarted, false)
	live.signal(domain.CardSignalActivity, "one")
	waitFor(t, "first call", func() bool { return len(live.cmd.calls()) == 1 })
	live.signal(domain.CardSignalActivity, "two")
	live.svc.forgetCardSummary("w-1")
	time.Sleep(350 * time.Millisecond)
	if len(live.cmd.calls()) != 1 || live.sessionCount() != 0 {
		t.Fatalf("forgotten session: calls=%d state=%d", len(live.cmd.calls()), live.sessionCount())
	}
}

func TestSecretsAndCommitHashesNeverReachTheModelOrTheCard(t *testing.T) {
	h := newSummaryHarness(t, steady("Typography reads better (b5bd909) token=sk-live-abcdef123456"))
	h.turn(domain.CardSignalTurnStarted, false)
	h.signal(domain.CardSignalAssistant, "Committed a pass (b5bd909) on ao/dev-2/root")
	h.signal(domain.CardSignalActivity, "curl -H 'Authorization: Bearer abcdef1234567890' https://user:hunter2@api.example.com && git rev-parse f8323b9")
	waitFor(t, "write", func() bool { return len(h.st.written()) == 1 })
	prompt, card := h.cmd.calls()[0], h.st.written()[0]
	for _, leak := range []string{"b5bd909", "f8323b9", "abcdef1234567890", "hunter2"} {
		if strings.Contains(prompt, leak) {
			t.Errorf("prompt carries %q:\n%s", leak, prompt)
		}
	}
	if strings.Contains(card, "b5bd909") || strings.Contains(card, "sk-live") {
		t.Errorf("card carries a hash or secret: %q", card)
	}
}

func TestAssistantTextDoesNotCarryIntoTheNextTurn(t *testing.T) {
	h := newSummaryHarness(t, steady("ok"))
	h.turn(domain.CardSignalTurnStarted, false)
	h.signal(domain.CardSignalAssistant, "Old turn conclusion")
	h.turn(domain.CardSignalTurnCompleted, false)
	waitFor(t, "first turn call", func() bool { return len(h.cmd.calls()) == 1 })
	h.turn(domain.CardSignalTurnStarted, false)
	h.signal(domain.CardSignalActivity, "edit")
	waitFor(t, "second turn call", func() bool { return len(h.cmd.calls()) == 2 })
	if strings.Contains(h.cmd.calls()[1], "Old turn conclusion") {
		t.Fatalf("previous turn's message leaked into the next prompt:\n%s", h.cmd.calls()[1])
	}
}

func TestScrubAndCleanCardSummary(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"committed as 62e234a; flagged", "committed as ; flagged"},
		{"a pass (b5bd909): 60ch", "a pass : 60ch"},
		{"the deadbeef word and 1234567 number", "the deadbeef word and 1234567 number"},
		{"export GITHUB_TOKEN=ghp_abcdefghijklmnop", "export GITHUB_TOKEN=[redacted]"},
		{"git clone https://me:s3cret@github.com/o/r", "git clone https://[redacted]@github.com/o/r"},
		{"Authorization: Bearer abc.def-ghi_jkl1234", "[redacted]"},
		{"go test ./internal/service/...", "go test ./internal/service/..."},
	} {
		if got := scrub(tt.in); got != tt.want {
			t.Errorf("scrub(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for _, tt := range []struct{ name, in, want string }{
		{"quoted", "\"Writing the parser\"", "Writing the parser"},
		{"bullet and fence", "- `Writing the parser`", "Writing the parser"},
		{"first line only", "Writing the parser\nIt is going well.", "Writing the parser"},
		{"blank", "  \n ", ""},
		{"clipped", strings.Repeat("a", 300), strings.Repeat("a", cardSummaryMaxLine) + "…"},
	} {
		if got := cleanCardSummary(tt.in); got != tt.want {
			t.Errorf("%s: cleanCardSummary(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestSwitchedOffSummariesMakeNoCallsAndShowNothing(t *testing.T) {
	h := newSummaryHarness(t, steady("Making a todo app"))
	var on atomic.Bool
	h.svc.SetCardSummariesEnabled(on.Load)
	rec := h.st.sessions["w-1"]
	rec.Metadata.CardSummary = "An older summary"
	if got := h.svc.cardSummaryText(rec); got != "" {
		t.Fatalf("a switched-off card still shows %q", got)
	}
	h.svc.requestFirstCardSummary("w-1")
	h.turn(domain.CardSignalTurnStarted, false)
	h.signal(domain.CardSignalActivity, "go test ./...")
	settle()
	if len(h.cmd.calls()) != 0 || h.sessionCount() != 0 {
		t.Fatalf("switched off: calls=%d state=%d", len(h.cmd.calls()), h.sessionCount())
	}

	on.Store(true) // enabling takes effect without a restart
	if got := h.svc.cardSummaryText(rec); got != "An older summary" {
		t.Fatalf("an enabled card shows %q", got)
	}
	h.svc.requestFirstCardSummary("w-1")
	waitFor(t, "a summary once enabled", func() bool { return len(h.st.written()) == 1 })
}
