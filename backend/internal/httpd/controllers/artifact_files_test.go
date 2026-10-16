package controllers_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/renderpage"
)

// artifactFileServer serves session ao-1 with the given artifact files.
func artifactFileServer(t *testing.T, files map[string]string) (*httptest.Server, string) {
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
	svc.sessions["ao-1"] = s
	return newSessionTestServer(t, svc), dir
}

func getArtifactFile(t *testing.T, srv *httptest.Server, rawPath, ifNoneMatch string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/artifact-files/"+rawPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(body)
}

func wantSandboxHeaders(t *testing.T, resp *http.Response, contentType string) {
	t.Helper()
	for header, want := range map[string]string{
		"Content-Security-Policy": "sandbox allow-scripts allow-forms; worker-src 'none'",
		"Content-Type":            contentType,
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "private, no-cache",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestArtifactFileRouteServesAPageSandboxedWithTheBootstrap(t *testing.T) {
	srv, _ := artifactFileServer(t, map[string]string{"report/index.html": "<p>report</p>"})

	resp, body := getArtifactFile(t, srv, "report/index.html", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `<style id="ao-theme">`) || !strings.HasSuffix(body, "<p>report</p>") {
		t.Fatalf("status=%d body=%.200q", resp.StatusCode, body)
	}
	wantSandboxHeaders(t, resp, "text/html; charset=utf-8")
	// The ETag names the bootstrap and the file's size and modification time,
	// all known before the file is read.
	etag := resp.Header.Get("ETag")
	if want := `"` + renderpage.Version + "-" + strconv.Itoa(len("<p>report</p>")) + "-"; !strings.HasPrefix(etag, want) {
		t.Fatalf("ETag = %q, want %s<mtime>\"", etag, want)
	}
	if again, _ := getArtifactFile(t, srv, "report/index.html", etag); again.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %s = %d, want 304", etag, again.StatusCode)
	}

	source, body := getArtifactFile(t, srv, "report/index.html?source=1", "")
	if source.StatusCode != http.StatusOK || body != "<p>report</p>" {
		t.Fatalf("source: status=%d body=%q, want the raw page", source.StatusCode, body)
	}
	wantSandboxHeaders(t, source, "text/plain; charset=utf-8")
}

func TestArtifactFileRouteServesTheFilesNextToAPage(t *testing.T) {
	srv, _ := artifactFileServer(t, map[string]string{
		"report/index.html":      `<img src="chart (1).png">`,
		"report/chart (1).png":   "\x89PNG\r\n",
		"report/notes.unknownxt": "x",
	})

	// A browser resolves the page's relative link with the space escaped and
	// the parentheses as they are.
	resp, body := getArtifactFile(t, srv, "report/chart%20(1).png", "")
	if resp.StatusCode != http.StatusOK || body != "\x89PNG\r\n" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	wantSandboxHeaders(t, resp, "image/png")
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `"6-`) || strings.Contains(etag, renderpage.Version) {
		t.Fatalf("ETag = %q, want the file's size and modification time alone", etag)
	}
	if again, _ := getArtifactFile(t, srv, "report/chart%20%281%29.png", etag); again.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %s = %d, want 304", etag, again.StatusCode)
	}

	unknown, _ := getArtifactFile(t, srv, "report/notes.unknownxt", "")
	wantSandboxHeaders(t, unknown, "application/octet-stream")
}

