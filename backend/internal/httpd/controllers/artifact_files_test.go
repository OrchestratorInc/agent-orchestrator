package controllers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
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
		"Content-Security-Policy": "sandbox allow-scripts allow-forms",
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
	// As for renders, the ETag names the bootstrap and the page.
	etag := resp.Header.Get("ETag")
	if want := `"` + renderpage.Version + "-"; !strings.HasPrefix(etag, want) || len(etag) != len(want)+16+1 {
		t.Fatalf("ETag = %q, want %s<16 hex>\"", etag, want)
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
	if len(etag) != 16+2 || strings.Contains(etag, renderpage.Version) {
		t.Fatalf("ETag = %q, want the content hash alone", etag)
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
