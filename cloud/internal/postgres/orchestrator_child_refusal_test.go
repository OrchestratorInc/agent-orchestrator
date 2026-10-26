package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

// An orchestrator acting on a session it did not spawn is told so, rather
// than getting the organization access error it got before.
func TestOrchestratorChildOperationsNameASessionItDidNotSpawn(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET kind = 'orchestrator' WHERE id = $1`, fixture.sessionID)
	// Read and arranged as the organization, so forced row-level security
	// shows the rows to a test role that does not bypass it.
	org := orgAdminPool(t, admin, fixture.orgID)
	child, err := store.CreateOrchestratorChild(ctx, fixture.orgID, fixture.sessionID, uuid.NewString(), 100, domain.CreateSession{
		Kind: "worker", Harness: "claude-code", DisplayName: "child", Prompt: "go", Provider: "docker",
	})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := store.CreateSession(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID,
		"stranger-"+uuid.NewString(), 100, domain.CreateSession{
			ProjectID: fixture.projectID, Kind: "worker", Harness: "claude-code", DisplayName: "stranger", Prompt: "go", Provider: "docker",
		})
	if err != nil {
		t.Fatal(err)
	}

	err = store.DeleteOrchestratorChild(ctx, fixture.orgID, fixture.sessionID, stranger.ID)
	if !errors.Is(err, ErrNotOrchestratorChild) || !errors.Is(err, ErrForbidden) {
		t.Fatalf("kill a session it did not spawn: err = %v, want ErrNotOrchestratorChild", err)
	}
	if _, err := store.SendOrchestratorChildMessage(ctx, fixture.orgID, fixture.sessionID, stranger.ID, uuid.NewString(), "hi"); !errors.Is(err, ErrNotOrchestratorChild) {
		t.Fatalf("message a session it did not spawn: err = %v, want ErrNotOrchestratorChild", err)
	}
	if err := store.DeleteOrchestratorChild(ctx, fixture.orgID, fixture.sessionID, child.ID); err != nil {
		t.Fatalf("kill its own child: %v", err)
	}
	var desired string
	if err := org.QueryRow(ctx, `SELECT desired_state FROM ao_sandboxes WHERE session_id = $1 LIMIT 1`, child.ID).Scan(&desired); err != nil || desired != string(domain.SandboxDesiredDeleted) {
		t.Fatalf("child sandbox desired_state = %q (%v), want deleted", desired, err)
	}
	// A child with no sandbox left is already gone: killing it again is not
	// an error.
	if tag, err := org.Exec(ctx, `DELETE FROM ao_sandboxes WHERE session_id = $1`, child.ID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("delete the child's sandbox: %v (%d rows)", err, tag.RowsAffected())
	}
	if err := store.DeleteOrchestratorChild(ctx, fixture.orgID, fixture.sessionID, child.ID); err != nil {
		t.Fatalf("kill a child with no sandbox left: %v", err)
	}
}
