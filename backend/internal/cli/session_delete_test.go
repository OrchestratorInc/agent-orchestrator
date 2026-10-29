package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// deleteCommandServer answers the routes `ao session delete` uses. A delete of
// demo-dirty is refused the way the daemon refuses an uncleaned worktree.
func deleteCommandServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mu.Lock()
			requests = append(requests, r.Method+" "+r.URL.Path)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-old":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-old", "demo", "worker", "terminated", true)+`}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/sessions/demo-old":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-old"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/sessions/demo-dirty":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"conflict","code":"WORKSPACE_NOT_RELEASED","message":"The session's worktree has not been cleaned up yet. Run `+"`ao session cleanup`"+` first."}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}
}

func TestSessionDelete_ConfirmsThenDeletes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, requests := deleteCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("yes\n"),
		ProcessAlive: func(int) bool { return true },
	}, "session", "delete", "demo-old", "--project", "demo")
	if err != nil {
		t.Fatalf("session delete failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "Permanently delete session demo-old") || !strings.Contains(out, "session demo-old deleted") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-old", "DELETE /api/v1/sessions/demo-old"}
	if got := requests(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionDelete_DeclinedConfirmationDeletesNothing(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, requests := deleteCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("no\n"),
		ProcessAlive: func(int) bool { return true },
	}, "session", "delete", "demo-old")
	if err != nil {
		t.Fatalf("session delete failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "aborted") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if got := requests(); len(got) != 0 {
		t.Fatalf("requests = %#v, want none", got)
	}
}

func TestSessionDelete_YesSkipsPrompt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, requests := deleteCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "delete", "demo-old", "-y")
	if err != nil {
		t.Fatalf("session delete failed: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("--yes should skip the prompt:\n%s", out)
	}
	if got := requests(); !reflect.DeepEqual(got, []string{"DELETE /api/v1/sessions/demo-old"}) {
		t.Fatalf("requests = %#v", got)
	}
}

func TestSessionDelete_SurfacesDaemonRefusal(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := deleteCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "delete", "demo-dirty", "--yes")
	if err == nil {
		t.Fatal("delete of an uncleaned session must fail")
	}
	if !strings.Contains(err.Error()+errOut, "ao session cleanup") {
		t.Fatalf("error does not say how to proceed: %v\nstderr=%s", err, errOut)
	}
}

func TestSessionDelete_RequiresID(t *testing.T) {
	setConfigEnv(t)
	if _, _, err := executeCLI(t, Deps{}, "session", "delete"); err == nil {
		t.Fatal("delete without an id must fail")
	}
}

// The dry-run preview leaves out sessions whose workspace is already released
// or never existed: cleanup skips them, so listing them again is the phantom
// candidate list the issue reported.
func TestSessionCleanup_DryRunSkipsReleasedWorkspaces(t *testing.T) {
	cfg := setConfigEnv(t)
	session := func(id, cleanup string) string {
		s := sessionJSON(id, "demo", "worker", "terminated", true)
		if cleanup == "" {
			return s
		}
		return strings.TrimSuffix(s, "}") + `,"workspaceCleanup":"` + cleanup + `"}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions" {
			_, _ = io.WriteString(w, `{"sessions":[`+
				session("demo-1", "removed")+`,`+
				session("demo-2", "not_applicable")+`,`+
				session("demo-3", "preserved_dirty")+`,`+
				session("demo-4", "")+`]}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "cleanup", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v\nstderr=%s", err, errOut)
	}
	for _, id := range []string{"demo-1", "demo-2"} {
		if strings.Contains(out, "Would clean demo:"+id) {
			t.Fatalf("released %s offered again:\n%s", id, out)
		}
	}
	for _, id := range []string{"demo-3", "demo-4"} {
		if !strings.Contains(out, "Would clean demo:"+id) {
			t.Fatalf("pending %s missing:\n%s", id, out)
		}
	}
}
