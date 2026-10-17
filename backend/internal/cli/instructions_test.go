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

// A dream saves its edit from inside its own session: the CLI must send the
// file's text, the revision it read and the session it runs in, to the
// project route when --project is given.
func TestInstructionsSetSendsTheFileRevisionAndSession(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "dream-1")
	var body saveInstructionsDTO
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/instructions"):
			path = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode save body: %v", err)
			}
			_, _ = io.WriteString(w, `{"instructions":{"projectId":"notes","body":"Run python3 -m unittest.","revision":8,"protectedLines":[],"removedLines":[]}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	file := filepath.Join(t.TempDir(), "next.md")
	if err := os.WriteFile(file, []byte("Run python3 -m unittest.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "instructions", "set", "--project", "notes", "--revision", "7", "--file", file)
	if err != nil {
		t.Fatalf("set: %v stderr=%s", err, stderr)
	}
	if path != "/api/v1/projects/notes/instructions" || body.Body != "Run python3 -m unittest.\n" || body.Revision == nil || *body.Revision != 7 || body.SessionID != "dream-1" {
		t.Fatalf("path=%q body=%+v", path, body)
	}
	if !strings.Contains(out, "saved project notes instructions (revision 8)") {
		t.Fatalf("output = %q", out)
	}
}

// Without --project the commands use the user's own instructions, and --json
// shows a dream what it needs: the revision and the protected lines.
func TestInstructionsShowJSONReadsTheUsersOwnInstructions(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/instructions":
			_, _ = io.WriteString(w, `{"instructions":{"body":"Answer first.","revision":3,"protectedLines":["Answer first."],"removedLines":["Use emoji."]}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "instructions", "show", "--json")
	if err != nil {
		t.Fatalf("show: %v stderr=%s", err, stderr)
	}
	var got instructionsDTO
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Revision != 3 || got.ProtectedLines[0] != "Answer first." || got.RemovedLines[0] != "Use emoji." {
		t.Fatalf("show --json = %q (%v)", out, err)
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "instructions", "set"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("set without --file err=%v exit=%d, want a usage error", err, ExitCode(err))
	}
}
