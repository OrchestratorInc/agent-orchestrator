package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// failureStorePragmas opens a second connection the way the store does.
const failureStorePragmas = "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"

func TestBindSessionAgentAccountCancelsOnlyChangedProviderAutoResume(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, _ := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			other := domain.AgentAccountProviderClaude
			if provider == other {
				other = domain.AgentAccountProviderCodex
			}
			// Initial, repeated and backdated bindings do not change the active login.
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
			assertAccountAutoResume(t, s, session.ID, true, "")
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(10*time.Minute)))
			assertAccountAutoResume(t, s, session.ID, true, "")
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "historical", testNow.Add(-2*time.Minute)))
			assertAccountAutoResume(t, s, session.ID, true, "")
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, other, "other-a", testNow.Add(-time.Minute)))
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, other, "other-b", testNow.Add(20*time.Minute)))
			assertAccountAutoResume(t, s, session.ID, true, "")

			second, _ := seedAccountAutoResume(t, s, harness)
			mustNoError(t, s.BindSessionAgentAccount(ctx, second.ID, provider, "account-a", testNow.Add(-time.Minute)))
			mustNoError(t, s.BindSessionAgentAccount(ctx, second.ID, provider, "account-b", testNow.Add(20*time.Minute)))
			assertAccountAutoResume(t, s, second.ID, false, "")
			assertAccountAutoResume(t, s, session.ID, true, "")

			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(30*time.Minute)))
			assertAccountAutoResume(t, s, session.ID, false, "")

			consumed, conversation := seedAccountAutoResume(t, s, harness)
			mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, "turn-"+string(consumed.ID), nil, false, "original refusal"))
			mustNoError(t, s.BindSessionAgentAccount(ctx, consumed.ID, provider, "account-a", testNow.Add(-time.Minute)))
			mustNoError(t, s.BindSessionAgentAccount(ctx, consumed.ID, provider, "account-b", testNow.Add(time.Minute)))
			assertAccountAutoResume(t, s, consumed.ID, false, "original refusal")
		})
	}
}

func TestSetTurnAutoResumeRejectsChangedSourceAccount(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			turnID := "turn-" + string(session.ID)
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
			// The switch precedes completion; the turn belongs to its requested account.
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(30*time.Second)))
			reset := testNow.Add(2 * time.Hour)
			if err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""); !errors.Is(err, ports.ErrAgentAccountChanged) {
				t.Fatalf("re-arm changed account = %v", err)
			}
			assertAccountAutoResume(t, s, session.ID, false, "")
			if err := s.SetTurnAutoResume(ctx, "other-conversation", turnID, &reset, true, ""); !errors.Is(err, domain.ErrNoConversationTurn) {
				t.Fatalf("re-arm wrong conversation = %v", err)
			}
			// Returning to the source account makes its known deadline usable again.
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(2*time.Minute)))
			reset = testNow.Add(time.Hour)
			mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""))
			assertAccountAutoResume(t, s, session.ID, true, "")
		})
	}
}

func TestSetTurnAutoResumePreservesUnknownSourceAccount(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(time.Minute)))
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(2*time.Minute)))
			reset := testNow.Add(time.Hour)
			mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, "turn-"+string(session.ID), &reset, true, ""))
			assertAccountAutoResume(t, s, session.ID, true, "")
		})
	}
}

func TestBindSessionAgentAccountRollsBackWithAutoResume(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			dataDir := t.TempDir()
			s := sqlitetest.MustOpenAt(t, dataDir)
			session, _ := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
			raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+failureStorePragmas)
			mustNoError(t, err)
			t.Cleanup(func() { _ = raw.Close() })
			_, err = raw.ExecContext(ctx, `CREATE TRIGGER reject_auto_resume_cancel
BEFORE UPDATE OF usage_limit_auto_resume ON conversation_turns
WHEN OLD.usage_limit_auto_resume = 1 AND NEW.usage_limit_auto_resume = 0
BEGIN SELECT RAISE(ABORT, 'injected consent cancellation failure'); END;`)
			mustNoError(t, err)
			if err := s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(time.Minute)); err == nil {
				t.Fatal("binding committed despite cancellation failure")
			}
			current, bound, err := s.GetSessionAgentAccount(ctx, session.ID, provider)
			mustNoError(t, err)
			if !bound || current != "account-a" {
				t.Fatalf("binding escaped rollback: %q, %v", current, bound)
			}
			assertAccountAutoResume(t, s, session.ID, true, "")
		})
	}
}

