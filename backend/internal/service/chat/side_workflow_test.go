package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type sideWorkflow struct {
	*harness
	driver  *connectedSideForkDriver
	changed *changedExcerptStore
}

func newSideWorkflow(t *testing.T, mode string, harnesses ...domain.AgentHarness) *sideWorkflow {
	t.Helper()
	ctx := context.Background()
	st := openStore(t)
	main := newFakeConversation()
	driver := &connectedSideForkDriver{}
	driver.start = func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		if cfg.SessionID == testSession {
			return main, nil
		}
		conv := newFakeConversation()
		conv.providerConversationID = "fresh-" + string(cfg.SessionID)
		driver.mu.Lock()
		driver.forks = append(driver.forks, conv)
		driver.configs = append(driver.configs, cfg)
		driver.mu.Unlock()
		return conv, nil
	}
	harnessType := domain.HarnessCodex
	if len(harnesses) > 0 {
		harnessType = harnesses[0]
	}
	changed := &changedExcerptStore{Store: st, mode: mode}
	var ids atomic.Int64
	svc := chatsvc.New(chatsvc.Options{Store: changed, Reader: fullSnapshotReader(st), Sessions: st, Drivers: fakeRegistry{driver: driver}, AppRunID: "workflow", NewID: func() string { return fmt.Sprintf("workflow-%d", ids.Add(1)) }})
	ctrl, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: harnessType, WorkspacePath: t.TempDir(), Model: "initial", Effort: "low", Permissions: "default", SystemPrompt: "Frozen instructions"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.InitializeSideChats(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.StopAll(ctx) })
	return &sideWorkflow{harness: &harness{st: st, svc: svc, ctrl: ctrl, conv: main}, driver: driver, changed: changed}
}
func (h *sideWorkflow) source(t *testing.T) ports.ChatExcerptReference {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "seed", ClientMessageID: "seed", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "source", Text: "Selected source text."}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	snap := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 2 && s.Turns[0].State == domain.TurnStateCompleted
	})
	source := snap.Messages[1]
	return ports.ChatExcerptReference{ConversationID: h.ctrl.ConversationID(), MessageID: source.ID, Revision: source.Revision, Text: "Selected source"}
}
func (h *sideWorkflow) side(t *testing.T, key string, force bool) domain.SideConversation {
	t.Helper()
	side, err := h.svc.CreateIndependentSideChat(context.Background(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: key, ForceNew: force})
	if err != nil {
		t.Fatal(err)
	}
	return h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "ready" }).Side
}
func (h *sideWorkflow) awaitSide(t *testing.T, id string, pred func(domain.SideSnapshot) bool) domain.SideSnapshot {
	t.Helper()
	var snap domain.SideSnapshot
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		var err error
		snap, err = h.svc.SideSnapshot(context.Background(), testSession, id, time.Time{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if pred(snap) {
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("side did not settle: %+v", snap)
	return snap
}
func (h *sideWorkflow) provider(index int) *fakeConversation {
	h.driver.mu.Lock()
	defer h.driver.mu.Unlock()
	return h.driver.forks[index]
}

func TestSideExplicitNewSharesAnchorAndKeepsMainIndependent(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	one := h.side(t, "one", true)
	two := h.side(t, "two", true)
	if one.ID == two.ID || one.AnchorTurnID != two.AnchorTurnID {
		t.Fatalf("explicit sides: %+v %+v", one, two)
	}
	reused := h.side(t, "reuse", false)
	if reused.ID != one.ID && reused.ID != two.ID {
		t.Fatal("btw did not reuse anchor")
	}
	for i, side := range []domain.SideConversation{one, two} {
		if _, err := h.svc.SendSideQuestion(context.Background(), testSession, side.ID, ports.ChatUserMessage{Text: "parallel", ClientMessageID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
		h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return h.provider(i).sendCallCount() == 1 })
	}
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "main still works", ClientMessageID: "main-active"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.Controller(testSession); got != h.ctrl {
		t.Fatal("main ownership replaced")
	}
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatal(err)
	}
	if sides, err := h.svc.ListIndependentSideChats(context.Background(), testSession); err != nil || len(sides) != 0 {
		t.Fatalf("parent stop retained sides: %v %v", sides, err)
	}
}

func TestSideExcerptOnlyFallbackAndFrozenReceipt(t *testing.T) {
	for _, n := range []int{1, 2} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			h := newSideWorkflow(t, "")
			ref := h.source(t)
			side := h.side(t, "side", true)
			refs := []ports.ChatExcerptReference{ref}
			if n == 2 {
				second := ref
				second.Text = "source text"
				refs = append(refs, second)
			}
			msg := ports.ChatUserMessage{ClientMessageID: "excerpt-only", Excerpts: refs}
			turn, err := h.svc.SendSideQuestion(context.Background(), testSession, side.ID, msg)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("Use the attached %d chat excerpt(s) as context", n)
			if turn.Text != want {
				t.Fatalf("fallback %q", turn.Text)
			}
			h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
			delivered := h.provider(0).sentTexts()[0]
			for _, part := range []string{want, "Selected source text.", "seed", "this", "it"} {
				if !strings.Contains(delivered, part) {
					t.Fatalf("missing %q: %s", part, delivered)
				}
			}
			h.changed.mode = "revision"
			h.changed.changed.Store(true)
			duplicate, err := h.svc.SendSideQuestion(context.Background(), testSession, side.ID, msg)
			if err != nil || duplicate.ID != turn.ID || h.provider(0).sendCallCount() != 1 {
				t.Fatalf("frozen retry %+v %v", duplicate, err)
			}
			encoded, _ := json.Marshal(turn)
			if strings.Contains(string(encoded), "Context") || strings.Contains(string(encoded), "user:\\n") {
				t.Fatalf("internal context leaked: %s", encoded)
			}
		})
	}
}

