package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLatestPrimaryTurnUsesDispatchArchiveOrderAndCurrentOwner(t *testing.T) {
	for _, scenario := range []string{"completed", "failed", "unbound failed", "known older failure", "newer native failure", "child", "compaction", "queued", "cancelled", "rolled back", "promoted", "fenced", "old branch", "foreign owner", "legacy uncertainty", "marked success supersedes legacy uncertainty", "legacy guard cannot fall through", "reordered bound failure", "reordered unbound failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s, session, conversation := seededChatConversation(t)
			const generation = "generation-root"
			archive := func(method, id, native, thread string, project func(context.Context) error) {
				t.Helper()
				payload := fmt.Sprintf(`{"turnId":%q,"providerTurnId":%q,"providerConversationId":%q}`, "turn-"+id, native, thread)
				if applied, err := s.ProjectProviderEvent(ctx, conversation.ID, session.ID, generation, method+"/"+id, method, payload, testNow, project); err != nil || !applied {
					t.Fatalf("archive %s/%s: applied=%v err=%v", method, id, applied, err)
				}
			}
			mark := func(id string, bind, recorded bool) {
				t.Helper()
				project := func(tx context.Context) error {
					if bind {
						return s.BindTurnToProvider(tx, "turn-"+id, "native-"+id, testNow)
					}
					return s.MarkTurnDispatching(tx, "turn-"+id)
				}
				if recorded {
					archive("turn.dispatching", id, "", "thread-root", project)
				} else if err := project(ctx); err != nil {
					t.Fatal(err)
				}
			}
			settle := func(id string, state domain.TurnState) {
				t.Helper()
				archive("turn.completed", id, "native-"+id, "thread-root", func(tx context.Context) error {
					return s.SettleTurn(tx, conversation.ID, "native-"+id, state, "", testNow)
				})
			}
			prompt := func(id string) { appendBranchPrompt(t, s, session, conversation, generation, id, id) }
			prompt("base")
			mark("base", true, false) // A completion saved by the previous build.
			settle("base", domain.TurnStateCompleted)
			want, wantState := "turn-base", domain.TurnStateCompleted
			switch scenario {
			case "failed", "unbound failed":
				prompt("new")
				mark("new", scenario == "failed", true)
				if err := s.SettleTurnByID(ctx, "turn-new", domain.TurnStateFailed, "send failed", testNow); err != nil {
					t.Fatal(err)
				}
				want, wantState = "turn-new", domain.TurnStateFailed
			case "known older failure", "newer native failure":
				prompt("failed")
				mark("failed", true, false)
				settle("failed", domain.TurnStateFailed)
				want, wantState = "turn-failed", domain.TurnStateFailed
				if scenario == "known older failure" {
					prompt("later")
					mark("later", true, false)
					settle("later", domain.TurnStateCompleted)
					want, wantState = "turn-later", domain.TurnStateCompleted
				}
			case "child", "compaction":
				if err := s.AdoptProviderTurn(ctx, conversation.ID, session.ID, generation, "turn-native", "native-work", testNow); err != nil {
					t.Fatal(err)
				}
				thread := "child-thread"
				if scenario == "compaction" {
					thread, want, wantState = "thread-root", "turn-native", domain.TurnStateRunning
				} else if err := s.AppendImportedUserMessage(ctx, conversation.ID, "native-work", domain.ConversationMessage{
					ID: "imported-child", ProviderItemID: "child-user-item", Text: "child prompt",
				}, testNow); err != nil {
					t.Fatal(err)
				}
				archive("turn.started", "native", "native-work", thread, func(context.Context) error { return nil })
			case "queued", "cancelled", "rolled back", "promoted":
				prompt("discarded")
				archive("turn.dispatching", "discarded", "", "thread-root", func(context.Context) error { return nil })
				switch scenario {
				case "cancelled":
					if err := s.CancelQueuedTurnByID(ctx, conversation.ID, "turn-discarded", testNow); err != nil {
						t.Fatal(err)
					}
				case "rolled back":
					mark("discarded", true, false)
					if _, err := s.RollbackTurns(ctx, conversation.ID, "turn-discarded", testNow); err != nil {
						t.Fatal(err)
					}
				case "promoted":
					if _, err := s.ReserveQueuedTurnForPromotion(ctx, conversation.ID, "turn-discarded", testNow); err != nil {
						t.Fatal(err)
					}
					if err := s.CompleteQueuedTurnPromotion(ctx, conversation.ID, "turn-discarded", "native-base", domain.ConversationActivity{
						ID: "promotion", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted, Summary: "steer",
					}, testNow); err != nil {
						t.Fatal(err)
					}
				}
			case "old branch":
				branch := domain.ConversationBranch{ID: "new-branch", ConversationID: conversation.ID, SessionID: session.ID,
					ProviderConversationID: "thread-new", ParentBranchID: conversation.ActiveBranchID}
				if err := s.CreateConversationBranch(ctx, branch, testNow); err != nil {
					t.Fatal(err)
				}
				activateTestBranch(t, s, session, conversation, branch.ID, branch.ProviderConversationID, generation)
				want = ""
			case "foreign owner":
				foreign, err := s.CreateSession(ctx, sampleRecord("branches"))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.AdoptProviderTurn(ctx, conversation.ID, foreign.ID, generation, "turn-foreign", "native-foreign", testNow); err != nil {
					t.Fatal(err)
				}
				archive("turn.completed", "foreign", "native-foreign", "thread-root", func(tx context.Context) error {
					return s.SettleTurn(tx, conversation.ID, "native-foreign", domain.TurnStateCompleted, "", testNow)
				})
			case "legacy uncertainty", "marked success supersedes legacy uncertainty", "legacy guard cannot fall through":
				if scenario == "legacy guard cannot fall through" {
					prompt("marked")
					mark("marked", true, true)
					settle("marked", domain.TurnStateCompleted)
					prompt("legacy")
					mark("legacy", true, false)
					settle("legacy", domain.TurnStateCompleted)
				}
				prompt("uncertain")
				mark("uncertain", false, false)
				if err := s.SettleTurnByID(ctx, "turn-uncertain", domain.TurnStateFailed, "unknown delivery", testNow); err != nil {
					t.Fatal(err)
				}
				want = ""
				if scenario == "marked success supersedes legacy uncertainty" {
					prompt("new")
					mark("new", true, true)
					settle("new", domain.TurnStateCompleted)
					want = "turn-new"
				}
			case "reordered bound failure", "reordered unbound failure":
				for i, id := range []string{"b", "c"} {
					if created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, generation,
						domain.ConversationMessage{ID: "message-" + id, Origin: domain.MessageOriginHuman, Text: id}, "turn-"+id, testNow.Add(time.Duration(i+1)*time.Second)); err != nil || !created {
						t.Fatalf("queue %s: %v %v", id, created, err)
					}
				}
				if err := s.ReorderQueuedTurns(ctx, conversation.ID, []string{"turn-c", "turn-b"}); err != nil {
					t.Fatal(err)
				}
				if next, err := s.NextQueuedTurn(ctx, conversation.ID); err != nil || next.TurnID != "turn-c" {
					t.Fatalf("reordered head: %+v %v", next, err)
				}
				mark("c", true, true)
				settle("c", domain.TurnStateCompleted)
				mark("b", scenario == "reordered bound failure", true)
				if scenario == "reordered bound failure" {
					settle("b", domain.TurnStateFailed)
				} else if err := s.SettleTurnByID(ctx, "turn-b", domain.TurnStateFailed, "later dispatch failed", testNow); err != nil {
					t.Fatal(err)
				}
				want, wantState = "turn-b", domain.TurnStateFailed
			}
			if err := s.ClaimChatControllerGeneration(ctx, session.ID, "replacement-generation"); err != nil {
				t.Fatal(err)
			}
			queryGeneration := "replacement-generation"
			if scenario == "fenced" {
				queryGeneration, want = generation, ""
			}
			got, found, err := s.LatestPrimaryTurn(ctx, conversation.ID, "thread-root", queryGeneration)
			if err != nil || found != (want != "") || (found && (got.ID != want || got.State != wantState)) {
				t.Fatalf("latest primary = %+v found=%v err=%v; want %q %s", got, found, err, want, wantState)
			}
		})
	}
}

