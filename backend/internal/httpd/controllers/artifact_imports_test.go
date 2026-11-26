package controllers_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

func importBody(t *testing.T, entry string, files map[string]string) string {
	t.Helper()
	type file struct {
		Path string `json:"path"`
		Data string `json:"data"`
	}
	body := struct {
		Entry string `json:"entry"`
		Files []file `json:"files"`
	}{Entry: entry}
	for name, content := range files {
		body.Files = append(body.Files, file{Path: name, Data: base64.StdEncoding.EncodeToString([]byte(content))})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSessionsAPI_SpawnCopiesASharedArtifact(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	shared := importBody(t, "shared/roadmap/page.html", map[string]string{
		"shared/roadmap/page.html":    "<img src=images/a.png>",
		"shared/roadmap/images/a.png": "png",
	})
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"kind":"worker","harness":"codex","prompt":"work from it","sharedArtifact":`+shared+`}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn = %d, want 201; body=%s", status, body)
	}
	if len(svc.lastSpawn.ArtifactImports) != 2 {
		t.Fatalf("imports = %+v, want two files", svc.lastSpawn.ArtifactImports)
	}
	if len(svc.artifactsRegistered) != 1 || svc.artifactsRegistered[0] != "shared/roadmap/page.html" {
		t.Fatalf("registered = %v, want the entry", svc.artifactsRegistered)
	}
}

func TestSessionsAPI_SpawnRefusesAnUnsafeSharedArtifact(t *testing.T) {
	for name, shared := range map[string]string{
		"path leaves the folder": importBody(t, "page.html", map[string]string{"page.html": "x", "../evil.sh": "x"}),
		"entry not a file":       importBody(t, "other.html", map[string]string{"page.html": "x"}),
		"no files":               `{"entry":"page.html","files":[]}`,
		"repeated path":          `{"entry":"a.html","files":[{"path":"a.html","data":"eA=="},{"path":"A.html","data":"eA=="}]}`,
		"not base64":             `{"entry":"a.html","files":[{"path":"a.html","data":"%%%"}]}`,
		"file is also a folder":  importBody(t, "a.html", map[string]string{"a.html": "x", "img": "x", "img/b.png": "x"}),
		"trailing dot":           importBody(t, "a.html", map[string]string{"a.html": "x", "b.": "x"}),
	} {
		t.Run(name, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
				`{"kind":"worker","harness":"codex","prompt":"p","sharedArtifact":`+shared+`}`)
			if status != http.StatusBadRequest {
				t.Fatalf("spawn = %d, want 400; body=%s", status, body)
			}
			if svc.lastSpawn.Prompt != "" {
				t.Fatal("the session was spawned anyway")
			}
		})
	}
}

func TestImportArtifactsWritesAndRegisters(t *testing.T) {
	srv, dir := deleteArtifactFileServer(t, map[string]string{"shared/r/page.html": "my edit"}, "shared/r/page.html")
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "shared/r/page.html", map[string]string{"shared/r/page.html": "owner v2", "shared/r/style.css": "body{}"}))
	if status != http.StatusOK {
		t.Fatalf("import = %d, want 200; body=%s", status, body)
	}
	if !strings.Contains(string(body), `"entry":"shared/r/page.html"`) || !strings.Contains(string(body), `"files":2`) {
		t.Fatalf("body = %s", body)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "shared", "r", "page.html")); string(got) != "owner v2" {
		t.Fatalf("page = %q, want the owner's version", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "shared", "r", "style.css")); string(got) != "body{}" {
		t.Fatalf("style = %q, want written", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared", "r", sessionartifacts.SharedCopyMarker)); err != nil {
		t.Fatalf("copy not marked: %v", err)
	}
}

func TestImportArtifactsRefusesBadInputAndUnknownSessions(t *testing.T) {
	srv, _ := deleteArtifactFileServer(t, nil)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "a.html", map[string]string{"/etc/a.html": "x"}))
	assertErrorCode(t, body, status, http.StatusBadRequest, "ARTIFACT_IMPORT_INVALID")

	// A copy is always in its own folder under shared/, the folder marked as a copy.
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "index.html", map[string]string{"index.html": "x"}))
	assertErrorCode(t, body, status, http.StatusBadRequest, "ARTIFACT_IMPORT_INVALID")

	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/nope/artifact-imports",
		importBody(t, "shared/a/a.html", map[string]string{"shared/a/a.html": "x"}))
	if status != http.StatusNotFound {
		t.Fatalf("unknown session = %d, want 404; body=%s", status, body)
	}
}

func TestImportArtifactsRefusesAWorkingSession(t *testing.T) {
	dir := t.TempDir()
	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Metadata.ArtifactDir = dir
	s.Status = domain.StatusWorking
	svc.sessions["ao-1"] = s
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "shared/r/page.html", map[string]string{"shared/r/page.html": "owner v2"}))
	assertErrorCode(t, body, status, http.StatusConflict, "SESSION_BUSY")
	if _, err := os.Stat(filepath.Join(dir, "shared")); !os.IsNotExist(err) {
		t.Fatalf("written while the session was working: %v", err)
	}
}