func TestSideStaleQueuedExcerptsAllowLaterQuestions(t *testing.T) {
	for _, mode := range []string{"revision", "rollback", "missing pair"} {
		t.Run(mode, func(t *testing.T) {
			h := newSideWorkflow(t, mode)
			ref := h.source(t)
			side := h.side(t, "side", true)
			ctx := context.Background()
			active, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, ports.ChatUserMessage{Text: "active", ClientMessageID: "active"})
			if err != nil {
				t.Fatal(err)
			}
			h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
			stale, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, ports.ChatUserMessage{Text: "stale", ClientMessageID: "stale", Excerpts: []ports.ChatExcerptReference{ref}})
			if err != nil {
				t.Fatal(err)
			}
			valid, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, ports.ChatUserMessage{Text: "next", ClientMessageID: "next"})
			if err != nil {
				t.Fatal(err)
			}
			h.changed.changed.Store(true)
			h.provider(0).emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
			snap := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool {
				states := map[string]string{}
				for _, turn := range s.Turns {
					states[turn.ID] = turn.State
				}
				return states[active.ID] == "completed" && states[stale.ID] == "failed" && states[valid.ID] == "running" && h.provider(0).sendCallCount() == 2
			})
			for _, turn := range snap.Turns {
				if turn.ID == stale.ID && turn.ErrorMessage != "chat excerpt is stale" {
					t.Fatalf("stale failure: %q", turn.ErrorMessage)
				}
			}
			if got := h.provider(0).sentTexts(); len(got) != 2 || !strings.HasSuffix(got[1], "Current side-chat request:\nnext") {
				t.Fatalf("dispatches: %v", got)
			}
		})
	}
}