func TestArtifactFileRouteServesOnlyRegularFilesInsideTheDirectory(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.html")
	if err := os.WriteFile(outside, []byte("<p>secret</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, dir := artifactFileServer(t, map[string]string{"report/index.html": "<p>report</p>"})
	if err := os.Symlink(outside, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	// One byte past the cap; sparse, so cheap.
	if err := os.WriteFile(filepath.Join(dir, "huge.html"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(dir, "huge.html"), attachmentstore.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}

	for name, rawPath := range map[string]string{
		"dot-dot":          "report/../../secret.html",
		"escaped dot-dot":  "..%2Fsecret.html",
		"escaping symlink": "escape.html",
		"directory":        "report",
		"over the cap":     "huge.html",
		"missing":          "report/gone.html",
	} {
		resp, body := getArtifactFile(t, srv, rawPath, "")
		var apiErr struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal([]byte(body), &apiErr)
		if resp.StatusCode != http.StatusNotFound || apiErr.Code != "ARTIFACT_FILE_NOT_FOUND" {
			t.Errorf("%s: status=%d body=%.200q, want 404 ARTIFACT_FILE_NOT_FOUND", name, resp.StatusCode, body)
		}
	}
}

// An HTML artifact framed in the chat runs on its own origin, so its module
// scripts, fetches and fonts load same-origin, yet that origin is refused
// everywhere else: the page cannot call the daemon API.
func TestInlineArtifactOriginServesItsFilesButNotTheAPI(t *testing.T) {
	srv, _ := artifactFileServer(t, map[string]string{
		"q3/report.html": `<script type="module" src="app.js"></script>`,
		"q3/app.js":      "export const ok = true;",
		"q3/data.json":   `{"x":1}`,
	})
	inline, err := previewutil.InlineArtifactFileURL(srv.URL, "ao-1", "q3/report.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inline, "://ao-inline-artifact.") || !strings.HasSuffix(inline, "/q3/report.html") {
		t.Fatalf("inline URL = %q", inline)
	}

	body, status, header := doPreviewOriginRequest(t, srv, inline, "/q3/report.html")
	if status != http.StatusOK || !strings.Contains(string(body), `<style id="ao-theme">`) {
		t.Fatalf("page: status=%d body=%.200q", status, body)
	}
	if got := header.Get("Content-Security-Policy"); got != "sandbox allow-scripts allow-forms allow-same-origin; worker-src 'none'" {
		t.Fatalf("page CSP = %q, want the inline sandbox with allow-same-origin", got)
	}
	// Same-origin requests carry the page's own Origin and are served.
	for path, want := range map[string]string{"/q3/app.js": "export const ok = true;", "/q3/data.json": `{"x":1}`} {
		if body, status, _ := doPreviewOriginRequest(t, srv, inline, path); status != http.StatusOK || string(body) != want {
			t.Fatalf("%s: status=%d body=%q", path, status, body)
		}
	}
	if _, status, _ := doPreviewOriginRequest(t, srv, inline, "/q3/../../etc/passwd"); status != http.StatusNotFound {
		t.Fatalf("escape: status=%d, want 404", status)
	}
	if _, status, _ := doPreviewOriginMethod(t, srv, http.MethodPost, inline, "/q3/report.html"); status != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status=%d, want 405", status)
	}

	// The same origin calling the daemon API, on the daemon's own host, is refused.
	u, _ := url.Parse(inline)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions", nil)
	req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("API from the inline origin: status=%d ACAO=%q, want 403 and no ACAO", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

// deleteArtifactFileServer serves session ao-1 with the given artifact files
// on disk, of which listed are the ones its session read reports.
func deleteArtifactFileServer(t *testing.T, files map[string]string, listed ...string) (*httptest.Server, string) {
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
	for _, name := range listed {
		s.ArtifactFiles = append(s.ArtifactFiles, domain.SessionArtifactFile{Path: name, Name: path.Base(name)})
	}
	svc.sessions["ao-1"] = s
	return newSessionTestServer(t, svc), dir
}

func deleteArtifactFile(t *testing.T, srv *httptest.Server, relPath string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/sessions/ao-1/artifact-files?path="+url.QueryEscape(relPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestDeleteArtifactFileRemovesAListedFile(t *testing.T) {
	srv, dir := deleteArtifactFileServer(t, map[string]string{"plans/notes.md": "# notes", "keep.md": "keep"}, "plans/notes.md", "keep.md")

	if status := deleteArtifactFile(t, srv, "plans/notes.md"); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "plans", "notes.md")); !os.IsNotExist(err) {
		t.Fatalf("notes.md still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.md")); err != nil {
		t.Fatalf("keep.md was removed: %v", err)
	}
}

func TestDeleteArtifactFileRefusesAnythingButAListedRegularFile(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, dir := deleteArtifactFileServer(t, map[string]string{"unlisted.md": "x", "plans/a.md": "a"}, "gone.md", "plans", "link.md", "../outside.md")
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}

	for _, relPath := range []string{"unlisted.md", "gone.md", "plans", "link.md", "../outside.md", ""} {
		if status := deleteArtifactFile(t, srv, relPath); status != http.StatusNotFound {
			t.Errorf("DELETE %q status = %d, want 404", relPath, status)
		}
	}
	for _, file := range []string{filepath.Join(dir, "unlisted.md"), filepath.Join(dir, "plans", "a.md"), outside} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("%s was removed: %v", file, err)
		}
	}
}

func artifactSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func putArtifactFile(t *testing.T, srv *httptest.Server, body map[string]string) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/sessions/ao-1/artifact-files", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUpdateArtifactFileReplacesAListedFile(t *testing.T) {
	srv, dir := deleteArtifactFileServer(t, map[string]string{"plans/notes.md": "# notes"}, "plans/notes.md")

	status, out := putArtifactFile(t, srv, map[string]string{"path": "plans/notes.md", "content": "# edited", "expectedFileFingerprint": artifactSHA("# notes")})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, out)
	}
	if out["fileFingerprint"] != artifactSHA("# edited") || out["path"] != "plans/notes.md" || out["size"] != float64(len("# edited")) {
		t.Fatalf("response = %v", out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "plans", "notes.md"))
	if err != nil || string(got) != "# edited" {
		t.Fatalf("file = %q, %v", got, err)
	}
}

func TestUpdateArtifactFileRefusesAStaleEdit(t *testing.T) {
	srv, dir := deleteArtifactFileServer(t, map[string]string{"notes.md": "agent rewrote this"}, "notes.md")

	status, _ := putArtifactFile(t, srv, map[string]string{"path": "notes.md", "content": "mine", "expectedFileFingerprint": artifactSHA("what I loaded")})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "notes.md")); string(got) != "agent rewrote this" {
		t.Fatalf("file = %q, want it untouched", got)
	}
}

func TestUpdateArtifactFileRefusesAnythingButAListedRegularFileWithText(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, dir := deleteArtifactFileServer(t, map[string]string{"unlisted.md": "x", "plans/a.md": "a", "ok.md": "ok"}, "gone.md", "plans", "link.md", "../outside.md", "ok.md")
	if err := os.Symlink(outside, filepath.Join(dir, "link.md")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}

	for _, relPath := range []string{"unlisted.md", "gone.md", "plans", "link.md", "../outside.md", ""} {
		if status, _ := putArtifactFile(t, srv, map[string]string{"path": relPath, "content": "y", "expectedFileFingerprint": artifactSHA("outside")}); status != http.StatusNotFound {
			t.Errorf("PUT %q status = %d, want 404", relPath, status)
		}
	}
	for _, body := range []map[string]string{
		{"path": "ok.md", "content": "y"},
		{"path": "ok.md", "content": "nul\x00byte", "expectedFileFingerprint": artifactSHA("ok")},
	} {
		if status, _ := putArtifactFile(t, srv, body); status != http.StatusBadRequest {
			t.Errorf("PUT %v status = %d, want 400", body, status)
		}
	}
	if got, _ := os.ReadFile(outside); string(got) != "outside" {
		t.Errorf("outside file = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ok.md")); string(got) != "ok" {
		t.Errorf("ok.md = %q", got)
	}
}

func TestUpdateArtifactFileKeepsPermissionsAndLeavesNoTemporaryFile(t *testing.T) {
	srv, dir := deleteArtifactFileServer(t, map[string]string{"plans/notes.md": "# notes"}, "plans/notes.md")
	file := filepath.Join(dir, "plans", "notes.md")
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}

	if status, out := putArtifactFile(t, srv, map[string]string{"path": "plans/notes.md", "content": "# edited", "expectedFileFingerprint": artifactSHA("# notes")}); status != http.StatusOK {
		t.Fatalf("status = %d (%v)", status, out)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Join(dir, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "notes.md" {
		t.Errorf("plans/ holds %v, want only notes.md", entries)
	}
}

func TestUpdateArtifactFileLeavesTheOriginalIntactWhenTheWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory does not block file creation on Windows")
	}
	srv, dir := deleteArtifactFileServer(t, map[string]string{"plans/notes.md": "# notes"}, "plans/notes.md")
	plans := filepath.Join(dir, "plans")
	if err := os.Chmod(plans, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(plans, 0o755) })
	if probe, err := os.Create(filepath.Join(plans, "probe")); err == nil {
		_ = probe.Close()
		t.Skip("running with privileges that ignore directory permissions")
	}

	if status, _ := putArtifactFile(t, srv, map[string]string{"path": "plans/notes.md", "content": "# edited", "expectedFileFingerprint": artifactSHA("# notes")}); status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if got, _ := os.ReadFile(filepath.Join(plans, "notes.md")); string(got) != "# notes" {
		t.Fatalf("notes.md = %q, want the original", got)
	}
}
