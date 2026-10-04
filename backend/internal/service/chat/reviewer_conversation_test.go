package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

const testReview = "review-1"

// modelListingConversation adds a model catalog to the history fake so the
// reviewer's model picker has something to list.
type modelListingConversation struct {
	*historyRecorder
}

func (m modelListingConversation) ListModels(context.Context) ([]ports.ChatModel, error) {
	return []ports.ChatModel{{ID: "gpt-reviewer", DisplayName: "Reviewer model"}}, nil
}

type reviewerHarness struct {
	svc         *chatsvc.Service
	st          *store.Store
	source      *historyRecorder
	ctrl        *chatsvc.Controller
	resumes     func() []ports.ChatResumeConfig
	modelPicked func() []string
}

// newReviewerHarness starts a Chat reviewer the way the review engine does: a
// review-owned controller on the worker's session, launched read-only.
func newReviewerHarness(t *testing.T) *reviewerHarness {
	t.Helper()
	st := openStore(t)
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	if err := st.UpsertReview(context.Background(), domain.Review{
		ID: testReview, SessionID: testSession, ProjectID: testProject,
		Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertReview: %v", err)
	}
	source := newHistoryRecorder()
	forked := newFakeConversation()
	forked.providerConversationID = "thread-forked"
	forked.turnSeq = 200

	var mu sync.Mutex
	var resumes []ports.ChatResumeConfig
	var picked []string
	driver := fakeDriver{conv: modelListingConversation{source}}
	driver.resume = func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
		mu.Lock()
		defer mu.Unlock()
		resumes = append(resumes, cfg)
		if cfg.ProviderConversationID != "thread-forked" {
			return nil, errors.New("unexpected provider conversation: " + cfg.ProviderConversationID)
		}
		return forked, nil
	}
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader:  storeReader(st),
		Drivers: fakeRegistry{driver: driver},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			mu.Lock()
			defer mu.Unlock()
			nextID++
			return fmt.Sprintf("reviewer-id-%d", nextID)
		},
		Now: func() time.Time { return now },
		OnModelChanged: func(_ domain.SessionID, model string) {
			mu.Lock()
			defer mu.Unlock()
			picked = append(picked, model)
		},
	})
	owner := domain.ReviewConversationOwner(testReview)
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		Owner: owner, SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto,
	})
	if err != nil {
		t.Fatalf("Start reviewer: %v", err)
	}
	t.Cleanup(func() { _ = svc.StopForOwner(context.Background(), owner) })
	return &reviewerHarness{
		svc: svc, st: st, source: source, ctrl: ctrl,
		resumes: func() []ports.ChatResumeConfig {
			mu.Lock()
			defer mu.Unlock()
			return append([]ports.ChatResumeConfig(nil), resumes...)
		},
		modelPicked: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), picked...)
		},
	}
}

func storeReader(st *store.Store) chatsvc.SnapshotReader {
	return chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
		snapshot, err := st.LoadConversationSnapshot(ctx, conversationID)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return chatsvc.ConversationRows{
			Conversation: snapshot.Conversation, ActiveBranch: snapshot.ActiveBranch,
			Turns: snapshot.Turns, Messages: snapshot.Messages,
			Activities: snapshot.Activities, BranchPoints: snapshot.BranchPoints,
			BranchedFromEarlierMessage: snapshot.BranchedFromEarlierMessage,
		}, nil
	})
}

func (h *reviewerHarness) completeTurn(t *testing.T, text, providerTurn string) string {
	t.Helper()
	turn, err := h.svc.SendForOwner(context.Background(), domain.ReviewConversationOwner(testReview),
		ports.ChatUserMessage{Text: text, Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatalf("SendForOwner %q: %v", text, err)
	}
	h.source.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurn},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: providerTurn,
			ProviderItemID: "msg-" + providerTurn, Text: "reply to " + text},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: providerTurn,
			TurnState: domain.TurnStateCompleted},
	)
	return turn.ID
}

func (h *reviewerHarness) awaitSnapshot(t *testing.T, pred func(store.ConversationSnapshot) bool) store.ConversationSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
		if err == nil && pred(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("reviewer conversation never reached the expected state: %+v (err=%v)", snapshot, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReviewerChatTurnsCompleteThroughTheSharedControllerPath(t *testing.T) {
	h := newReviewerHarness(t)
	h.completeTurn(t, "review the PR", "provider-turn-1")
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted && len(s.Messages) == 2
	})
	if snapshot.Messages[1].Text != "reply to review the PR" {
		t.Fatalf("reviewer reply = %q", snapshot.Messages[1].Text)
	}
}