func TestSideFreshAndReconstructedContext(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			h := newSideWorkflow(t, "", domain.HarnessClaudeCode)
			if completed {
				h.source(t)
			}
			_, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "running must be excluded", ClientMessageID: "running"})
			if err != nil {
				t.Fatal(err)
			}
			side := h.side(t, "context", true)
			want := "fresh"
			if completed {
				want = "reconstructed"
			}
			if side.ContextMode != want {
				t.Fatalf("mode=%s want=%s", side.ContextMode, want)
			}
			h.driver.mu.Lock()
			cfg := h.driver.configs[0]
			h.driver.mu.Unlock()
			if cfg.SessionID == testSession || !strings.HasPrefix(cfg.SystemPrompt, "Frozen instructions") || !strings.Contains(cfg.SystemPrompt, "[AO independent side chat]") || cfg.Model != "initial" || cfg.Effort != "low" || cfg.Permissions != "default" {
				t.Fatalf("config not inherited: %+v", cfg)
			}
			msg := ports.ChatUserMessage{Text: "  Explain it.  ", ClientMessageID: "question", Content: []ports.ChatContent{{Type: "resource_link", URI: "/workspace/note.txt", Name: "note.txt"}}}
			turn, err := h.svc.SendSideQuestion(context.Background(), testSession, side.ID, msg)
			if err != nil {
				t.Fatal(err)
			}
			if turn.Text != msg.Text {
				t.Fatalf("wording changed: %q", turn.Text)
			}
			h.awaitSide(t, side.ID, func(domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
			delivered := h.provider(0).sentTexts()[0]
			if strings.Contains(delivered, "running must be excluded") {
				t.Fatal("running context leaked")
			}
			if completed && (!strings.Contains(delivered, "Recorded visible main-chat history") || !strings.Contains(delivered, "Selected source text.")) {
				t.Fatalf("reconstructed context missing: %s", delivered)
			}
			if !strings.Contains(delivered, "/workspace/note.txt") {
				t.Fatal("attachment missing from provider context")
			}
		})
	}
}

func TestSideProviderFailurePausesQueueUntilExplicitRetry(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	side := h.side(t, "side", true)
	ctx := context.Background()
	active, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, ports.ChatUserMessage{Text: "active", ClientMessageID: "active"})
	if err != nil {
		t.Fatal(err)
	}
	h.awaitSide(t, side.ID, func(domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
	next, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, ports.ChatUserMessage{Text: "next", ClientMessageID: "next"})
	if err != nil {
		t.Fatal(err)
	}
	h.provider(0).emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateFailed})
	h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ID == active.ID {
				return turn.State == "failed"
			}
		}
		return false
	})
	snap, err := h.svc.SideSnapshot(ctx, testSession, side.ID, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range snap.Turns {
		if turn.ID == next.ID && turn.State != "queued" {
			t.Fatalf("unrelated queued turn became %s", turn.State)
		}
	}
	if h.provider(0).sendCallCount() != 1 {
		t.Fatal("queue drained after provider failure")
	}
	if err := h.svc.RetrySideQuestion(ctx, testSession, side.ID, active.ID); err != nil {
		t.Fatal(err)
	}
	h.awaitSide(t, side.ID, func(domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 2 })
	h.provider(0).mu.Lock()
	sent := append([]ports.ChatUserMessage(nil), h.provider(0).sent...)
	h.provider(0).mu.Unlock()
	if sent[1].ClientMessageID == sent[0].ClientMessageID {
		t.Fatal("explicit retry reused the failed provider request")
	}
	h.provider(0).emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2", TurnState: domain.TurnStateCompleted})
	h.awaitSide(t, side.ID, func(domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 3 })
}

