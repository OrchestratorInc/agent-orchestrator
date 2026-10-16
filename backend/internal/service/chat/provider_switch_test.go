package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestProviderSwitchPublishesApproximateBranch(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()

	// Give the source a complete durable exchange so the target assertion proves
	// the bootstrap came from AO's canonical rows, not provider scrollback.
	completeTurn(t, h, "keep this context", "provider-turn-1")
	h.awaitSnapshot(t, func(snapshot store.ConversationSnapshot) bool {
		return len(snapshot.Messages) == 2
	})

	// A turn this session had in the Terminal UI lives in the terminal turn
	// table. The note is built from both stores, merged by time.
	earlier := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC) // before the harness clock
	if err := h.st.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
		SessionID: testSession, Harness: domain.HarnessCodex, UserRequest: "terminal ask", RequestedAt: earlier,
	}); err != nil {
		t.Fatalf("record terminal request: %v", err)
	}
	if err := h.st.RecordTerminalTurnResponse(ctx, testSession, "terminal answer", earlier.Add(time.Minute)); err != nil {
		t.Fatalf("record terminal response: %v", err)
	}

	if source, err := h.st.ConversationForSession(ctx, testSession); err != nil {
		t.Fatalf("read conversation: %v", err)
	} else if err := h.st.RecordUsage(ctx, source.ID, domain.ConversationUsage{ContextUsed: 26956, ContextWindow: 475000}); err != nil {
		t.Fatalf("record source usage: %v", err)
	}
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("read conversation before the switch: %v", err)
	}
	if source, found, err := h.st.GetSession(ctx, testSession); err != nil || !found || source.Harness != domain.HarnessCodex {
		t.Fatalf("source session = %+v found=%v err=%v, want a Codex session", source, found, err)
	}
	if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness:     domain.HarnessClaudeCode,
		IdempotencyKey:    "switch-once",
		AutoCompactWindow: "1m",
	}); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}

	driver.mu.Lock()
	if len(driver.startConfigs) < 2 {
		driver.mu.Unlock()
		t.Fatalf("target start was not observed: %d starts", len(driver.startConfigs))
	}
	targetConfig := driver.startConfigs[len(driver.startConfigs)-1]
	driver.mu.Unlock()
	if after, err := h.st.ConversationForSession(ctx, testSession); err != nil || after.Usage != nil && after.Usage.ContextWindow != 0 {
		t.Fatalf("usage after the switch = %+v, %v; want the source's reading cleared", after.Usage, err)
	}
	if got := targetConfig.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
		t.Fatalf("target compaction window = %q, want the switch's 1m", got)
	}
	if !strings.Contains(targetConfig.SystemPrompt, "keep this context") {
		t.Fatalf("target system prompt did not contain AO handoff context: %q", targetConfig.SystemPrompt)
	}
	for _, want := range []string{domain.HandoffNotePrefix, "terminal ask", "terminal answer", "## Latest turn"} {
		if !strings.Contains(targetConfig.SystemPrompt, want) {
			t.Fatalf("target handoff note is missing %q: %q", want, targetConfig.SystemPrompt)
		}
	}
	if strings.Index(targetConfig.SystemPrompt, "terminal ask") > strings.Index(targetConfig.SystemPrompt, "keep this context") {
		t.Fatalf("the older terminal turn must come before the latest chat turn: %q", targetConfig.SystemPrompt)
	}
	history, total, err := h.svc.SessionHistory(ctx, testSession, 0, 10)
	if err != nil || total != 2 || len(history) != 2 ||
		history[0].UserRequest != "keep this context" || history[1].UserRequest != "terminal ask" {
		t.Fatalf("session history = %+v (total %d), %v; want the chat turn then the terminal turn", history, total, err)
	}
	// A page past the end is empty, not nil: the HTTP layer serialises it as [].
	if past, total, err := h.svc.SessionHistory(ctx, testSession, 5, 10); err != nil || total != 2 || past == nil || len(past) != 0 {
		t.Fatalf("history past the end = %#v (total %d), %v; want an empty page", past, total, err)
	}

	// A switch is not replayed: asking again for the provider that now owns
	// the session is refused.
	err = h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness:  domain.HarnessClaudeCode,
		IdempotencyKey: "switch-once",
	})
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalUnsupported {
		t.Fatalf("repeated switch error = %v, want an unsupported refusal", err)
	}

	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("read switched session: found=%v err=%v", found, err)
	}
	if record.Harness != domain.HarnessClaudeCode || record.Mode != domain.SessionModeChat {
		t.Fatalf("durable session owner = %+v, want Claude Chat", record)
	}
	if record.Metadata.ProviderConversationID == "" {
		t.Fatalf("switched session has no target provider conversation: %+v", record.Metadata)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, before.ID)
	if err != nil {
		t.Fatalf("read switched conversation: %v", err)
	}
	if snapshot.ActiveBranch.ID == "" || snapshot.ActiveBranch.ID == before.ActiveBranchID {
		t.Fatalf("active branch = %q, want a new branch after %q", snapshot.ActiveBranch.ID, before.ActiveBranchID)
	}
	if snapshot.ActiveBranch.ProviderConversationID != record.Metadata.ProviderConversationID {
		t.Fatalf("active branch provider conversation = %q, want the session's %q",
			snapshot.ActiveBranch.ProviderConversationID, record.Metadata.ProviderConversationID)
	}
	if !strings.Contains(snapshot.ActiveBranch.HandoffNote, "keep this context") {
		t.Fatalf("the handoff note was not stored with the new branch: %q", snapshot.ActiveBranch.HandoffNote)
	}
	if snapshot.ActiveBranch.Strategy != domain.ConversationBranchStrategyApproximateContext {
		t.Fatalf("branch strategy = %q, want approximate_context", snapshot.ActiveBranch.Strategy)
	}
	foundMarker := false
	for _, activity := range snapshot.Activities {
		if activity.RequestID == "switch-once" && strings.Contains(activity.Summary, "Switched provider") {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatalf("the switch marker was not committed with the branch")
	}
}

func TestProviderSwitchFailureRestoresSourceController(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	driver.startErr = errProviderSwitchTarget

	err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode,
	})
	if err == nil || !strings.Contains(err.Error(), "start target provider") {
		t.Fatalf("SwitchProvider error = %v, want target-start failure", err)
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("source controller was not restored: %v", err)
	}
}

func TestProviderSwitchPublicationFailureRestoresSourceController(t *testing.T) {
	h, _, _ := newEditHarnessWithStore(t, false, func(st *store.Store) chatsvc.Store {
		return failingProviderSwitchStore{Store: st, err: errors.New("commit unavailable")}
	})

	err := h.svc.SwitchProvider(context.Background(), testSession, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode,
	})
	if err == nil || !strings.Contains(err.Error(), "start target provider") {
		t.Fatalf("SwitchProvider error = %v, want publication failure", err)
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("source controller was not restored after publication failure: %v", err)
	}
}

type failingProviderSwitchStore struct {
	*store.Store
	err error
}

func (s failingProviderSwitchStore) CommitProviderSwitch(context.Context, domain.SessionRecord, domain.ConversationBranch, string, domain.ConversationActivity) error {
	return s.err
}

var errProviderSwitchTarget = ports.ErrChatDriverUnavailable
