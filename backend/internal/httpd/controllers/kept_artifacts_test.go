package controllers_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

func (f *fakeSessionService) DeleteKeepingArtifacts(_ context.Context, id domain.SessionID, _ bool) error {
	f.keepingDeleted = append(f.keepingDeleted, id)
	return nil
}

func (f *fakeSessionService) ArtifactSession(ctx context.Context, id domain.SessionID) (domain.Session, bool, error) {
	if sess, ok := f.keptSessions[id]; ok {
		return sess, true, nil
	}
	sess, err := f.Get(ctx, id)
	return sess, false, err
}

func (f *fakeSessionService) ListKeptArtifacts(context.Context) ([]sessionsvc.KeptArtifacts, error) {
	return f.keptSets, nil
}

func (f *fakeSessionService) ForgetKeptArtifact(_ context.Context, id domain.SessionID, path string) error {
	f.forgottenKept = append(f.forgottenKept, string(id)+"/"+path)
	delete(f.keptSessions, id)
	return nil
}

// keepArtifacts=true deletes through the keeping delete, and nothing else does.
func TestSessionsAPI_DeleteSessionKeepingArtifacts(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	if body, status, _ := doRequest(t, srv, http.MethodDelete, "/api/v1/sessions/ao-1?keepArtifacts=true", ""); status != http.StatusOK {
		t.Fatalf("delete = %d %s", status, body)
	}
	if !slices.Equal(svc.keepingDeleted, []domain.SessionID{"ao-1"}) || len(svc.deleted) != 0 {
		t.Fatalf("keeping deleted = %v, deleted = %v", svc.keepingDeleted, svc.deleted)
	}
}

// A deleted session's kept files are listed, served from its directory, and
// deleted one at a time through the session's artifact routes.
func TestSessionsAPI_KeptArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := domain.SessionArtifactFile{ID: "art-1", Path: "report.md", Name: "report.md", Kind: domain.SessionArtifactMarkdown, Size: 6, Registered: true}
	stand := domain.Session{ArtifactFiles: []domain.SessionArtifactFile{file}}
	stand.ID = "ao-gone"
	stand.IsTerminated = true
	stand.Metadata.ArtifactDir = dir
	svc := newFakeSessionService()
	svc.keptSessions = map[domain.SessionID]domain.Session{"ao-gone": stand}
	svc.keptSets = []sessionsvc.KeptArtifacts{{
		SessionID: "ao-gone", ProjectID: "mer", Title: "Report", SessionDeleted: true,
		At: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), Files: []domain.SessionArtifactFile{file},
	}}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/kept-artifacts", "")
	if status != http.StatusOK {
		t.Fatalf("list = %d %s", status, body)
	}
	var list struct {
		Sets []struct {
			SessionID      string `json:"sessionId"`
			ProjectID      string `json:"projectId"`
			Title          string `json:"title"`
			SessionDeleted bool   `json:"sessionDeleted"`
			ArtifactFiles  []struct {
				Path       string `json:"path"`
				Registered bool   `json:"registered"`
				RawURL     string `json:"rawUrl"`
			} `json:"artifactFiles"`
		} `json:"sets"`
	}
	mustJSON(t, body, &list)
	if len(list.Sets) != 1 || list.Sets[0].SessionID != "ao-gone" || list.Sets[0].ProjectID != "mer" || !list.Sets[0].SessionDeleted ||
		len(list.Sets[0].ArtifactFiles) != 1 || !list.Sets[0].ArtifactFiles[0].Registered || list.Sets[0].ArtifactFiles[0].RawURL == "" {
		t.Fatalf("list = %+v", list)
	}

	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-gone/artifact-files/report.md", "")
	if status != http.StatusOK || !strings.Contains(string(body), "# Kept") {
		t.Fatalf("serve kept file = %d %s", status, body)
	}

	// The desktop reads markdown and text previews through this URL.
	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-gone/preview/files/report.md?source=artifact&raw=true", "")
	if status != http.StatusOK || !strings.Contains(string(body), "# Kept") {
		t.Fatalf("preview kept file = %d %s", status, body)
	}
	// A workspace preview still needs the session itself.
	if body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-gone/preview/files/report.md", ""); status != http.StatusNotFound {
		t.Fatalf("workspace preview of a deleted session = %d %s", status, body)
	}

	body, status, _ = doRequest(t, srv, http.MethodDelete, "/api/v1/sessions/ao-gone/artifact-files?path=report.md", "")
	if status != http.StatusOK {
		t.Fatalf("delete kept file = %d %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); !os.IsNotExist(err) {
		t.Fatalf("kept file not deleted: %v", err)
	}
	if !slices.Equal(svc.forgottenKept, []string{"ao-gone/report.md"}) {
		t.Fatalf("forgotten = %v", svc.forgottenKept)
	}
}
