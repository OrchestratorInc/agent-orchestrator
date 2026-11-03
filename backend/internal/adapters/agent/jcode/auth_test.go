package jcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// fakeOllama serves Ollama's native model inventory and records each request.
func fakeOllama(t *testing.T, installed ...string) (server *httptest.Server, requests func() []string, setStatus func(int)) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	status := http.StatusOK
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		code := status
		mu.Unlock()
		if r.Method != http.MethodGet || r.URL.Path != "/api/tags" || code != http.StatusOK {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		models := make([]map[string]string, 0, len(installed))
		for _, name := range installed {
			models = append(models, map[string]string{"name": name, "model": name})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	}))
	t.Cleanup(server.Close)
	requests = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
	setStatus = func(code int) {
		mu.Lock()
		defer mu.Unlock()
		status = code
	}
	return server, requests, setStatus
}

func ollamaStatus(method string) string {
	return `{"any_available":true,"providers":[{"id":"claude","status":"not_configured","method":"not configured"},{"id":"ollama","status":"available","method":"` + method + `"}]}`
}

const ollamaCurrent = `{"requested_provider":"auto","resolved_provider":"Ollama","selected_model":"qwen3:1.7b"}`

// jcode's keyless Ollama mode (`jcode login ollama`, no key) is available only
// when the loopback Ollama server jcode reports answers its read-only
// inventory with jcode's selected model installed. Nothing runs a model and no
// credential is sent.
func TestAuthStatusVerifiesKeylessOllamaFromItsInstalledModels(t *testing.T) {
	server, requests, _ := fakeOllama(t, "llama3.2:latest", "qwen3:1.7b")
	binary, _ := fakeJCodeProvider(t, ollamaStatus("local endpoint (`"+server.URL+"/v1`)"), `{"providers":[]}`, ollamaCurrent)
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	got, err := (&Plugin{resolvedBinary: binary}).AuthStatus(ctx)
	if err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, %v; want authorized", got, err)
	}
	if seen := requests(); len(seen) != 1 || seen[0] != "GET /api/tags auth=" {
		t.Fatalf("requests = %v; want one credential-free GET /api/tags", seen)
	}
	diagnostics := evidence.Diagnostics()
	last := diagnostics[len(diagnostics)-1]
	if last.Code != "local_provider_available" || last.Provider != "Ollama" || last.Method != "GET "+server.URL+"/api/tags" {
		t.Fatalf("diagnostic = %+v", last)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "provider_accepted" {
			t.Fatalf("a keyless local provider must not report an accepted credential: %+v", diagnostic)
		}
	}
}

func TestAuthStatusKeepsKeylessOllamaConfiguredWhenUnavailable(t *testing.T) {
	server, _, setStatus := fakeOllama(t, "llama3.2:latest")
	local := ollamaStatus("local endpoint (`" + server.URL + "/v1`)")
	for name, tc := range map[string]struct {
		status, current string
		serverStatus    int
		code            string
	}{
		"model not installed": {local, ollamaCurrent, http.StatusOK, "model_selection_unresolved"},
		"server error":        {local, ollamaCurrent, http.StatusInternalServerError, "provider_unreachable"},
		"server down":         {ollamaStatus("local endpoint (`http://127.0.0.1:1/v1`)"), ollamaCurrent, http.StatusOK, "provider_unreachable"},
		"no selected model":   {local, `{"resolved_provider":"Ollama","selected_model":""}`, http.StatusOK, "model_selection_unresolved"},
	} {
		t.Run(name, func(t *testing.T) {
			setStatus(tc.serverStatus)
			binary, _ := fakeJCodeProvider(t, tc.status, `{"providers":[]}`, tc.current)
			ctx, evidence := ports.WithAuthEvidence(context.Background())
			got, err := (&Plugin{resolvedBinary: binary}).AuthStatus(ctx)
			diagnostics := evidence.Diagnostics()
			if err != nil || got != ports.AgentAuthStatusConfigured || diagnostics[len(diagnostics)-1].Code != tc.code {
				t.Fatalf("AuthStatus = %q, %v, diagnostics %+v; want configured with %s", got, err, diagnostics, tc.code)
			}
		})
	}
	setStatus(http.StatusOK)
}

// Only the keyless local Ollama mode is checked this way: a stored (possibly
// cloud) API key, a remote or non-Ollama endpoint, or another active provider
// keeps the existing behavior and never touches the Ollama server.
func TestAuthStatusDoesNotTreatOtherModesAsKeylessOllama(t *testing.T) {
	server, requests, _ := fakeOllama(t, "qwen3:1.7b")
	local := ollamaStatus("local endpoint (`" + server.URL + "/v1`)")
	lmStudio := `{"providers":[{"id":"lmstudio","status":"available","method":"local endpoint (` + "`" + server.URL + "/v1`" + `)"}]}`
	for name, tc := range map[string]struct{ status, current string }{
		"optional API key stored": {ollamaStatus("local endpoint (`" + server.URL + "/v1`) + optional API key (`OLLAMA_API_KEY`)"), ollamaCurrent},
		"remote endpoint":         {ollamaStatus("local endpoint (`https://ollama.example.com/v1`)"), ollamaCurrent},
		"not the /v1 endpoint":    {ollamaStatus("local endpoint (`" + server.URL + "/api`)"), ollamaCurrent},
		"another local provider":  {lmStudio, `{"resolved_provider":"LM Studio","selected_model":"qwen3:1.7b"}`},
		"another active provider": {local, `{"resolved_provider":"OpenAI-compatible","selected_model":"qwen3:1.7b"}`},
	} {
		t.Run(name, func(t *testing.T) {
			binary, _ := fakeJCodeProvider(t, tc.status, `{"providers":[]}`, tc.current)
			if got, err := (&Plugin{resolvedBinary: binary}).AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusConfigured {
				t.Fatalf("AuthStatus = %q, %v; want configured", got, err)
			}
		})
	}
	if seen := requests(); len(seen) != 0 {
		t.Fatalf("Ollama server was probed for another mode: %v", seen)
	}
}

func TestOllamaModelInstalledMatchesOllamaNames(t *testing.T) {
	installed := []string{"qwen3:1.7b", "llama3.2:latest", "hf.co/org/model:Q4"}
	for model, want := range map[string]bool{
		"qwen3:1.7b": true, "llama3.2": true, "llama3.2:latest": true, "hf.co/org/model:Q4": true,
		"qwen3": false, "qwen3:4b": false, "": false, "claude-opus-5-5": false,
	} {
		if got := OllamaModelInstalled(installed, model); got != want {
			t.Errorf("OllamaModelInstalled(%q) = %v, want %v", model, got, want)
		}
	}
}
