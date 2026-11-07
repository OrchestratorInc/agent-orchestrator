package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This fake deliberately returns the recovering owner before a competitor.
// Looking up only the first active orchestrator is not sole-owner proof.
type orderedRecoveryStore struct {
	*historicalChatRestoreStore
	owners []domain.SessionRecord
}

func (s *orderedRecoveryStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return s.owners, nil
}

type recoveryHistoryProbe struct {
	fakeAgent
	exists bool
	err    error
	calls  []string
}

func (a *recoveryHistoryProbe) NativeConversationExists(_ context.Context, _ ports.SessionRef, id string, _ map[string]string) (bool, error) {
	a.calls = append(a.calls, id)
	return a.exists, a.err
}

func TestOrdinaryOrchestratorRecoveryCoordinator(t *testing.T) {
	for _, scenario := range []string{
		"same_harness", "cross_harness", "reserved_predecessor_id",
		"same_session_mismatch", "live_predecessor", "missing_predecessor",
		"missing_native_thread", "probe_error", "no_probe", "no_registry",
		"competing_owner_after_current", "empty_identity_with_history",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			previous := domain.SessionRecord{
				ID: "p-1", ProjectID: "p", Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				IsTerminated: true, CreatedAt: now,
			}
			rec := domain.SessionRecord{
				ID: "p-2", ProjectID: "p", Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				CreatedAt: now.Add(time.Hour),
				Metadata: domain.SessionMetadata{
					ProviderConversationID: "native-current",
					ControllerGeneration:   "current-generation",
				},
			}
			st := &orderedRecoveryStore{
				historicalChatRestoreStore: &historicalChatRestoreStore{
					transitionStore: newTransitionStore(),
					conversation: domain.ConversationRecord{
						ID: "project-conversation", Scope: domain.ConversationScopeProject,
						SessionID: rec.ID, ActiveBranchID: "old-epoch", LatestSequence: 3,
					},
					activeBranch: domain.ConversationBranch{
						ID: "old-epoch", ConversationID: "project-conversation",
						SessionID: previous.ID, ParentBranchID: "root",
						ProviderConversationID: "native-predecessor", ProviderScopeID: "old-epoch",
					},
				},
				owners: []domain.SessionRecord{rec},
			}
			probe := &recoveryHistoryProbe{exists: true}
			var registry ports.AgentResolver = singleAgent{agent: probe}
			wantRecovery := scenario == "same_harness" || scenario == "cross_harness" || scenario == "reserved_predecessor_id"
			switch scenario {
			case "cross_harness":
				previous.Harness, rec.Harness = domain.HarnessClaudeCode, domain.HarnessOpenCode
			case "reserved_predecessor_id":
				st.activeBranch.ProviderConversationID = "reserved-boundary-id"
			case "same_session_mismatch":
				st.activeBranch.SessionID = rec.ID
			case "live_predecessor":
				previous.IsTerminated = false
			case "missing_native_thread":
				probe.exists = false
			case "probe_error":
				probe.err = errors.New("fixture transcript unavailable")
			case "no_probe":
				registry = singleAgent{agent: fakeAgent{}}
			case "no_registry":
				registry = nil
			case "competing_owner_after_current":
				other := rec
				other.ID = "p-3"
				st.owners = append(st.owners, other)
			case "empty_identity_with_history":
				st.activeBranch.ProviderConversationID = ""
			}
			st.sessions[previous.ID] = previous
			st.sessions[rec.ID] = rec
			if scenario == "missing_predecessor" {
				delete(st.sessions, previous.ID)
			}
			st.projects["p"] = domain.ProjectRecord{ID: "p"}
			beforeConversation, beforeBranch := st.conversation, st.activeBranch
			m := New(Deps{Store: st, Agents: registry})
			// Exercise the actual ordinary-resume entry, not a hand-built reservation.
			handoff, err := m.prepareRecoveredChatProviderHandoff(ctx, rec)
			if scenario == "empty_identity_with_history" {
				if err == nil {
					t.Fatal("unidentified recorded history must be refused")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if (handoff != nil) != wantRecovery {
				t.Fatalf("recovery = %+v, want recovery=%v", handoff, wantRecovery)
			}
			if wantRecovery {
				if handoff.BoundaryID == "" || handoff.BoundaryID == beforeBranch.ID ||
					handoff.PreviousSessionID != rec.ID || handoff.PreviousBranchID != beforeBranch.ID ||
					handoff.PreviousSequence != beforeConversation.LatestSequence ||
					handoff.ExpectedControllerOwner != rec.ControllerOwner() ||
					handoff.RecoverySource == nil || handoff.RecoverySource.SessionID != previous.ID ||
					handoff.RecoverySource.ProviderConversationID != beforeBranch.ProviderConversationID {
					t.Fatalf("incomplete recovery proof: %+v", handoff)
				}
				if !reflect.DeepEqual(probe.calls, []string{"native-current"}) {
					t.Fatalf("wrong native proof requests: %v", probe.calls)
				}
			}
			if scenario == "competing_owner_after_current" && len(probe.calls) != 0 {
				t.Fatal("competing orchestrator reached provider I/O")
			}
			if !reflect.DeepEqual(st.conversation, beforeConversation) || !reflect.DeepEqual(st.activeBranch, beforeBranch) {
				t.Fatal("admission mutated historic identity or project ownership")
			}
		})
	}
}
