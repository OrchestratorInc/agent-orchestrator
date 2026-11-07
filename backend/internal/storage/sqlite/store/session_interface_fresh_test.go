package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFreshInterfaceEpochReleasesOnlyUntouchedRootProvider(t *testing.T) {
	for _, name := range []string{"fresh", "provider turn", "stale mode"} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			seedProject(t, st, "fresh-switch")
			now := time.Now()
			rec := sampleRecord("fresh-switch")
			rec.Mode = domain.SessionModeChat
			rec.Metadata.ProviderConversationID = "reserved-chat-id"
			rec.Metadata.ControllerGeneration = "chat-generation"
			sess, err := st.CreateSession(ctx, rec)
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := st.CreateConversation(ctx, "fresh-conversation", domain.ConversationScopeSession,
				sess.ProjectID, sess.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			before, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "provider turn" {
				if err := st.AdoptProviderTurn(ctx, conversation.ID, sess.ID, "chat-generation", "turn-1", "provider-turn-1", now); err != nil {
					t.Fatal(err)
				}
			}
			source := domain.SessionModeChat
			if name == "stale mode" {
				source = domain.SessionModeTUI
			}
			changed, err := st.CommitSessionControllerEpoch(ctx, sess.ID, source, domain.SessionModeTUI, "", now)
			if name == "fresh" {
				if err != nil || !changed {
					t.Fatalf("fresh epoch changed=%v err=%v", changed, err)
				}
			} else if changed || (name == "provider turn" && err == nil) {
				t.Fatalf("unsafe epoch changed=%v err=%v", changed, err)
			}
			after, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			current, _, err := st.GetSession(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "fresh" {
				if after.ProviderConversationID != "" || after.ProviderScopeID == before.ProviderScopeID || after.ProviderScopeID == "" {
					t.Fatalf("unused provider binding was retained: before=%+v after=%+v", before, after)
				}
				if after.ID != before.ID || current.Mode != domain.SessionModeTUI {
					t.Fatalf("fresh root was replaced or mode not committed: branch=%+v mode=%s", after, current.Mode)
				}
			} else if after.ProviderConversationID != before.ProviderConversationID ||
				after.ProviderScopeID != before.ProviderScopeID || current.Mode != domain.SessionModeChat ||
				current.Metadata.ProviderConversationID != "reserved-chat-id" {
				t.Fatalf("failed epoch changed ownership: branch=%+v session=%+v", after, current)
			}
		})
	}
}

// A degraded identity-reset fresh TUI keeps a Chat that already carries real
// messages archived exactly as it is, provider binding included, instead of
// failing the untouched-Chat release proof (upstream issue #7724). The strict
// commit keeps requiring the untouched proof.
func TestArchivedChatInterfaceEpochKeepsMessagedChatArchived(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "archived-chat")
	now := time.Now()
	rec := sampleRecord("archived-chat")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ProviderConversationID = "reserved-chat-id"
	rec.Metadata.ControllerGeneration = "chat-generation"
	sess, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateConversation(ctx, "chat-conversation", domain.ConversationScopeSession,
		sess.ProjectID, sess.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdoptProviderTurn(ctx, conversation.ID, sess.ID, "chat-generation", "turn-1", "provider-turn-1", now); err != nil {
		t.Fatal(err)
	}

	// The strict commit still refuses to discard a Chat with history.
	if changed, err := st.CommitSessionControllerEpoch(ctx, sess.ID, domain.SessionModeChat, domain.SessionModeTUI, "", now); changed || err == nil {
		t.Fatalf("strict commit with a messaged Chat changed=%v err=%v", changed, err)
	}

	// The degraded archived-chat commit succeeds and archives the Chat as-is.
	changed, err := st.CommitSessionControllerEpochArchivedChat(ctx, sess.ID, domain.SessionModeChat, domain.SessionModeTUI, "", now)
	if err != nil || !changed {
		t.Fatalf("archived-chat epoch changed=%v err=%v", changed, err)
	}
	after, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProviderConversationID != "reserved-chat-id" || after.ProviderScopeID == "" {
		t.Fatalf("archived Chat root was disturbed: %+v", after)
	}
	if hasTurns, err := st.HasConversationTurns(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	} else if !hasTurns {
		t.Fatal("archived Chat lost its turn history")
	}
	current, _, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Mode != domain.SessionModeTUI {
		t.Fatalf("mode = %s, want tui", current.Mode)
	}
}

