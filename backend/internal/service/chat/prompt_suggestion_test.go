package chat_test

import (
	"context"
	"errors"
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

func settledSuggestionTurn(t *testing.T, h *harness) string {
	t.Helper()
	turnID := completeTurn(t, h, "Fix the error", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted && h.ctrl.State() == ports.ChatControllerReady
	})
	return turnID
}

func TestPromptSuggestionUsesIsolatedBoundedContextWithoutChangingHistory(t *testing.T) {
	h := newHarness(t)
	turnID := settledSuggestionTurn(t, h)
	before, err := h.svc.Snapshot(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	h.svc.SetPromptSuggestionRunner(func(ctx context.Context, id domain.SessionID, sourceTurnID, systemPrompt, prompt string) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("suggestion runner has no 30-second deadline")
		}
		if id != testSession || sourceTurnID != turnID || !strings.Contains(systemPrompt, "Do not answer the prompt, use tools") ||
			prompt != systemPrompt+"\n\nRecent transcript:\nuser: Fix the error\n\nassistant: reply to Fix the error\n\n" {
			t.Fatalf("runner context: id=%s system=%q prompt=%q", id, systemPrompt, prompt)
		}
		return `{"prompt":"\nAdd a regression test.\n"}`, nil
	})
	prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID)
	if err != nil || prompt != "Add a regression test." {
		t.Fatalf("suggestion = %q, %v", prompt, err)
	}
	after, err := h.svc.Snapshot(context.Background(), testSession)
	if err != nil || len(after.Turns) != len(before.Turns) || len(after.Messages) != len(before.Messages) ||
		after.Conversation.LatestSequence != before.Conversation.LatestSequence || len(h.conv.sentMessages()) != 1 {
		t.Fatalf("suggestion changed the conversation: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestPromptSuggestionRejectsUnavailableOrStaleContext(t *testing.T) {
	for _, scenario := range []string{"missing", "terminal", "terminated", "old", "running", "queued", "rolled-back", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			h := newHarnessWithConversation(t, newHistoryRecorder())
			turnID := settledSuggestionTurn(t, h)
			h.svc.SetPromptSuggestionRunner(func(context.Context, domain.SessionID, string, string, string) (string, error) {
				t.Fatal("provider was called for an unavailable suggestion")
				return "", nil
			})
			id := testSession
			want := chatsvc.ErrPromptSuggestionStale
			switch scenario {
			case "missing":
				id, want = "missing-session", ports.ErrSessionNotFound
			case "terminal":
				changed, err := h.st.CommitSessionControllerEpoch(context.Background(), testSession, domain.SessionModeChat, domain.SessionModeTUI, "thread-1", h.now())
				if err != nil || !changed {
					t.Fatalf("commit TUI mode: changed=%v err=%v", changed, err)
				}
				want = chatsvc.ErrNotChatMode
			case "terminated":
				record, _, err := h.st.GetSession(context.Background(), testSession)
				if err != nil {
					t.Fatal(err)
				}
				record.IsTerminated = true
				if err := h.st.UpdateSession(context.Background(), record); err != nil {
					t.Fatal(err)
				}
			case "old":
				turnID = "older-turn"
			case "running", "queued":
				if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "new work", Origin: domain.MessageOriginHuman}); err != nil {
					t.Fatal(err)
				}
				if scenario == "queued" {
					if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "queued work", Origin: domain.MessageOriginHuman}); err != nil {
						t.Fatal(err)
					}
				}
			case "rolled-back":
				if _, err := h.svc.Rollback(context.Background(), testSession, turnID); err != nil {
					t.Fatal(err)
				}
			case "failed":
				if err := h.st.SettleTurnByID(context.Background(), turnID, domain.TurnStateFailed, "failed", h.now()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := h.svc.PromptSuggestion(context.Background(), id, turnID); !errors.Is(err, want) {
				t.Fatalf("suggestion error = %v, want %v", err, want)
			}
		})
	}
}

