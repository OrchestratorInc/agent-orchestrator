package controllers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
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
