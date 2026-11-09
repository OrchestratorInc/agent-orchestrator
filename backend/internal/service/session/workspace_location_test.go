package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestWorkspaceLocationReturnsOnlyLiveSessionWorkspace(t *testing.T) {
	workspace := t.TempDir()
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID:       "ao-1",
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != workspace {
		t.Fatalf("WorkspaceLocation() = %q, want %q", got, workspace)
	}
}

func TestWorkspaceLocationDoesNotFallBackToProjectCheckout(t *testing.T) {
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "ao"}
	store.projects["ao"] = domain.ProjectRecord{ID: "ao", Path: t.TempDir()}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "ao-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestWorkspaceLocationRejectsMissingDirectory(t *testing.T) {
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID:       "ao-1",
		Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir() + "/gone"},
	}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "ao-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestWorkspaceLocationRejectsFileInPlaceOfDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "workspace")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID:       "ao-1",
		Metadata: domain.SessionMetadata{WorkspacePath: file},
	}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "ao-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestWorkspaceLocationReportsUnreadableWorkspace(t *testing.T) {
	workspace := unreadableWorkspace(t)
	if _, err := os.ReadDir(workspace); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("fixture does not deny real directory reads: %v", err)
	} else {
		t.Logf("scratch directory read denied: %v", err)
	}
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID:       "ao-1",
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "ao-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_UNREADABLE")
}

func TestWorkspaceLocationPreservesStoreFailure(t *testing.T) {
	store := newFakeStore()
	store.getSessionErr = errors.New("storage unavailable")

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "ao-1")
	if err == nil || !errors.Is(err, store.getSessionErr) {
		t.Fatalf("WorkspaceLocation() error = %v, want wrapped storage failure", err)
	}
}

func assertAPIErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != code {
		t.Fatalf("error = %v, want API code %s", err, code)
	}
}

func TestArtifactLocationReturnsTheStoredArtifactDirectory(t *testing.T) {
	artifactDir := t.TempDir()
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{ArtifactDir: artifactDir}}

	got, err := (&Service{store: store, dataDir: t.TempDir()}).ArtifactLocation(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != artifactDir {
		t.Fatalf("ArtifactLocation() = %q, want %q", got, artifactDir)
	}
}

func TestArtifactLocationFallsBackToTheDataDirLayout(t *testing.T) {
	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "ao-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1"}

	got, err := (&Service{store: store, dataDir: dataDir}).ArtifactLocation(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != artifactDir {
		t.Fatalf("ArtifactLocation() = %q, want %q", got, artifactDir)
	}
}

func TestArtifactLocationRejectsAMissingDirectory(t *testing.T) {
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{ArtifactDir: filepath.Join(t.TempDir(), "gone")}}

	_, err := (&Service{store: store}).ArtifactLocation(context.Background(), "ao-1")
	assertAPIErrorCode(t, err, "SESSION_ARTIFACTS_NOT_FOUND")
}