func TestPromptSuggestionDiscardsResultWhenConversationChanges(t *testing.T) {
	for _, scenario := range []string{"send", "rollback", "termination", "branch", "revised-answer"} {
		t.Run(scenario, func(t *testing.T) {
			h := newHarnessWithConversation(t, newHistoryRecorder())
			turnID := settledSuggestionTurn(t, h)
			h.svc.SetPromptSuggestionRunner(func(ctx context.Context, _ domain.SessionID, _, _, _ string) (string, error) {
				switch scenario {
				case "send":
					_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "I already chose", Origin: domain.MessageOriginHuman})
					if err != nil {
						t.Fatal(err)
					}
				case "rollback":
					if _, err := h.svc.Rollback(ctx, testSession, turnID); err != nil {
						t.Fatal(err)
					}
				case "termination":
					record, _, err := h.st.GetSession(ctx, testSession)
					if err != nil {
						t.Fatal(err)
					}
					record.IsTerminated = true
					if err := h.st.UpdateSession(ctx, record); err != nil {
						t.Fatal(err)
					}
				case "branch":
					snapshot, err := h.svc.Snapshot(ctx, testSession)
					if err != nil {
						t.Fatal(err)
					}
					branch := domain.ConversationBranch{
						ID: "suggestion-sibling", ConversationID: snapshot.Conversation.ID, SessionID: testSession,
						ProviderConversationID: "thread-sibling", ParentBranchID: snapshot.ActiveBranch.ID,
						ForkAfterTurnID: turnID, ForkAfterSequence: snapshot.Conversation.LatestSequence,
					}
					if err := h.st.CreateConversationBranch(ctx, branch, h.now()); err != nil {
						t.Fatal(err)
					}
					if err := h.st.ActivateConversationBranch(ctx, testSession, branch.ConversationID, branch.ID, branch.ProviderConversationID, h.ctrl.Generation(), h.now()); err != nil {
						t.Fatal(err)
					}
				case "revised-answer":
					if err := h.st.SettleAssistantMessage(ctx, h.ctrl.ConversationID(), "msg-provider-turn-1", "provider-turn-1", "A different answer", "replacement-message", h.now()); err != nil {
						t.Fatal(err)
					}
				}
				return `{"prompt":"Run the tests."}`, nil
			})
			if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); prompt != "" || !errors.Is(err, chatsvc.ErrPromptSuggestionStale) {
				t.Fatalf("stale suggestion = %q, %v", prompt, err)
			}
		})
	}
}

func TestPromptSuggestionSurvivesActivityOnlyUpdates(t *testing.T) {
	h := newHarness(t)
	turnID := settledSuggestionTurn(t, h)
	h.svc.SetPromptSuggestionRunner(func(ctx context.Context, _ domain.SessionID, _, _, _ string) (string, error) {
		if err := h.st.UpsertActivity(ctx, h.ctrl.ConversationID(), "provider-turn-1", domain.ConversationActivity{
			ID: "late-activity", ProviderItemID: "late-item", Kind: domain.ActivityKindUsage,
			Status: domain.ActivityStatusCompleted, Summary: "Updated usage",
		}, h.now()); err != nil {
			t.Fatal(err)
		}
		return `{"prompt":"Add a regression test."}`, nil
	})
	if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); err != nil || prompt != "Add a regression test." {
		t.Fatalf("activity update discarded suggestion: %q, %v", prompt, err)
	}
}

func TestPromptSuggestionReturnsEmptyForNoUsefulMalformedOrOversizedAnswer(t *testing.T) {
	h := newHarness(t)
	turnID := settledSuggestionTurn(t, h)
	for _, raw := range []string{
		`{"prompt":""}`, "",
		`{"prompt":"` + strings.Repeat("界", 241) + `"}`,
		"getaddrinfo ENOTFOUND llm.substrate.dev",
		"{invalid json}", `{"prompt":42}`, `{"error":"provider unavailable"}`,
		"Here is a suggestion: {\"prompt\":\"Run the tests.\"}",
		"```json\n{\"prompt\":\"Run the tests.\"}\n```\nProvider failed.",
		`{"prompt":"Run the tests."} trailing prose`,
	} {
		h.svc.SetPromptSuggestionRunner(func(context.Context, domain.SessionID, string, string, string) (string, error) { return raw, nil })
		if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); err != nil || prompt != "" {
			t.Fatalf("answer %q: prompt=%q err=%v", raw, prompt, err)
		}
	}
}

