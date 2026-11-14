package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	claudeSignedInPath   = "/api/v1/agents/claude-code/auth/signed-in"
	claudeOperationsPath = "/api/v1/agents/claude-code/auth/operations"
)

// claudeLoginDaemon stands in for the daemon `ao claude-login` reports to.
// failing lists the paths that answer with a store error.
type claudeLoginDaemon struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]map[string]string
}

func startClaudeLoginDaemon(t *testing.T, failing ...string) *claudeLoginDaemon {
	t.Helper()
	cfg := setConfigEnv(t)
	daemon := &claudeLoginDaemon{bodies: map[string]map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		daemon.mu.Lock()
		daemon.requests = append(daemon.requests, r.Method+" "+r.URL.Path)
		daemon.bodies[r.URL.Path] = body
		daemon.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if slices.Contains(failing, r.URL.Path) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"AGENT_AUTH_STORE_UNAVAILABLE","message":"AO's store could not be written."}`)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == claudeSignedInPath:
			_, _ = io.WriteString(w, `{"releasedKey":"ANTHROPIC_API_KEY"}`)
		case r.Method == http.MethodPost && r.URL.Path == claudeOperationsPath:
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"login-1","agentId":"claude-code","method":"setup_token","status":"succeeded","expiresAt":"2026-11-11T00:00:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return daemon
}

func (d *claudeLoginDaemon) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.requests...)
}

func executeClaudeLogin(t *testing.T, deps Deps) error {
	t.Helper()
	deps.ProcessAlive = func(int) bool { return true }
	cmd := newClaudeLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/claude"})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	return cmd.Execute()
}

func TestClaudeLoginRunsEverySupportedLoginMethod(t *testing.T) {
	tests := []struct {
		name      string
		selection string
		wantArgs  []string
	}{
		{name: "Claude subscription", selection: "1\n", wantArgs: []string{"auth", "login", "--claudeai"}},
		{name: "Anthropic Console", selection: "2\n", wantArgs: []string{"auth", "login", "--console"}},
		{name: "SSO", selection: "3\n", wantArgs: []string{"auth", "login", "--sso"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			daemon := startClaudeLoginDaemon(t)
			var gotName string
			var gotArgs []string
			err := executeClaudeLogin(t, Deps{
				In:  strings.NewReader(tt.selection),
				Out: io.Discard,
				Err: io.Discard,
				LookPath: func(string) (string, error) {
					return "", errors.New("not on PATH")
				},
				RunInteractiveCommand: func(_ context.Context, name, dir string, args []string, _ io.Reader, _, _ io.Writer) error {
					gotName = name
					gotArgs = append([]string(nil), args...)
					return nil
				},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if gotName != "/managed/bin/claude" {
				t.Errorf("executable = %q, want adapter-resolved Claude", gotName)
			}
			if !slices.Equal(gotArgs, tt.wantArgs) {
				t.Errorf("args = %q, want %q", gotArgs, tt.wantArgs)
			}
			// The newest sign-in wins over an earlier setup token and an API key
			// saved from Claude Code's AO panel; the daemon owns both changes.
			if got := daemon.sent(); !slices.Equal(got, []string{"POST " + claudeSignedInPath}) {
				t.Errorf("daemon requests = %q, want the sign-in recorded", got)
			}
		})
	}
}

func TestClaudeLoginMenuListsEverySupportedMethod(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeClaudeLoginMenu(&stdout, codexLoginStyle{}); err != nil {
		t.Fatalf("writeClaudeLoginMenu: %v", err)
	}
	for _, fragment := range []string{"Sign in to Claude Code", "Claude subscription", "Anthropic Console", "SSO", "Setup token", "Selection [1-4]"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("menu missing %q:\n%s", fragment, stdout.String())
		}
	}
}

func setupTokenDeps(t *testing.T, token string, output io.Writer) Deps {
	t.Helper()
	return Deps{
		In: strings.NewReader("4\n"), Out: output, Err: io.Discard,
		ReadSecret: func(io.Reader) ([]byte, error) { return []byte(token), nil },
		RunInteractiveCommand: func(context.Context, string, string, []string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("setup token must not be sent to the Claude CLI")
			return nil
		},
	}
}

func TestClaudeLoginSavesSetupTokenThroughTheDaemon(t *testing.T) {
	daemon := startClaudeLoginDaemon(t)
	const token = "sk-ant-oat01-local-setup-token"
	var output bytes.Buffer
	if err := executeClaudeLogin(t, setupTokenDeps(t, token, &output)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := daemon.sent(); !slices.Equal(got, []string{"POST " + claudeOperationsPath}) {
		t.Fatalf("daemon requests = %q, want one setup-token sign-in", got)
	}
	if body := daemon.bodies[claudeOperationsPath]; body["method"] != "setup_token" || body["secret"] != token {
		t.Fatalf("setup-token request = %v", body)
	}
	if strings.Contains(output.String(), token) {
		t.Fatal("setup token appeared in terminal output")
	}
	if !strings.Contains(output.String(), "sign in to local sessions with this setup token") {
		t.Fatalf("output = %q, want the token reported as saved", output.String())
	}
}

func TestClaudeLoginRejectsInvalidSetupToken(t *testing.T) {
	daemon := startClaudeLoginDaemon(t)
	if err := executeClaudeLogin(t, setupTokenDeps(t, "invalid token", io.Discard)); err == nil || ExitCode(err) != 2 {
		t.Fatalf("Execute error = %v, want usage error", err)
	}
	if got := daemon.sent(); len(got) != 0 {
		t.Fatalf("invalid token reached the daemon: %q", got)
	}
}

// The sign-in already succeeded by the time AO records it, so a failure to
// update AO's state is reported as a warning, not a failed sign-in.
func TestClaudeLoginSucceedsWhenRecordingTheSignInFails(t *testing.T) {
	startClaudeLoginDaemon(t, claudeSignedInPath)
	var stdout, stderr bytes.Buffer
	err := executeClaudeLogin(t, Deps{
		In:  strings.NewReader("1\n"),
		Out: &stdout,
		Err: &stderr,
		LookPath: func(string) (string, error) {
			return "", errors.New("not on PATH")
		},
		RunInteractiveCommand: func(context.Context, string, string, []string, io.Reader, io.Writer, io.Writer) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "sign-in complete") {
		t.Fatalf("stdout = %q, want the sign-in reported as complete", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Warning:") || !strings.Contains(stderr.String(), "AO's store could not be written") {
		t.Fatalf("stderr = %q, want the daemon's reason as a warning", stderr.String())
	}
}

func TestClaudeLoginFailsWhenTheDaemonCannotSaveTheSetupToken(t *testing.T) {
	startClaudeLoginDaemon(t, claudeOperationsPath)
	err := executeClaudeLogin(t, setupTokenDeps(t, "sk-ant-oat01-unsaved-setup-token", io.Discard))
	if err == nil || !strings.Contains(err.Error(), "AO's store could not be written") {
		t.Fatalf("Execute error = %v, want the daemon's reason", err)
	}
}
