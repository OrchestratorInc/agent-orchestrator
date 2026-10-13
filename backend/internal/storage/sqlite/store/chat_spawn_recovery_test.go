package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The recovery source is proven before provider I/O; the publication commit
// must re-prove it inside its transaction. These cases run the real
// CommitChatSpawnPrepared against the real rebinding shape: the project
// conversation already belongs to the replacement, while the active branch
// still records the terminated predecessor's provider identity.
func TestCommitChatSpawnRecoverySourceReproof(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	seed := func(t *testing.T) (*store.Store, domain.SessionRecord, domain.SessionRecord, domain.ConversationRecord) {
		t.Helper()
		st := newTestStore(t)
		seedProject(t, st, "recovery")
		previous := sampleRecord("recovery")
		previous.Kind = domain.KindOrchestrator
		previous.Mode = domain.SessionModeChat
		previous.IsTerminated = true
		previous.Metadata.ProviderConversationID = "stale-thread"
		previous.Metadata.ControllerGeneration = "previous-generation"
		previous.CreatedAt = now
		previous.UpdatedAt = now
		prevRec, err := st.CreateSession(ctx, previous)
		if err != nil {
			t.Fatal(err)
		}
		conversation, err := st.CreateConversation(ctx, "recovery-conversation",
			domain.ConversationScopeProject, prevRec.ProjectID, prevRec.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendUserMessage(ctx, conversation.ID, prevRec.ID,
			"previous-generation", domain.ConversationMessage{
				ID: "old-user-message", Text: "predecessor transcript",
				Origin: domain.MessageOriginHuman,
			}, "old-turn", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		replacement := sampleRecord("recovery")
		replacement.Kind = domain.KindOrchestrator
		replacement.Mode = domain.SessionModeChat
		replacement.Metadata.ProviderConversationID = "real-thread"
		replacement.CreatedAt = now.Add(time.Hour)
		replacement.UpdatedAt = now.Add(time.Hour)
		target, err := st.CreateSession(ctx, replacement)
		if err != nil {
			t.Fatal(err)
		}
		// The project conversation is already rebound: the replacement owns it,
		// while the stale root still records the predecessor's identity.
		conversation, err = st.CreateConversation(ctx, "unused",
			domain.ConversationScopeProject, target.ProjectID, target.ID, now.Add(2*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return st, prevRec, target, conversation
	}

	recoveryHandoff := func(target domain.SessionRecord, prevRec domain.SessionRecord, conversation domain.ConversationRecord) *domain.ChatProviderHandoff {
		return &domain.ChatProviderHandoff{
			BoundaryID: "recovery-epoch", ConversationID: conversation.ID,
			PreviousSessionID: target.ID, PreviousBranchID: conversation.ActiveBranchID,
			PreviousSequence: conversation.LatestSequence,
			ExpectedControllerOwner: target.ControllerOwner(),
			RecoverySource: &domain.ChatProviderHandoffRecoverySource{
				SessionID: prevRec.ID, ProviderConversationID: "stale-thread",
			},
		}
	}
	recoveryBoundary := func(target domain.SessionRecord, conversation domain.ConversationRecord) domain.ConversationBranch {
		return domain.ConversationBranch{
			ID: "recovery-epoch", ConversationID: conversation.ID, SessionID: target.ID,
			ProviderConversationID: "real-thread", ParentBranchID: conversation.ActiveBranchID,
			ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "recovery-epoch",
			ProviderIDsScoped: true, CreatedAt: now.Add(3 * time.Hour),
		}
	}
	assertUnchanged := func(t *testing.T, st *store.Store, target domain.SessionRecord, conversation domain.ConversationRecord) {
		t.Helper()
		after, err := st.ConversationForSession(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.ActiveBranchID != conversation.ActiveBranchID || after.LatestSequence != conversation.LatestSequence {
			t.Fatalf("failed commit changed the head: %+v want active %s", after, conversation.ActiveBranchID)
		}
		if _, err := st.ConversationBranch(ctx, conversation.ID, "recovery-epoch"); !errors.Is(err, domain.ErrNoConversationBranch) {
			t.Fatalf("failed commit left a recovery boundary: %v", err)
		}
		if rec, _, err := st.GetSession(ctx, target.ID); err != nil ||
			rec.Metadata.ControllerGeneration == "recovery-generation" {
			t.Fatalf("failed commit published the replacement: %+v err=%v", rec, err)
		}
	}

	t.Run("success appends the recovery epoch and preserves the predecessor branch", func(t *testing.T) {
		st, prevRec, target, conversation := seed(t)
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		if err := st.CommitChatSpawnPrepared(ctx, rec, recoveryBoundary(target, conversation),
			recoveryHandoff(target, prevRec, conversation), func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		after, err := st.ConversationForSession(ctx, target.ID)
		if err != nil || after.ActiveBranchID != "recovery-epoch" {
			t.Fatalf("recovery epoch not active: %+v err=%v", after, err)
		}
		stale, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
		if err != nil {
			t.Fatal(err)
		}
		if stale.Active || stale.SessionID != prevRec.ID || stale.ProviderConversationID != "stale-thread" {
			t.Fatalf("predecessor branch was rewritten: %+v", stale)
		}
		if stale.ParentBranchID != "" {
			t.Fatalf("predecessor branch was re-parented: %+v", stale)
		}
		boundary, err := st.ConversationBranch(ctx, conversation.ID, "recovery-epoch")
		if err != nil {
			t.Fatal(err)
		}
		if boundary.ParentBranchID != conversation.ActiveBranchID || !boundary.Active ||
			boundary.SessionID != target.ID || boundary.ProviderConversationID != "real-thread" {
			t.Fatalf("recovery epoch = %+v", boundary)
		}
	})

	t.Run("stale recovery handle fails closed", func(t *testing.T) {
		st, prevRec, target, conversation := seed(t)
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		handoff := recoveryHandoff(target, prevRec, conversation)
		handoff.RecoverySource.ProviderConversationID = "already-adopted"
		err := st.CommitChatSpawnPrepared(ctx, rec, recoveryBoundary(target, conversation),
			handoff, func(context.Context) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "recovery source changed") {
			t.Fatalf("stale recovery handle error = %v", err)
		}
		assertUnchanged(t, st, target, conversation)
	})

	t.Run("revived predecessor fails closed after provider I/O", func(t *testing.T) {
		st, prevRec, target, conversation := seed(t)
		prevRec.IsTerminated = false
		if err := st.UpdateSession(ctx, prevRec); err != nil {
			t.Fatal(err)
		}
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		err := st.CommitChatSpawnPrepared(ctx, rec, recoveryBoundary(target, conversation),
			recoveryHandoff(target, prevRec, conversation), func(context.Context) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "not a retired predecessor") {
			t.Fatalf("revived predecessor error = %v", err)
		}
		assertUnchanged(t, st, target, conversation)
	})

	t.Run("competing live orchestrator fails closed after provider I/O", func(t *testing.T) {
		st, prevRec, target, conversation := seed(t)
		competitor := sampleRecord("recovery")
		competitor.Kind = domain.KindOrchestrator
		competitor.CreatedAt = now.Add(2 * time.Hour)
		if _, err := st.CreateSession(ctx, competitor); err != nil {
			t.Fatal(err)
		}
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		err := st.CommitChatSpawnPrepared(ctx, rec, recoveryBoundary(target, conversation),
			recoveryHandoff(target, prevRec, conversation), func(context.Context) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "competing live orchestrator") {
			t.Fatalf("competing orchestrator error = %v", err)
		}
		assertUnchanged(t, st, target, conversation)
	})

	t.Run("moved head fails closed after provider I/O", func(t *testing.T) {
		st, prevRec, target, conversation := seed(t)
		// A live worker (not an orchestrator) moves the head so the head CAS is
		// what rejects this commit, not the sole-orchestrator guard.
		competitor := sampleRecord("recovery")
		competitor.Kind = domain.KindWorker
		competitor.Mode = domain.SessionModeChat
		competitor.Metadata.ProviderConversationID = "other-thread"
		competitor.CreatedAt = now.Add(2 * time.Hour)
		competitor.UpdatedAt = now.Add(2 * time.Hour)
		competitorRec, err := st.CreateSession(ctx, competitor)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateAndActivateConversationBranch(ctx, competitorRec.ID, domain.ConversationBranch{
			ID: "competing-epoch", ConversationID: conversation.ID, SessionID: competitorRec.ID,
			ProviderConversationID: "other-thread", ParentBranchID: conversation.ActiveBranchID,
			ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "competing-epoch",
			ProviderIDsScoped: true, CreatedAt: now.Add(2 * time.Hour),
		}, "competing-generation", now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		err = st.CommitChatSpawnPrepared(ctx, rec, recoveryBoundary(target, conversation),
			recoveryHandoff(target, prevRec, conversation), func(context.Context) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "active branch changed") {
			t.Fatalf("moved head error = %v", err)
		}
	})

	t.Run("history growth during provider I/O fails closed", func(t *testing.T) {
		st, prevRec, target, conversation := seed(t)
		if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
			ID: "concurrent-activity", ProviderItemID: "concurrent-activity",
			Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		}, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		err := st.CommitChatSpawnPrepared(ctx, rec, recoveryBoundary(target, conversation),
			recoveryHandoff(target, prevRec, conversation), func(context.Context) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "handoff history changed") {
			t.Fatalf("history growth error = %v", err)
		}
		// The concurrent activity legitimately moved the head's sequence, so only
		// the branch topology and the unpublished replacement are asserted here.
		after, err := st.ConversationForSession(ctx, target.ID)
		if err != nil || after.ActiveBranchID != conversation.ActiveBranchID {
			t.Fatalf("failed commit moved the head: %+v err=%v", after, err)
		}
		if _, err := st.ConversationBranch(ctx, conversation.ID, "recovery-epoch"); !errors.Is(err, domain.ErrNoConversationBranch) {
			t.Fatalf("failed commit left a recovery boundary: %v", err)
		}
	})

	t.Run("unrebound transition handoff keeps its rebind path", func(t *testing.T) {
		// The Terminal -> Chat handoff binds the conversation inside the commit;
		// the new recovery-source re-proof must not disturb that shape.
		st := newTestStore(t)
		seedProject(t, st, "recovery")
		previous := sampleRecord("recovery")
		previous.Kind = domain.KindOrchestrator
		previous.Mode = domain.SessionModeChat
		previous.IsTerminated = true
		previous.Metadata.ProviderConversationID = "stale-thread"
		previous.CreatedAt = now
		previous.UpdatedAt = now
		prevRec, err := st.CreateSession(ctx, previous)
		if err != nil {
			t.Fatal(err)
		}
		conversation, err := st.CreateConversation(ctx, "handoff-conversation",
			domain.ConversationScopeProject, prevRec.ProjectID, prevRec.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		replacement := sampleRecord("recovery")
		replacement.Kind = domain.KindOrchestrator
		replacement.Mode = domain.SessionModeChat
		replacement.Metadata.ProviderConversationID = "real-thread"
		replacement.CreatedAt = now.Add(time.Hour)
		replacement.UpdatedAt = now.Add(time.Hour)
		target, err := st.CreateSession(ctx, replacement)
		if err != nil {
			t.Fatal(err)
		}
		rec := target
		rec.Metadata.ControllerGeneration = "recovery-generation"
		handoff := &domain.ChatProviderHandoff{
			BoundaryID: "handoff-epoch", ConversationID: conversation.ID,
			PreviousSessionID: prevRec.ID, PreviousBranchID: conversation.ActiveBranchID,
			PreviousSequence: conversation.LatestSequence,
			ExpectedControllerOwner: target.ControllerOwner(),
			RecoverySource: &domain.ChatProviderHandoffRecoverySource{
				SessionID: prevRec.ID, ProviderConversationID: "stale-thread",
			},
		}
		boundary := domain.ConversationBranch{
			ID: "handoff-epoch", ConversationID: conversation.ID, SessionID: target.ID,
			ProviderConversationID: "real-thread", ParentBranchID: conversation.ActiveBranchID,
			ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "handoff-epoch",
			ProviderIDsScoped: true, CreatedAt: now.Add(2 * time.Hour),
		}
		if err := st.CommitChatSpawnPrepared(ctx, rec, boundary, handoff,
			func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		after, err := st.ConversationForSession(ctx, target.ID)
		if err != nil || after.ActiveBranchID != "handoff-epoch" {
			t.Fatalf("handoff epoch not active: %+v err=%v", after, err)
		}
	})
}