// A degraded identity-reset fresh TUI whose Chat conversation never accepted a
// message still releases the unused Chat provider reservation and event
// namespace, exactly as the strict fresh commit does: only a messaged Chat
// keeps its binding archived (upstream PR #7740 review, P2).
func TestArchivedChatInterfaceEpochReleasesUntouchedChatProvider(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "archived-empty-chat")
	now := time.Now()
	rec := sampleRecord("archived-empty-chat")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ProviderConversationID = "reserved-chat-id"
	rec.Metadata.ControllerGeneration = "chat-generation"
	sess, err := st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateConversation(ctx, "chat-conversation", domain.ConversationScopeSession,
		sess.ProjectID, sess.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := st.CommitSessionControllerEpochArchivedChat(ctx, sess.ID, domain.SessionModeChat, domain.SessionModeTUI, "", now)
	if err != nil || !changed {
		t.Fatalf("archived-chat epoch for an empty Chat changed=%v err=%v", changed, err)
	}
	after, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ProviderConversationID != "" || after.ProviderScopeID == before.ProviderScopeID || after.ProviderScopeID == "" {
		t.Fatalf("untouched Chat kept its provider reservation: before=%+v after=%+v", before, after)
	}
	current, _, err := st.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Mode != domain.SessionModeTUI {
		t.Fatalf("mode = %s, want tui", current.Mode)
	}
	if current.Metadata.ProviderConversationID != "" {
		t.Fatalf("session provider id = %q, want it cleared with the reservation", current.Metadata.ProviderConversationID)
	}
}

// Orchestrator conversations are project-owned: session_id is empty and the
// active orchestrator is recorded only as current_session_id.
func TestFreshInterfaceEpochReleasesUntouchedProjectConversation(t *testing.T) {
	for _, name := range []string{"fresh", "provider turn"} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			seedProject(t, st, "fresh-orchestrator")
			now := time.Now()
			rec := sampleRecord("fresh-orchestrator")
			rec.Kind = domain.KindOrchestrator
			rec.Mode = domain.SessionModeChat
			rec.Metadata.ProviderConversationID = "reserved-chat-id"
			rec.Metadata.ControllerGeneration = "chat-generation"
			sess, err := st.CreateSession(ctx, rec)
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := st.CreateConversation(ctx, "project-conversation", domain.ConversationScopeProject,
				sess.ProjectID, sess.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			before, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "provider turn" {
				if err := st.AdoptProviderTurn(ctx, conversation.ID, sess.ID, "chat-generation", "turn-1", "provider-turn-1", now); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := st.CommitSessionControllerEpoch(ctx, sess.ID, domain.SessionModeChat, domain.SessionModeTUI, "", now)
			after, branchErr := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if branchErr != nil {
				t.Fatal(branchErr)
			}
			current, _, getErr := st.GetSession(ctx, sess.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if name == "fresh" {
				if err != nil || !changed {
					t.Fatalf("fresh orchestrator epoch changed=%v err=%v", changed, err)
				}
				if after.ProviderConversationID != "" || after.ProviderScopeID == before.ProviderScopeID ||
					current.Mode != domain.SessionModeTUI {
					t.Fatalf("unused provider binding was retained: before=%+v after=%+v mode=%s", before, after, current.Mode)
				}
				return
			}
			if changed || err == nil {
				t.Fatalf("orchestrator with history changed=%v err=%v", changed, err)
			}
			if after.ProviderConversationID != before.ProviderConversationID || current.Mode != domain.SessionModeChat {
				t.Fatalf("failed epoch changed ownership: branch=%+v session=%+v", after, current)
			}
		})
	}
}

