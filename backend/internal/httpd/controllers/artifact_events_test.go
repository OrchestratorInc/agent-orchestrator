package controllers_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openArtifactEvents opens session ao-1's artifact change stream and returns
// its event names as they arrive.
func openArtifactEvents(t *testing.T, url string) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/v1/sessions/ao-1/artifact-events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET artifact events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET artifact events = %d body=%s", resp.StatusCode, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	events := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if name, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
				events <- name
			}
		}
	}()
	return events
}

func awaitArtifactEvent(t *testing.T, events <-chan string, want string) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case got := <-events:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func TestArtifactEventsReportAnAgentRewritingAnOpenFile(t *testing.T) {
	srv, dir := artifactFileServer(t, map[string]string{"plans/plan.md": "# v1"})
	events := openArtifactEvents(t, srv.URL)
	awaitArtifactEvent(t, events, "ready")

	if err := os.WriteFile(filepath.Join(dir, "plans", "plan.md"), []byte("# v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	awaitArtifactEvent(t, events, "artifacts_changed")
}

func TestArtifactEventsReportANewFile(t *testing.T) {
	srv, dir := artifactFileServer(t, map[string]string{"plan.md": "# plan"})
	events := openArtifactEvents(t, srv.URL)
	awaitArtifactEvent(t, events, "ready")

	if err := os.MkdirAll(filepath.Join(dir, "charts"), 0o755); err != nil {
		t.Fatal(err)
	}
	awaitArtifactEvent(t, events, "artifacts_changed")
	if err := os.WriteFile(filepath.Join(dir, "charts", "chart.html"), []byte("<p>chart</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	awaitArtifactEvent(t, events, "artifacts_changed")
}

func TestArtifactEventsLeaveOutWhatTheListSkips(t *testing.T) {
	srv, dir := artifactFileServer(t, map[string]string{
		"plan.md":                    "# plan",
		"node_modules/rxjs/index.js": "module",
		".versions/plan.md/1":        "kept version",
	})
	events := openArtifactEvents(t, srv.URL)
	awaitArtifactEvent(t, events, "ready")

	for _, skipped := range []string{"node_modules/rxjs/index.js", ".versions/plan.md/2", "__pycache__"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(skipped)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case got := <-events:
		t.Fatalf("event %q for a change the artifact list leaves out", got)
	case <-time.After(300 * time.Millisecond):
	}

	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	awaitArtifactEvent(t, events, "artifacts_changed")
}

func TestArtifactEventsRefuseAMissingArtifactDirectory(t *testing.T) {
	srv, dir := artifactFileServer(t, nil)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(srv.URL + "/api/v1/sessions/ao-1/artifact-events")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "ARTIFACT_DIR_NOT_FOUND") {
		t.Fatalf("status = %d body = %s, want 404 ARTIFACT_DIR_NOT_FOUND", resp.StatusCode, body)
	}
}