func TestPromptSuggestionAcceptsStructuredJSONAndSingleFences(t *testing.T) {
	h := newHarness(t)
	turnID := settledSuggestionTurn(t, h)
	for _, raw := range []string{
		`{"prompt":"Run the tests."}`,
		"```json\n{\"prompt\":\"Run the tests.\"}\n```",
		"```json\r\n{\"prompt\":\"Run the tests.\"}\r\n```",
		"```JSON\n{\"prompt\":\"Run the tests.\"}\n```",
		"```\n{\"prompt\":\"Run the tests.\"}\n```",
	} {
		h.svc.SetPromptSuggestionRunner(func(context.Context, domain.SessionID, string, string, string) (string, error) { return raw, nil })
		if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); err != nil || prompt != "Run the tests." {
			t.Fatalf("answer %q: prompt=%q err=%v", raw, prompt, err)
		}
	}
	h.svc.SetPromptSuggestionRunner(func(context.Context, domain.SessionID, string, string, string) (string, error) {
		return `{"prompt":"NONE"}`, nil
	})
	if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); err != nil || prompt != "NONE" {
		t.Fatalf("plain-text protocol sentinel still intercepted JSON: prompt=%q err=%v", prompt, err)
	}
}

func TestPromptSuggestionOmitsExpectedOptionalRunnerFailures(t *testing.T) {
	h := newHarness(t)
	turnID := settledSuggestionTurn(t, h)
	for _, runErr := range []error{
		errors.New("provider unavailable"), errors.New("background task requested user interaction"),
		errors.New("background task returned no text"), errors.New("session: harness install active"), context.DeadlineExceeded,
	} {
		h.svc.SetPromptSuggestionRunner(func(context.Context, domain.SessionID, string, string, string) (string, error) { return "", runErr })
		if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); prompt != "" || err != nil {
			t.Fatalf("optional error %v: prompt=%q err=%v", runErr, prompt, err)
		}
	}
}

func TestPromptSuggestionLimitsConcurrentProviderCallsAndHonorsCancellation(t *testing.T) {
	h := newHarness(t)
	turnID := settledSuggestionTurn(t, h)
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	h.svc.SetPromptSuggestionRunner(func(ctx context.Context, _ domain.SessionID, _, _, _ string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := h.svc.PromptSuggestion(ctx, testSession, turnID)
			done <- err
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("suggestion runner did not start")
		}
	}
	if prompt, err := h.svc.PromptSuggestion(ctx, testSession, turnID); !errors.Is(err, chatsvc.ErrPromptSuggestionBusy) || prompt != "" || calls.Load() != 2 {
		t.Fatalf("capacity result = %q, %v; calls=%d", prompt, err, calls.Load())
	}
	cancel()
	for range 2 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	}
	h.svc.SetPromptSuggestionRunner(func(context.Context, domain.SessionID, string, string, string) (string, error) {
		return `{"prompt":"Run the tests."}`, nil
	})
	if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID); err != nil || prompt != "Run the tests." {
		t.Fatalf("released capacity did not recover: prompt=%q err=%v", prompt, err)
	}
}

func newPagedSuggestionHarness(t *testing.T, read chatsvc.SnapshotPageReaderFunc) *harness {
	t.Helper()
	st := openStore(t)
	conv := newFakeConversation()
	h := &harness{st: st, conv: conv, clock: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)}
	var counter atomic.Int32
	h.svc = chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Reader: fullSnapshotReader(st), PageReader: read,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}}, Now: h.now,
		NewID: func() string { return fmt.Sprintf("suggestion-%d", counter.Add(1)) },
	})
	var err error
	h.ctrl, err = h.svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.svc.Stop(context.Background(), testSession) })
	return h
}

func suggestionPage(rows store.ConversationSnapshot) chatsvc.ConversationRows {
	return chatsvc.ConversationRows{
		Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
		Messages: rows.Messages, Activities: rows.Activities, OldestSequence: rows.OldestSequence, HasMoreBefore: rows.HasMoreBefore,
	}
}