func TestBindSessionAgentAccountRacesWithAutoResumeIntent(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		for _, manualRetry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/manual_retry=%v", harness, manualRetry), func(t *testing.T) {
				ctx := context.Background()
				s := newTestStore(t)
				session, conversation := seedAccountAutoResume(t, s, harness)
				provider, _ := domain.AgentAccountProviderFor(harness)
				turnID := "turn-" + string(session.ID)
				mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
				start, results := make(chan struct{}), make(chan error, 2)
				go func() {
					<-start
					results <- s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(time.Minute))
				}()
				go func() {
					<-start
					if manualRetry {
						created, err := s.AppendRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root",
							domain.ConversationMessage{ID: "manual-message", Origin: domain.MessageOriginHuman, Text: "finish this task"},
							"manual-retry", turnID, testNow.Add(2*time.Minute))
						if err == nil && !created {
							err = errors.New("manual retry was not recorded")
						}
						results <- err
						return
					}
					reset := testNow.Add(time.Hour)
					err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, "")
					if errors.Is(err, ports.ErrAgentAccountChanged) {
						err = nil
					}
					results <- err
				}()
				close(start)
				for range 2 {
					mustNoError(t, <-results)
				}
				assertAccountAutoResume(t, s, session.ID, false, "")
				if manualRetry {
					id, found, err := s.RetryTurnIDForSource(ctx, conversation.ID, turnID)
					if err != nil || !found || id != "manual-retry" {
						t.Fatalf("manual retry changed: %q, %v, %v", id, found, err)
					}
				}
			})
		}
	}
}

func TestSetTurnAutoResumeCannotRearmRetriedSource(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		for _, concurrent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/concurrent=%v", harness, concurrent), func(t *testing.T) {
				ctx := context.Background()
				s := newTestStore(t)
				session, conversation := seedAccountAutoResume(t, s, harness)
				turnID := "turn-" + string(session.ID)
				retry := func() error {
					created, err := s.AppendRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root",
						domain.ConversationMessage{ID: "manual-message", Origin: domain.MessageOriginHuman, Text: "finish this task"},
						"manual-retry", turnID, testNow.Add(2*time.Minute))
					if err == nil && !created {
						return errors.New("manual retry was not recorded")
					}
					return err
				}
				reset := testNow.Add(time.Hour)
				if concurrent {
					start, results := make(chan struct{}), make(chan error, 2)
					go func() { <-start; results <- retry() }()
					go func() {
						<-start
						err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, "")
						if errors.Is(err, domain.ErrNoConversationTurn) {
							err = nil
						}
						results <- err
					}()
					close(start)
					for range 2 {
						mustNoError(t, <-results)
					}
				} else {
					mustNoError(t, retry())
				}
				if err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""); !errors.Is(err, domain.ErrNoConversationTurn) {
					t.Fatalf("re-arm retried source = %v", err)
				}
				assertAccountAutoResume(t, s, session.ID, false, "")
				id, found, err := s.RetryTurnIDForSource(ctx, conversation.ID, turnID)
				if err != nil || !found || id != "manual-retry" {
					t.Fatalf("retry receipt changed: %q, %v, %v", id, found, err)
				}
			})
		}
	}
}

