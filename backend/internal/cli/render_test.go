package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderServer(t *testing.T, status int, respBody string) (*httptest.Server, *previewCapture) {
	t.Helper()
	capture := &previewCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The root command pings /internal/telemetry/cli-invoked before every
		// run; only the render routes count as the CLI calling the daemon.
		if !strings.HasSuffix(r.URL.Path, "/renders") && !strings.HasSuffix(r.URL.Path, "/renders/check") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capture.called, capture.body, capture.path, capture.method = true, string(body), r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func writePage(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chart.html")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderPostsThePageToTheSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated,
		`{"renderId":"r1","activityId":"a1","path":"/api/v1/sessions/aa-47/renders/r1"}`)
	writeRunFileFor(t, cfg, srv)
	shot := writeImage(t, t.TempDir(), "shot.png", pngSignature)
	page := writePage(t, []byte(`<!doctype html><img src="`+shot+`">`))

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", page, "--title", "Turns by day", "--height", "420")
	if err != nil {
		t.Fatalf("render: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/aa-47/renders" {
		t.Fatalf("hit %s %s", capture.method, capture.path)
	}
	var req struct {
		HTML   string `json:"html"`
		Title  string `json:"title"`
		Height int    `json:"height"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// The CLI reads local images with the agent's own access and inlines them.
	if req.HTML != `<!doctype html><img src="data:image/png;base64,iVBORw0KGgo=">` || req.Title != "Turns by day" || req.Height != 420 {
		t.Fatalf("request = %+v", req)
	}
	if !strings.Contains(out, "r1") || strings.Contains(capture.body, "artifact") || strings.Contains(out, "artifact") {
		t.Fatalf("stdout = %q body = %.80q, want the render id and no artifact", out, capture.body)
	}
}

func TestRenderArtifactKeepsThePageOrWarns(t *testing.T) {
	for _, tc := range []struct {
		name, resp, stdout, stderr string
	}{
		{"kept", `{"renderId":"r1","artifactPath":"/ao/artifacts/aa-47/Turns by day.html"}`, "saved as artifact: /ao/artifacts/aa-47/Turns by day.html\n", ""},
		// The page is in the thread either way, so the command still succeeds.
		{"not kept", `{"renderId":"r1","artifactError":"save render artifact: disk full"}`, "", "warning: not saved as artifact: save render artifact: disk full\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "aa-47")
			cfg := setConfigEnv(t)
			srv, capture := renderServer(t, http.StatusCreated, tc.resp)
			writeRunFileFor(t, cfg, srv)

			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
				"render", writePage(t, []byte("<p>x</p>")), "--title", "Turns by day", "--artifact")
			if err != nil {
				t.Fatalf("render --artifact: %v\nstderr=%s", err, errOut)
			}
			if !strings.Contains(capture.body, `"artifact":true`) {
				t.Fatalf("request = %s, want artifact", capture.body)
			}
			if !strings.HasPrefix(out, renderShownText("r1")+"\n") || !strings.HasSuffix(out, tc.stdout) || !strings.HasSuffix(errOut, tc.stderr) {
				t.Fatalf("stdout = %q, stderr = %q", out, errOut)
			}
		})
	}
}

func TestRenderOutsideASessionDoesNotCallTheDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", writePage(t, []byte("<p>x</p>")), "--title", "x")
	if err == nil || capture.called {
		t.Fatalf("err=%v called=%v; want a usage error and no request", err, capture.called)
	}
}

func TestRenderRefusesOversizedFilesBeforeSending(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", writePage(t, make([]byte, 1<<20+1)), "--title", "x")
	if err == nil || capture.called {
		t.Fatalf("err=%v called=%v; want a size error and no request", err, capture.called)
	}
}

func TestRenderCheckWritesTheScreenshotAndPrintsConsole(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusOK,
		`{"screenshot":{"mimeType":"image/png","data":"iVBORw0KGgo=","width":390,"height":412},"contentHeight":412,`+
			`"consoleMessages":[{"level":"error","text":"Uncaught ReferenceError: d3 is not defined"}]}`)
	writeRunFileFor(t, cfg, srv)
	out := filepath.Join(t.TempDir(), "check.png")

	stdout, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", "--check", writePage(t, []byte("<p>chart</p>")), "--width", "390", "--out", out)
	if err != nil {
		t.Fatalf("render --check: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/aa-47/renders/check" || !strings.Contains(capture.body, `"width":390`) {
		t.Fatalf("hit %s with %s", capture.path, capture.body)
	}
	if png, err := os.ReadFile(out); err != nil || len(png) == 0 {
		t.Fatalf("screenshot not written: %v", err)
	}
	for _, want := range []string{out, "412", "console.error: Uncaught ReferenceError: d3 is not defined"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// The check, fix, check-again loop reuses one --out path; a symlink or a
// directory at that path is never replaced.
func TestRenderCheckReplacesItsEarlierScreenshot(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, _ := renderServer(t, http.StatusOK,
		`{"screenshot":{"mimeType":"image/png","data":"iVBORw0KGgo=","width":390,"height":412},"contentHeight":412,"consoleMessages":[]}`)
	writeRunFileFor(t, cfg, srv)
	dir := t.TempDir()
	out := filepath.Join(dir, "check.png")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := func(target string) error {
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
			"render", "--check", writePage(t, []byte("<p>chart</p>")), "--out", target)
		return err
	}
	for range 2 {
		if err := check(out); err != nil {
			t.Fatalf("render --check over an earlier screenshot: %v", err)
		}
	}
	if png, _ := os.ReadFile(out); string(png) != string(pngSignature) {
		t.Fatalf("screenshot = %q, want the new PNG", png)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if err := check(link); err == nil {
		t.Fatal("render --check replaced a symlink")
	}
	if kept, _ := os.ReadFile(secret); string(kept) != "keep" {
		t.Fatalf("symlink target = %q, want it untouched", kept)
	}
	if err := check(dir); err == nil {
		t.Fatal("render --check replaced a directory")
	}
}

func TestRenderWithoutTitleOrCheckDoesNotCallTheDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", writePage(t, []byte("<p>x</p>")))
	if err == nil || !strings.Contains(err.Error(), "--title") || capture.called {
		t.Fatalf("err=%v called=%v; want a --title usage error and no request", err, capture.called)
	}
}

func TestRenderWithAMissingLocalImagePublishesNothing(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusCreated, `{}`)
	writeRunFileFor(t, cfg, srv)
	gone := filepath.Join(t.TempDir(), "shot.png")

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", writePage(t, []byte(`<img src="`+gone+`">`)), "--title", "x")
	want := "These local images could not be read: " + gone + ". Use absolute paths to existing image files, or remove them."
	if err == nil || err.Error() != want || capture.called {
		t.Fatalf("err=%v called=%v; want %q and no request", err, capture.called, want)
	}
}

func TestRenderCheckInlinesImagesAndListsTheMissingOnes(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := renderServer(t, http.StatusOK,
		`{"screenshot":{"mimeType":"image/png","data":"iVBORw0KGgo=","width":720,"height":200},"contentHeight":200,"consoleMessages":[]}`)
	writeRunFileFor(t, cfg, srv)
	dir := t.TempDir()
	shot := writeImage(t, dir, "shot.png", pngSignature)
	gone := filepath.Join(dir, "gone.png")

	stdout, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"render", "--check", writePage(t, []byte(`<img src="`+shot+`"><img src="`+gone+`">`)),
		"--out", filepath.Join(dir, "check.png"))
	if err != nil {
		t.Fatalf("render --check: %v\nstderr=%s", err, errOut)
	}
	var req struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if want := `<img src="data:image/png;base64,iVBORw0KGgo="><img src="` + gone + `">`; req.HTML != want {
		t.Fatalf("html = %s, want %s", req.HTML, want)
	}
	// The paths come from the page, so they are marked untrusted like console text.
	if want := browserUntrustedText("missing images: " + gone); !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout)
	}
}
