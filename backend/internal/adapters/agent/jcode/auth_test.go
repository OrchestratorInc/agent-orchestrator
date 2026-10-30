package jcode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeJCode writes a jcode stand-in that answers `auth status --json` and
// `usage --json` with the given bodies and logs every argv.
func fakeJCode(t *testing.T, status, usage string) (binary, log string) {
	return fakeJCodeProvider(t, status, usage, `{"requested_provider":"auto","resolved_provider":"Claude","selected_model":"claude-opus-5-5"}`)
}

func fakeJCodeProvider(t *testing.T, status, usage, current string) (binary, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "argv.log")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("status.json", status)
	write("usage.json", usage)
	write("current.json", current)
	script := "#!/bin/sh\n" +
		"echo \"$* JCODE_RUNTIME_DIR=$JCODE_RUNTIME_DIR JCODE_SOCKET=$JCODE_SOCKET NONINT=$JCODE_NON_INTERACTIVE\" >> '" + log + "'\n" +
		"case \"$*\" in\n" +
		"  *'auth status --json'*) cat '" + filepath.Join(dir, "status.json") + "' ;;\n" +
		"  *'usage --json'*) cat '" + filepath.Join(dir, "usage.json") + "' ;;\n" +
		"  *'provider current --json'*) cat '" + filepath.Join(dir, "current.json") + "' ;;\n" +
		"  *'server stop --force'*) exit 0 ;;\n" +
		"  *) exit 3 ;;\n" +
		"esac\n"
	binary = filepath.Join(dir, "jcode")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return binary, log
}

const notConfigured = `{"any_available":false,"providers":[{"id":"claude","status":"not_configured","validation_method":"presence check"}]}`
const claudeAvailable = `{"any_available":true,"providers":[{"id":"claude","status":"available","validation_method":"timestamp check","validation":"runtime validated (6d ago)"}]}`

func TestAuthStatusUnauthorizedWithoutAnyCredential(t *testing.T) {
	binary, log := fakeJCode(t, notConfigured, `{"providers":[]}`)
	got, err := (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("AuthStatus = %q, %v; want unauthorized", got, err)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "usage") {
		t.Fatalf("usage probe ran without a credential: %s", data)
	}
}

func TestAuthStatusNeverAuthorizesFromPresenceOrStoredValidation(t *testing.T) {
	for name, usage := range map[string]string{
		"no usage endpoint": `{"providers":[]}`,
		"quota":             `{"providers":[{"provider_name":"Anthropic (Claude)","limits":[],"error":"Usage API error (429 Too Many Requests)"}]}`,
		"rejected":          `{"providers":[{"provider_name":"Anthropic (Claude)","limits":[],"error":"Usage API error (401 Unauthorized)"}]}`,
		"garbage":           `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			binary, _ := fakeJCode(t, claudeAvailable, usage)
			got, _ := (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background())
			if got != ports.AgentAuthStatusConfigured {
				t.Fatalf("AuthStatus = %q; present but unverified credentials must be configured", got)
			}
		})
	}
}

func TestAuthStatusAuthorizedOnlyAfterLiveProviderRoundTrip(t *testing.T) {
	usage := `{"providers":[{"provider_name":"Anthropic (Claude)","limits":[{"name":"5h","usage_percent":3.0}],"error":null}]}`
	binary, log := fakeJCode(t, claudeAvailable, usage)
	got, err := (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, %v; want authorized", got, err)
	}
	data, _ := os.ReadFile(log)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(line, "--no-update") || !strings.Contains(line, "NONINT=1") {
			t.Fatalf("probe could self-update or prompt: %q", line)
		}
	}
}

func TestAuthStatusVerifiesTheActiveProviderNotAnyProvider(t *testing.T) {
	usage := `{"providers":[` +
		`{"provider_name":"Anthropic (Claude)","limits":[],"error":"Usage API error (401 Unauthorized)"},` +
		`{"provider_name":"OpenRouter","limits":[{"name":"credits","usage_percent":1.0}],"error":null}]}`
	binary, _ := fakeJCode(t, claudeAvailable, usage)
	got, _ := (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background())
	if got != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = %q; a valid OpenRouter key must not verify the selected Claude login", got)
	}

	binary, _ = fakeJCodeProvider(t, claudeAvailable, usage, `{"requested_provider":"openrouter","resolved_provider":"OpenRouter","selected_model":"x"}`)
	got, _ = (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background())
	if got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q; the active OpenRouter provider passed its live check", got)
	}

	binary, _ = fakeJCodeProvider(t, claudeAvailable, usage, `{"requested_provider":"deepseek","resolved_provider":"DeepSeek","selected_model":"x"}`)
	got, _ = (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background())
	if got != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = %q; a provider without a usage check stays configured", got)
	}
}

func TestTerminateNativeSessionStopsOnlyThisSessionsDaemon(t *testing.T) {
	t.Setenv("JCODE_SOCKET", "/tmp/user-shared/jcode.sock")
	binary, log := fakeJCode(t, notConfigured, `{}`)
	dataDir := t.TempDir()
	p := &Plugin{resolvedBinary: binary}
	ref := ports.SessionRef{ID: "proj-7", DataDir: dataDir}
	if err := p.TerminateNativeSession(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Fatalf("stopped a daemon that was never started: %s", data)
	}
	dir := runtimeDir(dataDir, "proj-7")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jcode.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.TerminateNativeSession(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "server stop --force JCODE_RUNTIME_DIR="+dir+" JCODE_SOCKET="+filepath.Join(dir, "jcode.sock")+" ") {
		t.Fatalf("stop argv/env = %q", data)
	}
}

func TestAuthDiagnosticsDescribeTheProviderCheck(t *testing.T) {
	cases := map[string]struct{ usage, current, code string }{
		"accepted":    {`{"providers":[{"provider_name":"Anthropic (Claude)","limits":[],"error":null}]}`, `{"resolved_provider":"Claude"}`, "provider_accepted"},
		"unsupported": {`{"providers":[]}`, `{"resolved_provider":"DeepSeek"}`, "provider_unsupported"},
		"quota":       {`{"providers":[{"provider_name":"Anthropic (Claude)","limits":[],"error":"Usage API error (429 Too Many Requests)"}]}`, `{"resolved_provider":"Claude"}`, "provider_unverified"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			binary, _ := fakeJCodeProvider(t, claudeAvailable, tc.usage, tc.current)
			ctx, evidence := ports.WithAuthEvidence(context.Background())
			if _, err := (&Plugin{resolvedBinary: binary}).AuthStatus(ctx); err != nil {
				t.Fatal(err)
			}
			diagnostics := evidence.Diagnostics()
			last := diagnostics[len(diagnostics)-1]
			if last.Code != tc.code || !strings.Contains(last.Method, "usage") && tc.code != "provider_unsupported" {
				t.Fatalf("last diagnostic = %+v, want %s", last, tc.code)
			}
		})
	}
}