func TestSideLaterMainExcerptDoesNotMoveFrozenFork(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessClaudeCode)
	h.source(t)
	side := h.side(t, "frozen", true)
	ctx := context.Background()
	completeMain := func(key string) ports.ChatExcerptReference {
		t.Helper()
		turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "question-" + key, ClientMessageID: key})
		if err != nil {
			t.Fatal(err)
		}
		h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: key, Text: "answer-" + key}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted})
		snap := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
			for _, v := range s.Turns {
				if v.ID == turn.ID {
					return v.State == domain.TurnStateCompleted
				}
			}
			return false
		})
		for _, msg := range snap.Messages {
			if msg.TurnID == turn.ID && msg.Role == domain.MessageRoleAssistant {
				return ports.ChatExcerptReference{ConversationID: h.ctrl.ConversationID(), MessageID: msg.ID, Revision: msg.Revision, Text: "answer-" + key}
			}
		}
		t.Fatal("missing source")
		return ports.ChatExcerptReference{}
	}
	ref := completeMain("B")
	completeMain("C")
	_, err := h.svc.SendSideQuestion(ctx, testSession, side.ID, ports.ChatUserMessage{Text: "Explain this", ClientMessageID: "with-B", Excerpts: []ports.ChatExcerptReference{ref}})
	if err != nil {
		t.Fatal(err)
	}
	snap := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
	text := h.provider(0).sentTexts()[0]
	if snap.Side.AnchorTurnID != side.AnchorTurnID || !strings.Contains(text, "Selected source text") || !strings.Contains(text, "answer-B") || strings.Contains(text, "answer-C") || strings.Contains(text, "question-C") {
		t.Fatalf("fork moved or context leaked: anchor=%s text=%s", snap.Side.AnchorTurnID, text)
	}
}

func TestSideSelfExcerptDeliveryAndCrossConversationRejection(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	ctx := context.Background()
	one := h.side(t, "self", true)
	two := h.side(t, "isolated", true)
	turn, err := h.svc.SendSideQuestion(ctx, testSession, one.ID, ports.ChatUserMessage{Text: "Explain side-only topic", ClientMessageID: "self-seed"})
	if err != nil {
		t.Fatal(err)
	}
	h.awaitSide(t, one.ID, func(s domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
	h.provider(0).emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderItemID: "self-source", Text: "Side-only answer."}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	snap := h.awaitSide(t, one.ID, func(s domain.SideSnapshot) bool {
		for _, v := range s.Turns {
			if v.ID == turn.ID {
				return v.State == "completed" && len(s.Messages) == 2
			}
		}
		return false
	})
	var source domain.SideMessage
	for _, msg := range snap.Messages {
		if msg.Role == "assistant" {
			source = msg
		}
	}
	ref := ports.ChatExcerptReference{ConversationID: one.ID, MessageID: source.ID, Revision: source.Revision, Text: "Side-only answer"}
	if _, err = h.svc.SendSideQuestion(ctx, testSession, two.ID, ports.ChatUserMessage{ClientMessageID: "cross", Excerpts: []ports.ChatExcerptReference{ref}}); err == nil {
		t.Fatal("another side accepted source")
	}
	if _, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{ClientMessageID: "side-to-main", Excerpts: []ports.ChatExcerptReference{ref}}); err == nil {
		t.Fatal("main accepted side source")
	}
	accepted, err := h.svc.SendSideQuestion(ctx, testSession, one.ID, ports.ChatUserMessage{ClientMessageID: "self-ref", Excerpts: []ports.ChatExcerptReference{ref}})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Text != "Use the attached 1 chat excerpt(s) as context" {
		t.Fatal(accepted.Text)
	}
	h.awaitSide(t, one.ID, func(s domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 2 })
	text := h.provider(0).sentTexts()[1]
	if !strings.Contains(text, "Explain side-only topic") || !strings.Contains(text, "Side-only answer.") {
		t.Fatalf("missing self paired turn: %s", text)
	}
	duplicate, err := h.svc.SendSideQuestion(ctx, testSession, one.ID, ports.ChatUserMessage{ClientMessageID: "self-ref", Excerpts: []ports.ChatExcerptReference{ref}})
	if err != nil || duplicate.ID != accepted.ID {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
	if h.provider(1).sendCallCount() != 0 {
		t.Fatal("other side was dispatched")
	}
}