func TestLatestPrimaryTurnUsesReviewerFenceAndProjectionTransaction(t *testing.T) {
	ctx := context.Background()
	s, session, _ := seededChatConversation(t)
	review := domain.Review{ID: "primary-review", SessionID: session.ID, ProjectID: session.ProjectID, Harness: domain.ReviewerCodex, CreatedAt: testNow, UpdatedAt: testNow}
	if err := s.UpsertReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateReviewConversation(ctx, "primary-review-conversation", review.ID, session.ProjectID, session.ID, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimReviewChatController(ctx, review.ID, "review-thread", "review-generation", testNow); err != nil || !ok {
		t.Fatalf("claim review: %v %v", ok, err)
	}
	if created, err := s.AppendReviewUserMessage(ctx, conversation.ID, session.ID, review.ID, "review-generation", domain.ConversationMessage{
		ID: "review-message", Text: "review", Origin: domain.MessageOriginHuman,
	}, "review-turn", testNow); err != nil || !created {
		t.Fatalf("review prompt: %v %v", created, err)
	}
	_, err = s.ProjectReviewProviderEvent(ctx, conversation.ID, session.ID, review.ID, "review-generation", "review-dispatch", "turn.dispatching", `{"turnId":"review-turn","providerConversationId":"review-thread"}`, testNow, func(tx context.Context) error {
		if err := s.BindTurnToProvider(tx, "review-turn", "review-native", testNow); err != nil {
			return err
		}
		got, found, err := s.LatestPrimaryTurn(tx, conversation.ID, "review-thread", "review-generation")
		if err != nil || !found || got.ID != "review-turn" {
			return fmt.Errorf("transaction read: %+v %v: %w", got, found, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimReviewChatController(ctx, review.ID, "review-thread", "replacement-review-generation", testNow); err != nil || !ok {
		t.Fatalf("claim replacement review: %v %v", ok, err)
	}
	for _, generation := range []string{"generation-root", "", "review-generation", "replacement-review-generation"} {
		got, found, err := s.LatestPrimaryTurn(ctx, conversation.ID, "review-thread", generation)
		if err != nil || found != (generation == "replacement-review-generation") || (found && got.ID != "review-turn") {
			t.Fatalf("review fence %q: %+v %v %v", generation, got, found, err)
		}
	}
}
