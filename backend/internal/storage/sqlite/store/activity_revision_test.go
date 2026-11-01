package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestActivityProjectionCannotOverwriteHumanMessageWithUnchangedTimestamp(t *testing.T) {
	for _, offset := range []time.Duration{0, -time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			s, sessionID, conversationID := conversationFixture(t)
			ctx := context.Background()
			before, _, err := s.GetSession(ctx, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			before.UpdatedAt = histClock.Add(time.Hour)
			before.Metadata.LatestUserPrompt = "old prompt"
			before.Metadata.LatestUserPromptAt = histClock
			before.Metadata.LatestAssistantUpdate = "old answer"
			before.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
			before.Metadata.ConversationCheckpointGeneration = "gen-1"
			before.Metadata.ConversationCheckpointNativeID = "native-1"
			if err := s.UpdateSession(ctx, before); err != nil {
				t.Fatal(err)
			}
			before, _, err = s.GetSession(ctx, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			created, err := s.AppendUserMessage(ctx, conversationID, sessionID, "gen-1", domain.ConversationMessage{
				ID: "new-message", Origin: domain.MessageOriginHuman, Text: "new prompt",
			}, "new-turn", before.UpdatedAt.Add(offset))
			if err != nil || !created {
				t.Fatalf("append message: created=%v err=%v", created, err)
			}
			after, _, err := s.GetSession(ctx, sessionID)
			if err != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("fixture must retain updated_at: before=%v after=%v err=%v", before.UpdatedAt, after.UpdatedAt, err)
			}
			applied, err := s.UpdateSessionFromActivitySignal(ctx, before, before.Revision)
			if err != nil || applied {
				t.Fatalf("stale projection overwrote human checkpoint: applied=%v err=%v", applied, err)
			}
			after, _, err = s.GetSession(ctx, sessionID)
			if err != nil || after.Metadata.LatestUserPrompt != "new prompt" || after.Metadata.LatestAssistantUpdate != "" {
				t.Fatalf("human checkpoint lost: %+v err=%v", after.Metadata, err)
			}
		})
	}
}

func TestSemanticDeliveryAcceptanceRequiresCurrentRevisionAndLaunch(t *testing.T) {
	s, sessionID, _ := semanticDeliveryFixture(t)
	ctx := context.Background()
	const deliveryID = "report-batch:fenced1"
	if registered, err := s.RegisterSemanticDelivery(ctx, sessionID, "launch-1", deliveryID); err != nil || !registered {
		t.Fatalf("register receipt: registered=%v err=%v", registered, err)
	}
	stale, found, err := s.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	newer := stale
	newer.Metadata.LatestUserPrompt = "later human prompt"
	newer.Metadata.LatestUserPromptAt = time.Now().UTC()
	newer.UpdatedAt = newer.Metadata.LatestUserPromptAt
	if err := s.UpdateSession(ctx, newer); err != nil {
		t.Fatalf("write newer prompt: %v", err)
	}
	if applied, err := s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, stale, stale.Revision, "launch-1", deliveryID); err != nil || applied {
		t.Fatalf("stale revision projection: applied=%v err=%v", applied, err)
	}
	if accepted, err := s.SemanticDeliveryAccepted(ctx, sessionID, "launch-1", deliveryID); err != nil || accepted {
		t.Fatalf("stale revision accepted receipt: accepted=%v err=%v", accepted, err)
	}

	ownerBefore, found, err := s.GetSession(ctx, sessionID)
	if err != nil || !found {
		t.Fatalf("reload session: found=%v err=%v", found, err)
	}
	ownerAfter := ownerBefore
	ownerAfter.Metadata.RuntimeLaunchID = "launch-2"
	if err := s.UpdateSession(ctx, ownerAfter); err != nil {
		t.Fatalf("transfer runtime launch: %v", err)
	}
	if applied, err := s.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(ctx, ownerBefore, ownerBefore.Revision, "launch-1", deliveryID); err != nil || applied {
		t.Fatalf("stale launch projection: applied=%v err=%v", applied, err)
	}
	if accepted, err := s.SemanticDeliveryAccepted(ctx, sessionID, "launch-1", deliveryID); err != nil || accepted {
		t.Fatalf("stale launch accepted receipt: accepted=%v err=%v", accepted, err)
	}
}
