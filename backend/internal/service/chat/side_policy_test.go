package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestBoundaryAcceptanceAndRecoveryDoNotReplayFirstRequest(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := newMemorySideStore()
	_, _ = store.ClaimSideLaunch(ctx, "launch", now)
	side := domain.SideConversation{PolicyVersion: sidePolicyVersion, ID: "side", SessionID: "main", MainConversationID: "conversation", AppRunID: "launch", ProviderHostID: "btw-launch-side", ProviderForkID: "fork", Generation: "g", State: "ready"}
	_, _, _ = store.CreateSideConversation(ctx, side)
	turn := domain.SideTurn{ID: "first", SideID: side.ID, ClientMessageID: "request", State: "running", Text: "explicit request"}
	_, _, _ = store.ReserveSideTurn(ctx, turn, "launch")
	store.turns[side.ID][0].State = "running"
	first, err := store.PrepareSideBoundary(ctx, side.ID, turn.ID, side.Generation)
	if err != nil || !first {
		t.Fatalf("first boundary: %v %v", first, err)
	}
	// A crash before an acceptance receipt cannot safely replay this request.
	unknown := newMemorySideStore()
	_, _ = unknown.ClaimSideLaunch(ctx, "launch", now)
	recovered, err := unknown.recover("launch", store.export("launch"), now)
	if err != nil || len(recovered) != 1 || recovered[0].State != "failed" || recovered[0].ErrorMessage != ErrSidePolicyRecreate.Error() {
		t.Fatalf("uncertain recovery: %+v %v", recovered, err)
	}
	if _, err := unknown.PrepareSideBoundary(ctx, side.ID, turn.ID, side.Generation); !errors.Is(err, ErrSidePolicyRecreate) {
		t.Fatal(err)
	}
	if err := store.SetSideTurnProviderID(ctx, side.ID, turn.ID, side.Generation, "accepted-provider-turn"); err != nil {
		t.Fatal(err)
	}
	accepted := newMemorySideStore()
	_, _ = accepted.ClaimSideLaunch(ctx, "launch", now)
	recovered, err = accepted.recover("launch", store.export("launch"), now)
	if err != nil || recovered[0].State != "recovering" || recovered[0].BoundaryState != "delivered" {
		t.Fatalf("accepted recovery: %+v %v", recovered, err)
	}
	if first, err := accepted.PrepareSideBoundary(ctx, side.ID, "next", side.Generation); err != nil || first {
		t.Fatalf("boundary replay: %v %v", first, err)
	}
}

func TestLegacyRecoveryPreservesTranscriptAndRequiresRecreation(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := newMemorySideStore()
	_, _ = store.ClaimSideLaunch(ctx, "launch", now)
	record := SideRecoveryRecord{Side: domain.SideConversation{ID: "legacy", SessionID: "main", MainConversationID: "conversation", State: "ready"}, ProviderHostID: "btw-launch-legacy", ProviderForkID: "fork", Generation: "g", Draft: "unfinished", Messages: []domain.SideMessage{{ID: "message", SideID: "legacy", Text: "keep this transcript"}}}
	recovered, err := store.recover("launch", []SideRecoveryRecord{record}, now)
	if err != nil || recovered[0].State != "failed" || recovered[0].ErrorMessage != ErrSidePolicyRecreate.Error() {
		t.Fatalf("legacy resumed: %+v %v", recovered, err)
	}
	messages, _ := store.SideMessages(ctx, "legacy", nil)
	draft, _ := store.SideDraft(ctx, "legacy")
	if len(messages) != 1 || messages[0].Text != "keep this transcript" || draft != "unfinished" {
		t.Fatal("legacy data discarded")
	}
	encoded, _ := json.Marshal(recovered[0])
	if strings.Contains(string(encoded), "policyVersion") || strings.Contains(string(encoded), "boundaryState") {
		t.Fatal("private policy metadata leaked into summary")
	}
}

func TestSideBoundaryIncludesAuthorizationAndDelegationRules(t *testing.T) {
	for _, required := range []string{"Everything before this boundary is inherited history", "Only messages submitted after this boundary are active user instructions", "If there is no user question after this boundary yet, wait for one.", "Do not interact with any existing or new sub-agents", "unless the user explicitly asks for that mutation after this boundary", "Do not request escalated permissions or broader sandbox access"} {
		if !strings.Contains(sideConversationBoundary, required) {
			t.Fatal(required)
		}
	}
}