func TestPromptSuggestionReadsBoundedPagesAcrossActivityHistory(t *testing.T) {
	var h *harness
	var reads int
	h = newPagedSuggestionHarness(t, func(ctx context.Context, conversationID string, before, limit int64) (chatsvc.ConversationRows, error) {
		if limit != 200 {
			t.Fatalf("unbounded page limit: %d", limit)
		}
		reads++
		rows, err := h.st.LoadConversationSnapshotPage(ctx, conversationID, before, limit)
		return suggestionPage(rows), err
	})
	for i := range 4 {
		turnID := completeTurn(t, h, fmt.Sprintf("Message %d", i), fmt.Sprintf("provider-turn-%d", i+1))
		h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
			return len(s.Turns) == i+1 && s.Turns[i].ID == turnID && s.Turns[i].State == domain.TurnStateCompleted && h.ctrl.State() == ports.ChatControllerReady
		})
	}
	rows, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 205 {
		// Late activity belongs to an older turn. The latest turn must retain
		// durable row ordering even when the first page represents only that turn.
		if err := h.st.UpsertActivity(context.Background(), h.ctrl.ConversationID(), "provider-turn-1", domain.ConversationActivity{
			ID: fmt.Sprintf("suggestion-activity-%d", i), ProviderItemID: fmt.Sprintf("suggestion-item-%d", i),
			Kind: domain.ActivityKindUsage, Status: domain.ActivityStatusCompleted,
		}, h.now()); err != nil {
			t.Fatal(err)
		}
	}
	h.svc.SetPromptSuggestionRunner(func(_ context.Context, _ domain.SessionID, _, _, prompt string) (string, error) {
		if strings.Count(prompt, "\nuser: ") != 4 || strings.Count(prompt, "\nassistant: ") != 4 || !strings.Contains(prompt, "user: Message 0") {
			t.Fatalf("activity pages lost the last eight text messages: %q", prompt)
		}
		return `{"prompt":"Run the tests."}`, nil
	})
	// Activity does not count toward a page, so the read and its revalidation
	// each take one bounded page despite the 205 late activities.
	if prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, rows.Turns[len(rows.Turns)-1].ID); err != nil || prompt != "Run the tests." || reads != 2 {
		t.Fatalf("paged suggestion: prompt=%q err=%v reads=%d", prompt, err, reads)
	}
}

func TestPromptSuggestionRevalidatesWithCallerContextAndPreservesReadErrors(t *testing.T) {
	for _, failure := range []string{"none", "initial", "revalidation"} {
		t.Run(failure, func(t *testing.T) {
			var h *harness
			var runCalls, reads int
			readErr := errors.New("snapshot database unavailable")
			h = newPagedSuggestionHarness(t, func(ctx context.Context, conversationID string, before, limit int64) (chatsvc.ConversationRows, error) {
				reads++
				if _, hasDeadline := ctx.Deadline(); hasDeadline {
					t.Fatal("snapshot read inherited the provider deadline")
				}
				if failure == "initial" || (failure == "revalidation" && runCalls > 0) {
					return chatsvc.ConversationRows{}, readErr
				}
				rows, err := h.st.LoadConversationSnapshotPage(ctx, conversationID, before, limit)
				return suggestionPage(rows), err
			})
			turnID := settledSuggestionTurn(t, h)
			h.svc.SetPromptSuggestionRunner(func(ctx context.Context, _ domain.SessionID, _, _, _ string) (string, error) {
				runCalls++
				if _, hasDeadline := ctx.Deadline(); !hasDeadline {
					t.Fatal("provider has no deadline")
				}
				return `{"prompt":"Run the tests."}`, nil
			})
			prompt, err := h.svc.PromptSuggestion(context.Background(), testSession, turnID)
			if failure == "none" {
				if prompt != "Run the tests." || err != nil || reads != 2 {
					t.Fatalf("successful revalidation: prompt=%q err=%v reads=%d", prompt, err, reads)
				}
			} else if prompt != "" || !errors.Is(err, readErr) || (failure == "initial" && runCalls != 0) {
				t.Fatalf("snapshot failure suppressed: prompt=%q err=%v runner calls=%d", prompt, err, runCalls)
			}
		})
	}
}