func TestReviewerModelPickerUsesTheReviewerAndNeverRewritesTheWorkerModel(t *testing.T) {
	h := newReviewerHarness(t)
	ctx := context.Background()
	owner := domain.ReviewConversationOwner(testReview)

	models, _, err := h.svc.ModelsForOwner(ctx, owner)
	if err != nil || len(models) != 1 || models[0].ID != "gpt-reviewer" {
		t.Fatalf("reviewer models = %+v, err=%v", models, err)
	}
	if _, _, err := h.svc.Models(ctx, testSession); !errors.Is(err, chatsvc.ErrNoController) {
		t.Fatalf("worker models err = %v; the reviewer controller must not answer for the worker", err)
	}

	current := h.ctrl.Settings()
	settings, err := h.svc.SetTurnSettingsForOwner(ctx, owner, domain.ConversationSettings{
		Model: "gpt-reviewer", ReasoningEffort: "high", ApprovalMode: current.ApprovalMode,
	})
	if err != nil || settings.Model != "gpt-reviewer" || settings.ReasoningEffort != "high" {
		t.Fatalf("SetTurnSettingsForOwner = %+v, err=%v", settings, err)
	}
	if picked := h.modelPicked(); len(picked) != 0 {
		t.Fatalf("reviewer model pick rewrote the worker session model: %v", picked)
	}
}

func TestReviewerApprovalModeStaysReadOnly(t *testing.T) {
	h := newReviewerHarness(t)
	owner := domain.ReviewConversationOwner(testReview)
	_, err := h.svc.SetTurnSettingsForOwner(context.Background(), owner, domain.ConversationSettings{
		ApprovalMode: ports.PermissionModeBypassPermissions,
	})
	if !errors.Is(err, chatsvc.ErrReviewerReadOnly) {
		t.Fatalf("approval change err = %v, want ErrReviewerReadOnly", err)
	}
	if _, err := h.svc.SetConfigOptionForOwner(context.Background(), owner, "mode",
		ports.ChatConfigOptionValue{Select: "bypassPermissions"}); !errors.Is(err, chatsvc.ErrReviewerReadOnly) {
		t.Fatalf("permission config option err = %v, want ErrReviewerReadOnly", err)
	}
}

func TestReviewerEditRelaunchesReadOnlyOnTheReviewerHostUnderTheReviewFence(t *testing.T) {
	h := newReviewerHarness(t)
	ctx := context.Background()
	owner := domain.ReviewConversationOwner(testReview)
	workerBefore, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	h.completeTurn(t, "A", "provider-turn-1")
	second := h.completeTurn(t, "B", "provider-turn-2")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 4 })

	result, err := h.svc.EditMessageForOwner(ctx, owner, second, ports.ChatUserMessage{
		Text: "B edited", ClientMessageID: "edit-b", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("EditMessageForOwner: %v", err)
	}
	if result.ActiveBranchID == "" || result.ActiveBranchID == result.SourceBranchID {
		t.Fatalf("edit result = %+v", result)
	}
	resumes := h.resumes()
	if len(resumes) != 1 {
		t.Fatalf("resumes = %+v", resumes)
	}
	if !resumes[0].ReadOnly {
		t.Fatal("edited reviewer relaunched without its read-only sandbox")
	}
	if resumes[0].SessionID != domain.SessionID("review-"+testReview) {
		t.Fatalf("edited reviewer relaunched on host %q, want the reviewer host", resumes[0].SessionID)
	}

	review, found, err := h.st.GetReviewByID(ctx, testReview)
	if err != nil || !found {
		t.Fatalf("GetReviewByID: found=%v err=%v", found, err)
	}
	if review.ProviderConversationID != "thread-forked" || review.ControllerGeneration == "" ||
		review.ControllerGeneration == h.ctrl.Generation() {
		t.Fatalf("review fence after edit = %+v", review)
	}
	workerAfter, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if workerAfter.Metadata.ControllerGeneration != workerBefore.Metadata.ControllerGeneration ||
		workerAfter.Metadata.ProviderConversationID != workerBefore.Metadata.ProviderConversationID {
		t.Fatalf("reviewer edit moved the worker controller: before=%+v after=%+v",
			workerBefore.Metadata, workerAfter.Metadata)
	}
}
