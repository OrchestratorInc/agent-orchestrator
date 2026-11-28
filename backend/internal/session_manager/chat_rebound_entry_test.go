package sessionmanager

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type reboundEntryProbe struct{ fakeAgent }

func (reboundEntryProbe) NativeConversationExists(context.Context, ports.SessionRef, string, map[string]string) (bool, error) {
	return true, nil
}

func TestReboundOrchestratorResumeEntries(t *testing.T) {
	for _, live := range []bool{false, true} {
		name := "cold_resume"
		if live {
			name = "live_handoff"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			previous := domain.SessionRecord{
				ID: "p-1", ProjectID: "p", Kind: domain.KindOrchestrator,
				IsTerminated: true, CreatedAt: now.Add(-time.Hour),
			}
			rec := domain.SessionRecord{
				ID: "p-2", ProjectID: "p", Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: now,
				Metadata: domain.SessionMetadata{ProviderConversationID: "native-current"},
			}
			st := &historicalChatRestoreStore{
				transitionStore: newTransitionStore(),
				conversation: domain.ConversationRecord{
					ID: "project-history", Scope: domain.ConversationScopeProject,
					ProjectID: "p", SessionID: rec.ID, ActiveBranchID: "predecessor", LatestSequence: 3,
				},
				activeBranch: domain.ConversationBranch{
					ID: "predecessor", ConversationID: "project-history", SessionID: previous.ID,
					ProviderConversationID: "native-previous", ParentBranchID: "root",
				},
			}
			st.sessions[previous.ID], st.sessions[rec.ID] = previous, rec
			st.projects["p"] = domain.ProjectRecord{ID: "p"}
			m := New(Deps{Store: st, Agents: singleAgent{agent: reboundEntryProbe{}}})
			prepare := m.prepareRecoveredChatProviderHandoff
			if live {
				prepare = m.prepareLiveChatProviderHandoff
			}
			beforeConversation, beforeBranch := st.conversation, st.activeBranch
			handoff, err := prepare(context.Background(), rec)
			if err != nil {
				t.Fatal(err)
			}
			if handoff == nil || handoff.BoundaryID == "" || handoff.BoundaryID == beforeBranch.ID ||
				handoff.PreviousBranchID != beforeBranch.ID || handoff.PreviousSessionID != rec.ID ||
				handoff.PreviousSequence != beforeConversation.LatestSequence ||
				handoff.ExpectedControllerOwner != rec.ControllerOwner() {
				t.Fatalf("resume did not reserve the rebound conversation boundary: %+v", handoff)
			}
			if !reflect.DeepEqual(st.conversation, beforeConversation) || !reflect.DeepEqual(st.activeBranch, beforeBranch) {
				t.Fatal("preparing recovery changed the predecessor's history")
			}
		})
	}
}
