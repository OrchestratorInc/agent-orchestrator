package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessionLinkServer serves one project session (demo-1) and one standalone
// session (standalone-40), which the daemon reports with an empty project.
func sessionLinkServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			if r.URL.Query().Get("active") == "false" {
				_, _ = io.WriteString(w, `{"sessions":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"sessions":[`+
				sessionJSON("demo-1", "demo", "worker", "working", false)+`,`+
				sessionJSON("standalone-40", "", "worker", "idle", false)+`]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/standalone-40":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("standalone-40", "", "worker", "idle", false)+`}`)
		case r.Method == http.MethodPost && r.URL.Path == cliInvokedPath:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"SESSION_NOT_FOUND","message":"session not found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const cliInvokedPath = "/internal/telemetry/cli-invoked"

func TestSessionLink_PrintsCanonicalLink(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"demo-1", "ao://sessions/demo/demo-1"},
		{"standalone-40", "ao://sessions/__standalone__/standalone-40"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			cfg := setConfigEnv(t)
			writeRunFileFor(t, cfg, sessionLinkServer(t))
			deps := Deps{ProcessAlive: func(int) bool { return true }}

			out, errOut, err := executeCLI(t, deps, "session", "link", tc.id)
			if err != nil {
				t.Fatalf("session link failed: %v\nstderr=%s", err, errOut)
			}
			if out != tc.want+"\n" {
				t.Fatalf("output = %q, want %q", out, tc.want+"\n")
			}

			out, errOut, err = executeCLI(t, deps, "session", "link", tc.id, "--json")
			if err != nil {
				t.Fatalf("session link --json failed: %v\nstderr=%s", err, errOut)
			}
			var got sessionLinkOutput
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("output is not decodable: %v\n%s", err, out)
			}
			if got.SessionID != tc.id || got.Link != tc.want {
				t.Fatalf("json = %#v, want id %q link %q", got, tc.id, tc.want)
			}
		})
	}
}

func TestSessionLink_Errors(t *testing.T) {
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, sessionLinkServer(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	_, _, err := executeCLI(t, deps, "session", "link")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("missing id exit code = %d, want 2 (err=%v)", got, err)
	}
	_, _, err = executeCLI(t, deps, "session", "link", "demo-1", "-p", "other")
	if got := ExitCode(err); got != 2 || err == nil || !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("project mismatch: exit %d, err %v", got, err)
	}
	_, _, err = executeCLI(t, deps, "session", "link", "nope-1")
	if got := ExitCode(err); got != 1 || err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("missing session: exit %d, err %v", got, err)
	}
}

func TestSessionGet_IncludesLink(t *testing.T) {
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, sessionLinkServer(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "session", "get", "standalone-40", "--json")
	if err != nil {
		t.Fatalf("session get --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not decodable: %v\n%s", err, out)
	}
	if got.Session.ProjectID != "" || got.Session.Link != "ao://sessions/__standalone__/standalone-40" {
		t.Fatalf("session = %#v", got.Session)
	}

	out, errOut, err = executeCLI(t, deps, "session", "get", "demo-1")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "link: ao://sessions/demo/demo-1\n") {
		t.Fatalf("human output lacks link:\n%s", out)
	}
}

func TestSessionList_JSONIncludesLinks(t *testing.T) {
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, sessionLinkServer(t))

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "ls", "--json")
	if err != nil {
		t.Fatalf("session ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not decodable: %v\n%s", err, out)
	}
	links := map[string]string{}
	for _, entry := range got.Data {
		links[entry.ID] = entry.Link
	}
	want := map[string]string{
		"demo-1":        "ao://sessions/demo/demo-1",
		"standalone-40": "ao://sessions/__standalone__/standalone-40",
	}
	for id, link := range want {
		if links[id] != link {
			t.Fatalf("link for %s = %q, want %q (all: %#v)", id, links[id], link, links)
		}
	}
}
