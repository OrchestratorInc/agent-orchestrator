package store_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSetSessionNamedWorkspaceGuardsOwnership(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	for _, name := range []string{"chat", "tui", "failed", "runtime", "provider", "terminated", "preparation", "changed_path"} {
		t.Run(name, func(t *testing.T) {
			rec := sampleRecord("mer")
			rec.Metadata.WorkspacePath = "/old"
			rec.DisplayName = "My chosen title"
			want := false
			switch name {
			case "chat":
				rec.Mode, rec.ProvisionState, want = domain.SessionModeChat, domain.SessionProvisionProvisioning, true
			case "tui":
				rec.Mode, rec.ProvisionState, want = domain.SessionModeTUI, domain.SessionProvisionReady, true
			case "failed":
				rec.ProvisionState, want = domain.SessionProvisionFailed, true
			case "runtime":
				rec.Metadata.RuntimeHandleID = "live-runtime"
			case "provider":
				rec.Metadata.ProviderConversationID = "live-provider"
			case "terminated":
				rec.IsTerminated = true
			case "preparation":
				rec.IsTaskPreparation = true
			case "changed_path":
				rec.Metadata.WorkspacePath = "/someone-else"
			}
			created, err := s.CreateSession(t.Context(), rec)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := s.SetSessionNamedWorkspace(t.Context(), created.ID, "/old", "ao/login/root", "/new", "/repo", created.UpdatedAt)
			if err != nil || updated != want {
				t.Fatalf("updated=%v, err=%v, want %v", updated, err, want)
			}
			actual, _, err := s.GetSession(t.Context(), created.ID)
			if err != nil || actual.DisplayName != rec.DisplayName {
				t.Fatalf("unrelated session fields changed: %+v, %v", actual, err)
			}
			if want {
				if actual.Metadata.WorkspacePath != "/new" || actual.Metadata.Branch != "ao/login/root" {
					t.Fatalf("naming was not saved: %+v", actual)
				}
				if updated, err := s.SetSessionNamedWorkspace(t.Context(), created.ID, "/old", "ao/login/root", "/new", "/repo", created.UpdatedAt); err != nil || !updated {
					t.Fatalf("repeated acknowledgement=%v, %v", updated, err)
				}
			} else if actual.Metadata.WorkspacePath != rec.Metadata.WorkspacePath || actual.Metadata.Branch != rec.Metadata.Branch {
				t.Fatalf("another workspace was overwritten: %+v", actual)
			}
		})
	}
}