func TestPostponeTurnAutoResumeNeverRestoresConsumedIntent(t *testing.T) {
	for _, state := range []string{"armed", "cancelled", "refused", "retried", "account round trip"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, domain.HarnessClaudeCode)
			turnID := "turn-" + string(session.ID)
			diagnostic := ""
			switch state {
			case "cancelled", "refused":
				if state == "refused" {
					diagnostic = "original refusal"
				}
				mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, turnID, nil, false, diagnostic))
			case "retried":
				created, err := s.AppendRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root",
					domain.ConversationMessage{ID: "manual-message", Origin: domain.MessageOriginHuman, Text: "finish this task"},
					"manual-retry", turnID, testNow.Add(2*time.Minute))
				mustNoError(t, err)
				if !created {
					t.Fatal("manual retry was not recorded")
				}
			case "account round trip":
				for i, account := range []string{"account-a", "account-b", "account-a"} {
					mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderClaude,
						account, testNow.Add(time.Duration(i-1)*time.Minute)))
				}
			}
			reset := testNow.Add(2 * time.Hour)
			before, err := s.LatestSeq(ctx)
			mustNoError(t, err)
			if moved, err := s.PostponeTurnAutoResume(ctx, "other-conversation", turnID, reset); err != nil || moved {
				t.Fatalf("postpone wrong conversation = %v, %v", moved, err)
			}
			if moved, err := s.PostponeTurnAutoResume(ctx, conversation.ID, "missing", reset); err != nil || moved {
				t.Fatalf("postpone missing turn = %v, %v", moved, err)
			}
			moved, err := s.PostponeTurnAutoResume(ctx, conversation.ID, turnID, reset)
			if err != nil || moved != (state == "armed") {
				t.Fatalf("postpone %s = %v, %v", state, moved, err)
			}
			events, err := s.EventsAfter(ctx, before, 10)
			mustNoError(t, err)
			wantEvents := 0
			if moved {
				wantEvents = 1
			}
			if len(events) != wantEvents {
				t.Fatalf("postpone CDC events = %+v, want %d", events, wantEvents)
			}
			if moved && (events[0].SessionID != string(session.ID) || events[0].Type != "session_updated") {
				t.Fatalf("postpone CDC attribution = %+v", events[0])
			}
			if state != "armed" {
				assertAccountAutoResume(t, s, session.ID, false, diagnostic)
				return
			}
			source, err := s.TurnByID(ctx, turnID)
			mustNoError(t, err)
			if source.UsageLimit == nil || !source.UsageLimit.AutoResume || source.UsageLimit.ResetsAt == nil ||
				!source.UsageLimit.ResetsAt.Equal(reset) || source.UsageLimit.ResumeError != "" {
				t.Fatalf("postponed intent = %+v", source.UsageLimit)
			}
		})
	}
}

func seedAccountAutoResume(t *testing.T, s *sqlite.Store, harness domain.AgentHarness) (domain.SessionRecord, domain.ConversationRecord) {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "account-resume")
	record := sampleRecord("account-resume")
	record.Harness, record.Mode = harness, domain.SessionModeChat
	record.CreatedAt, record.UpdatedAt = testNow.Add(-time.Hour), testNow
	record.Metadata.ControllerGeneration = "generation-root"
	record.Metadata.ProviderConversationID = "native-thread"
	session, err := s.CreateSession(ctx, record)
	mustNoError(t, err)
	conversation, err := s.CreateConversation(ctx, "conversation-"+string(session.ID), domain.ConversationScopeSession,
		"account-resume", session.ID, testNow)
	mustNoError(t, err)
	appendBranchPrompt(t, s, session, conversation, "generation-root", string(session.ID), "finish this task")
	turnID := "turn-" + string(session.ID)
	mustNoError(t, s.SettleTurnByID(ctx, turnID, domain.TurnStateFailed, "Usage limit reached", testNow.Add(time.Minute)))
	reset := testNow.Add(time.Hour)
	mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""))
	return session, conversation
}

func assertAccountAutoResume(t *testing.T, s *sqlite.Store, id domain.SessionID, armed bool, diagnostic string) {
	t.Helper()
	source, err := s.TurnByID(context.Background(), "turn-"+string(id))
	if err != nil || source.UsageLimit == nil {
		t.Fatalf("source = %+v, %v", source, err)
	}
	limit := source.UsageLimit
	if limit.AutoResume != armed || limit.ResumeError != diagnostic || limit.ResetsAt == nil ||
		!limit.ResetsAt.Equal(testNow.Add(time.Hour)) || source.ErrorMessage != "Usage limit reached" {
		t.Fatalf("source metadata changed: source=%+v limit=%+v", source, limit)
	}
}
