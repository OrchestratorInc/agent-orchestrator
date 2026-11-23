package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func connectorDaemon(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) {
	t.Helper()
	t.Setenv("AO_PROJECT_ID", "")
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/internal/telemetry/cli-invoked" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if !handle(w, r) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
}

func TestConnectorAddPostsAURLOrACommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want connectorRequestDTO
	}{
		{
			name: "url signs in when the server asks",
			args: []string{"connector", "add", "linear", "--url", "https://mcp.linear.app/mcp"},
			want: connectorRequestDTO{Key: "linear", Transport: "http", URL: "https://mcp.linear.app/mcp", Auth: "oauth"},
		},
		{
			name: "a header selects header sign-in",
			args: []string{"connector", "add", "docs", "--url", "https://docs.example.com/mcp", "--header", "Authorization=Bearer t=1"},
			want: connectorRequestDTO{Key: "docs", Transport: "http", URL: "https://docs.example.com/mcp", Auth: "header", Headers: map[string]string{"Authorization": "Bearer t=1"}},
		},
		{
			name: "command after the dashes",
			args: []string{"connector", "add", "paper", "--env", "PAPER_KEY=k", "--", "npx", "-y", "paper-mcp"},
			want: connectorRequestDTO{Key: "paper", Transport: "stdio", Command: "npx", Args: []string{"-y", "paper-mcp"}, Env: map[string]string{"PAPER_KEY": "k"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got connectorRequestDTO
			connectorDaemon(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/connectors" {
					return false
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode: %v", err)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"connector":{"id":"c1","key":"`+got.Key+`","transport":"`+got.Transport+`","url":"`+got.URL+`","command":"`+got.Command+`","args":[],"envKeys":[],"headerKeys":[],"tools":[]}}`)
				return true
			})
			out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tt.args...)
			if err != nil {
				t.Fatalf("add: %v stderr=%s", err, stderr)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) || !strings.Contains(out, "added connector "+tt.want.Key) {
				t.Fatalf("body = %s, want %s; out = %q", gotJSON, wantJSON, out)
			}
		})
	}
}

func TestConnectorAddUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"connector", "add", "x"},
		{"connector", "add", "x", "--url", "https://x.example/mcp", "--", "npx"},
		{"connector", "add", "x", "--url", "https://x.example/mcp", "--header", "NoValue"},
		{"connector", "add"},
	} {
		connectorDaemon(t, func(http.ResponseWriter, *http.Request) bool { return false })
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
		if code := ExitCode(err); code != 2 {
			t.Errorf("%v: exit code = %d (%v), want 2", args, code, err)
		}
	}
}

func TestConnectorListRemoveAndDisable(t *testing.T) {
	var calls []string
	connectorDaemon(t, func(w http.ResponseWriter, r *http.Request) bool {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/connectors":
			_, _ = io.WriteString(w, `{"connectors":[{"id":"c1","key":"linear","transport":"http","url":"https://mcp.linear.app/mcp","enabled":true,"status":"needs_signin","args":[],"envKeys":[],"headerKeys":[],"tools":[]},{"id":"c2","key":"paper","transport":"stdio","command":"npx","args":["-y","paper-mcp"],"enabled":false,"status":"connected","projectId":"demo","envKeys":[],"headerKeys":[],"tools":[]}]}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/connectors/paper":
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"enabled":false}` {
				t.Errorf("patch body = %s", body)
			}
			_, _ = io.WriteString(w, `{"connector":{"id":"c2","key":"paper","args":[],"envKeys":[],"headerKeys":[],"tools":[]}}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/connectors/linear":
			w.WriteHeader(http.StatusNoContent)
		default:
			return false
		}
		return true
	})
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	out, _, err := executeCLI(t, deps, "connector", "ls")
	if err != nil || !strings.Contains(out, "linear  needs_signin  all") || !strings.Contains(out, "paper   off           demo     npx -y paper-mcp") {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	if out, _, err := executeCLI(t, deps, "connector", "disable", "paper"); err != nil || !strings.Contains(out, "disabled connector paper") {
		t.Fatalf("disable: %v %q", err, out)
	}
	if out, _, err := executeCLI(t, deps, "connector", "rm", "linear"); err != nil || !strings.Contains(out, "removed connector linear") {
		t.Fatalf("rm: %v %q", err, out)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %v", calls)
	}
}
