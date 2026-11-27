package controllers_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// importServer serves session ao-1 with its artifact folder holding files,
// and the version service an import keeps replaced content in.
func importServer(t *testing.T, files map[string]string, mutate ...func(*domain.Session)) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Metadata.ArtifactDir = dir
	for _, m := range mutate {
		m(&s)
	}
	svc.sessions["ao-1"] = s
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	versions := &fakeArtifactVersions{dir: dir, blobs: map[string]string{}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc, ArtifactVersions: versions}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, dir
}

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
		"file is also a folder":  importBody(t, "shared/r/a.html", map[string]string{"shared/r/a.html": "x", "shared/r/img": "x", "shared/r/img/b.png": "x"}),
		"file outside the copy":  importBody(t, "shared/r/pages/index.html", map[string]string{"shared/r/pages/index.html": "x", "report.html": "x"}),
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
	srv, dir := importServer(t, map[string]string{"shared/r/page.html": "my edit"})
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
	srv, _ := importServer(t, nil)
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
	srv, dir := importServer(t, nil, func(s *domain.Session) { s.Status = domain.StatusWorking })
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "shared/r/page.html", map[string]string{"shared/r/page.html": "owner v2"}))
	assertErrorCode(t, body, status, http.StatusConflict, "SESSION_BUSY")
	if _, err := os.Stat(filepath.Join(dir, "shared")); !os.IsNotExist(err) {
		t.Fatalf("written while the session was working: %v", err)
	}
}

func TestImportArtifactsUsesTheFilesOwnSpelling(t *testing.T) {
	srv, _ := importServer(t, nil)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "shared/r/PAGE.html", map[string]string{"shared/r/page.html": "x"}))
	if status != http.StatusOK || !strings.Contains(string(body), `"entry":"shared/r/page.html"`) {
		t.Fatalf("import = %d, want 200 with the file's spelling; body=%s", status, body)
	}
}

func TestImportArtifactsFailsWhenTheCopyCannotBeMarked(t *testing.T) {
	srv, dir := importServer(t, nil)
	// A folder where the marker goes makes writing it fail.
	if err := os.MkdirAll(filepath.Join(dir, "shared", "r", sessionartifacts.SharedCopyMarker), 0o755); err != nil {
		t.Fatal(err)
	}
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "shared/r/page.html", map[string]string{"shared/r/page.html": "x"}))
	if status < 500 {
		t.Fatalf("import = %d, want a failure; body=%s", status, body)
	}
}

func TestImportArtifactsNeedsTheVersionService(t *testing.T) {
	srv, _ := deleteArtifactFileServer(t, nil)
	_, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/artifact-imports",
		importBody(t, "shared/r/page.html", map[string]string{"shared/r/page.html": "x"}))
	if status != http.StatusNotImplemented {
		t.Fatalf("import without versions = %d, want 501", status)
	}
}
