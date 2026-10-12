package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSessionInteractionValidatesStoredSenderAndPreservesHumanFacts(t *testing.T) {
	for _, tc := range []struct {
		name, project                           string
		kind                                    domain.SessionKind
		unknown, terminated, targetOrchestrator bool
		counts                                  bool
	}{
		{name: "project orchestrator", project: "hist", kind: domain.KindOrchestrator, counts: true},
		{name: "worker report", project: "hist", kind: domain.KindWorker},
		{name: "cross project", project: "other", kind: domain.KindOrchestrator},
		{name: "unknown sender", unknown: true},
		{name: "terminated orchestrator", project: "hist", kind: domain.KindOrchestrator, terminated: true},
		{name: "orchestrator target", project: "hist", kind: domain.KindOrchestrator, targetOrchestrator: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, id, _ := conversationFixture(t)
			target, _, err := st.GetSession(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			target.Metadata.LatestUserPrompt = "human task"
			target.Metadata.LatestUserPromptAt = histClock
			if tc.targetOrchestrator {
				target.Kind = domain.KindOrchestrator
			}
			if err := st.UpdateSession(ctx, target); err != nil {
				t.Fatal(err)
			}
			sender := "unknown"
			if !tc.unknown {
				if tc.project != "hist" {
					seedProject(t, st, tc.project)
				}
				rec := sampleRecord(tc.project)
				rec.Kind = tc.kind
				rec.IsTerminated = tc.terminated
				source, err := st.CreateSession(ctx, rec)
				if err != nil {
					t.Fatal(err)
				}
				sender = string(source.ID)
			}
			before, err := st.EventsAfter(ctx, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			at := histClock.Add(time.Hour)
			if err := st.RecordSessionInteraction(ctx, id, sender, at); err != nil {
				t.Fatal(err)
			}
			got, _, err := st.GetSession(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.counts != got.Metadata.LatestInteractionAt.Equal(at) {
				t.Fatalf("interaction=%v counts=%v", got.Metadata.LatestInteractionAt, tc.counts)
			}
			if got.Metadata.LatestUserPrompt != "human task" || !got.Metadata.LatestUserPromptAt.Equal(histClock) {
				t.Fatal("changed human prompt facts")
			}
			events, err := st.EventsAfter(ctx, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			extra := 0
			if tc.counts {
				extra = 1
			}
			if len(events) != len(before)+extra {
				t.Fatalf("CDC delta=%d want=%d", len(events)-len(before), extra)
			}
			// Replay and out-of-order facts never refresh age; full row writers cannot clobber it.
			if err := st.RecordSessionInteraction(ctx, id, sender, at.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateSession(ctx, target); err != nil {
				t.Fatal(err)
			}
			got, _, _ = st.GetSession(ctx, id)
			if tc.counts && !got.Metadata.LatestInteractionAt.Equal(at) {
				t.Fatalf("stale write clobbered interaction: %v", got.Metadata.LatestInteractionAt)
			}
		})
	}
}

func TestChatQueuedOrchestrationInteractionUsesOriginalAcceptance(t *testing.T) {
	ctx := context.Background()
	st, id, conv := conversationFixture(t)
	rec := sampleRecord("hist")
	rec.Kind = domain.KindOrchestrator
	sender, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	accepted := histClock.Add(time.Hour)
	msg := domain.ConversationMessage{ID: "queued-message", Origin: domain.MessageOriginAutomation, Text: "deliberate direction", SenderSessionID: string(sender.ID), InteractionAt: accepted, ClientMessageID: "send-1"}
	created, err := st.AppendUserMessage(ctx, conv, id, "gen-1", msg, "queued-turn", accepted.Add(time.Hour))
	if err != nil || !created {
		t.Fatalf("append=%v err=%v", created, err)
	}
	if _, err := st.AppendUserMessage(ctx, conv, id, "gen-1", msg, "duplicate-turn", accepted.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _, _ := st.GetSession(ctx, id)
	if !got.Metadata.LatestInteractionAt.Equal(accepted) || !got.Metadata.LatestUserPromptAt.IsZero() {
		t.Fatalf("facts=%+v", got.Metadata)
	}
	snapshot, err := st.LoadConversationSnapshot(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Origin != domain.MessageOriginAutomation || snapshot.Messages[0].SenderSessionID != string(sender.ID) {
		t.Fatalf("history=%+v", snapshot.Messages)
	}
	// Routine automation with neither sender nor user authorship cannot advance age.
	msg.ID = "routine-message"
	msg.ClientMessageID = "routine"
	msg.SenderSessionID = ""
	msg.Text = "review feedback"
	if _, err := st.AppendUserMessage(ctx, conv, id, "gen-1", msg, "routine-turn", accepted.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _, _ = st.GetSession(ctx, id)
	if !got.Metadata.LatestInteractionAt.Equal(accepted) {
		t.Fatal("routine automation refreshed age")
	}
}

func TestAcceptedSteeringInteractionIsIdempotentAndTruthful(t *testing.T) {
	for _, tc := range []struct {
		name          string
		kind          domain.SessionKind
		human, counts bool
	}{
		{name: "human", human: true, counts: true}, {name: "orchestrator", kind: domain.KindOrchestrator, counts: true}, {name: "worker", kind: domain.KindWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, id, conv := conversationFixture(t)
			detail := map[string]string{"event": "steer", "origin": "human"}
			if !tc.human {
				rec := sampleRecord("hist")
				rec.Kind = tc.kind
				sender, err := st.CreateSession(ctx, rec)
				if err != nil {
					t.Fatal(err)
				}
				detail["senderSessionId"] = string(sender.ID)
				detail["origin"] = "automation"
			}
			raw, _ := json.Marshal(detail)
			activity := domain.ConversationActivity{ID: "steer-1", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted, Detail: raw, ProviderItemID: "steer:delivery-1"}
			at := histClock.Add(time.Hour)
			if err := st.UpsertActivity(ctx, conv, "", activity, at); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertActivity(ctx, conv, "", activity, at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			got, _, _ := st.GetSession(ctx, id)
			if tc.counts != got.Metadata.LatestInteractionAt.Equal(at) {
				t.Fatalf("interaction=%v", got.Metadata.LatestInteractionAt)
			}
			if !got.Metadata.LatestUserPromptAt.IsZero() {
				t.Fatal("steer altered human-only prompt facts")
			}
		})
	}
}

func TestTransitionOutboxRecordsOrchestratorInteractionAtAcceptance(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			st, id, _ := conversationFixture(t)
			source := sampleRecord("hist")
			source.Kind = kind
			sender, err := st.CreateSession(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			transition, _, err := st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
				ID: "transition-direction", SessionID: id, SourceMode: domain.SessionModeChat, TargetMode: domain.SessionModeTUI,
				Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
				Phase: domain.SessionInterfaceTransitionRequested, CreatedAt: histClock, UpdatedAt: histClock,
			})
			if err != nil {
				t.Fatal(err)
			}
			at := histClock.Add(time.Hour)
			if err := st.EnqueueSessionInterfaceTransitionMessage(ctx, transition.ID, "direction-1", "do the next step", at, ports.MessageDeliveryOptions{SenderSessionID: string(sender.ID)}); err != nil {
				t.Fatal(err)
			}
			got, _, _ := st.GetSession(ctx, id)
			if (kind == domain.KindOrchestrator) != got.Metadata.LatestInteractionAt.Equal(at) {
				t.Fatalf("interaction=%v kind=%s", got.Metadata.LatestInteractionAt, kind)
			}
			messages, err := st.ListPendingSessionInterfaceTransitionMessages(ctx, transition.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].SenderSessionID != string(sender.ID) || messages[0].AuthoredByUser || !messages[0].CreatedAt.Equal(at) {
				t.Fatalf("queue=%+v", messages)
			}
			if err := st.MarkSessionInterfaceTransitionMessageDelivered(ctx, messages[0].ID, at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			after, _, _ := st.GetSession(ctx, id)
			if !after.Metadata.LatestInteractionAt.Equal(got.Metadata.LatestInteractionAt) {
				t.Fatal("queue replay refreshed age")
			}
		})
	}
}

func TestQueuedUserAuthoredAnnotationRetainsHumanAuthorshipAndTime(t *testing.T) {
	ctx := context.Background()
	st, id, conv := conversationFixture(t)
	at := histClock.Add(time.Hour)
	msg := domain.ConversationMessage{ID: "annotation", Origin: domain.MessageOriginAutomation, AuthoredByUser: true, Text: "user feedback", InteractionAt: at, ClientMessageID: "annotation-1"}
	if _, err := st.AppendUserMessage(ctx, conv, id, "gen-1", msg, "annotation-turn", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _, _ := st.GetSession(ctx, id)
	if got.Metadata.LatestUserPrompt != "user feedback" || !got.Metadata.LatestUserPromptAt.Equal(at) {
		t.Fatalf("human facts=%+v", got.Metadata)
	}
	snapshot, err := st.LoadConversationSnapshot(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Origin != domain.MessageOriginAutomation {
		t.Fatalf("history=%+v", snapshot.Messages)
	}
}