// A Chat id that the provider reserved but never persisted moves, with its
// untouched root, to the fresh provider conversation started in its place.
func TestReplaceUnpersistedChatProviderMovesSessionAndRoot(t *testing.T) {
	cases := []struct {
		name     string
		scope    domain.ConversationScope
		turn     bool
		expected string
		wantErr  bool
	}{
		{name: "worker", scope: domain.ConversationScopeSession, expected: "reserved-chat-id"},
		{name: "orchestrator", scope: domain.ConversationScopeProject, expected: "reserved-chat-id"},
		{name: "provider turn", scope: domain.ConversationScopeProject, turn: true, expected: "reserved-chat-id", wantErr: true},
		{name: "stale owner", scope: domain.ConversationScopeProject, expected: "older-chat-id", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			seedProject(t, st, "replace-chat")
			now := time.Now()
			rec := sampleRecord("replace-chat")
			if tc.scope == domain.ConversationScopeProject {
				rec.Kind = domain.KindOrchestrator
			}
			rec.Mode = domain.SessionModeChat
			rec.Metadata.ProviderConversationID = "reserved-chat-id"
			rec.Metadata.ControllerGeneration = "chat-generation"
			sess, err := st.CreateSession(ctx, rec)
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := st.CreateConversation(ctx, "replace-conversation", tc.scope, sess.ProjectID, sess.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			before, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.turn {
				if err := st.AdoptProviderTurn(ctx, conversation.ID, sess.ID, "chat-generation", "turn-1", "provider-turn-1", now); err != nil {
					t.Fatal(err)
				}
			}

			err = st.ReplaceUnpersistedChatProvider(ctx, sess.ID, tc.expected, "fresh-chat-id")

			after, branchErr := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if branchErr != nil {
				t.Fatal(branchErr)
			}
			current, _, getErr := st.GetSession(ctx, sess.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("replace succeeded without proof of an untouched owner")
				}
				if current.Metadata.ProviderConversationID != "reserved-chat-id" ||
					after.ProviderConversationID != before.ProviderConversationID {
					t.Fatalf("failed replace changed ownership: session=%q branch=%+v",
						current.Metadata.ProviderConversationID, after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if current.Metadata.ProviderConversationID != "fresh-chat-id" || after.ProviderConversationID != "fresh-chat-id" {
				t.Fatalf("fresh provider not bound: session=%q branch=%+v", current.Metadata.ProviderConversationID, after)
			}
			// The scope is part of a surviving provider host's identity.
			if after.ID != before.ID || after.ProviderScopeID != before.ProviderScopeID {
				t.Fatalf("replace changed the root: before=%+v after=%+v", before, after)
			}
			if current.Metadata.ControllerGeneration != "chat-generation" || current.Mode != domain.SessionModeChat {
				t.Fatalf("replace changed controller owner: %+v", current.ControllerOwner())
			}
		})
	}
}

// A project conversation handed to a replacement orchestrator keeps its root.
// The root's session_id records the orchestrator that created it; the
// conversation's current session is the live owner. An untouched reservation
// left by the previous owner is released at the rebind, so the replacement
// binds its own provider to the same root and both the Chat->Terminal release
// and the fresh-resume swap prove ownership through current_session_id.
func TestProjectRebindLetsReplacementOrchestratorReleaseAndReplace(t *testing.T) {
	for _, name := range []string{"release", "replace", "with history", "same owner"} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			seedProject(t, st, "rebind")
			now := time.Now()
			orchestrator := func(provider string) domain.SessionRecord {
				rec := sampleRecord("rebind")
				rec.Kind = domain.KindOrchestrator
				rec.Mode = domain.SessionModeChat
				rec.Metadata.ProviderConversationID = provider
				rec.Metadata.ControllerGeneration = "chat-generation"
				created, err := st.CreateSession(ctx, rec)
				if err != nil {
					t.Fatal(err)
				}
				return created
			}
			s1 := orchestrator("s1-reserved")
			conversation, err := st.CreateConversation(ctx, "project-conversation", domain.ConversationScopeProject, s1.ProjectID, s1.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			root, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "with history" {
				if err := st.AdoptProviderTurn(ctx, conversation.ID, s1.ID, "chat-generation", "turn-1", "provider-turn-1", now); err != nil {
					t.Fatal(err)
				}
			}
			if name == "same owner" {
				// A restart of the current owner must not drop its own reservation:
				// a surviving provider host may still hold it.
				if _, err := st.CreateConversation(ctx, "ignored", domain.ConversationScopeProject, s1.ProjectID, s1.ID, now); err != nil {
					t.Fatal(err)
				}
				kept, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
				if err != nil {
					t.Fatal(err)
				}
				if kept.ProviderConversationID != "s1-reserved" || kept.ProviderScopeID != root.ProviderScopeID {
					t.Fatalf("same-owner rebind changed the root: before=%+v after=%+v", root, kept)
				}
				return
			}

			s2 := orchestrator("")
			if _, err := st.CreateConversation(ctx, "ignored", domain.ConversationScopeProject, s2.ProjectID, s2.ID, now); err != nil {
				t.Fatal(err)
			}
			rebound, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			if rebound.SessionID != s1.ID {
				t.Fatalf("rebind rewrote the root's creating session: %+v", rebound)
			}
			if name == "with history" {
				if rebound.ProviderConversationID != "s1-reserved" {
					t.Fatalf("rebind released a provider with history: %+v", rebound)
				}
				return
			}
			if rebound.ProviderConversationID != "" {
				t.Fatalf("rebind kept the previous owner's reservation: %+v", rebound)
			}

			// The replacement's first Chat start publishes its provider id; the
			// root binding trigger moves it onto the same, still-empty root.
			s2.Metadata.ProviderConversationID = "s2-reserved"
			if err := st.UpdateSession(ctx, s2); err != nil {
				t.Fatal(err)
			}
			current, err := st.ConversationForSession(ctx, s2.ID)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := st.ConversationBranch(ctx, conversation.ID, current.ActiveBranchID)
			if err != nil {
				t.Fatal(err)
			}
			if bound.ID != root.ID || bound.ProviderConversationID != "s2-reserved" {
				t.Fatalf("replacement did not bind the root: %+v", bound)
			}

			if name == "release" {
				changed, err := st.CommitSessionControllerEpoch(ctx, s2.ID, domain.SessionModeChat, domain.SessionModeTUI, "", now)
				if err != nil || !changed {
					t.Fatalf("replacement Chat->Terminal changed=%v err=%v", changed, err)
				}
				return
			}
			if err := st.ReplaceUnpersistedChatProvider(ctx, s2.ID, "s2-reserved", "s2-fresh"); err != nil {
				t.Fatalf("replacement fresh-resume swap: %v", err)
			}
			moved, err := st.ConversationBranch(ctx, conversation.ID, root.ID)
			if err != nil {
				t.Fatal(err)
			}
			if moved.ProviderConversationID != "s2-fresh" {
				t.Fatalf("swap did not move the root: %+v", moved)
			}
		})
	}
}
