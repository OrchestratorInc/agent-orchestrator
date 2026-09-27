//go:build !windows

package reasonix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type liveReasonixRequest struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// GetAgentHooks pins os.Executable, so the test binary is the disposable
// observer. Capture native payloads only in an explicitly configured fixture;
// ordinary test invocation still runs the complete suite normally.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "hooks" && os.Args[2] == "reasonix" {
		dir := os.Getenv("AO_REASONIX_HOOK_DIR")
		if !filepath.IsAbs(dir) {
			os.Exit(2)
		}
		for _, spec := range reasonixHookEvents {
			if os.Args[3] != spec.ao {
				continue
			}
			payload, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
			if err != nil {
				os.Exit(2)
			}
			path := filepath.Join(dir, fmt.Sprintf("%s.%d.json", spec.ao, os.Getpid()))
			if err := os.WriteFile(path+".tmp", payload, 0o600); err != nil {
				os.Exit(2)
			}
			if err := os.Rename(path+".tmp", path); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// TestReasonixLiveAOConformance qualifies an explicitly supplied patched CLI
// through AO's real tmux runtime. A disposable observer captures native hook
// payloads; AO CLI routing is covered separately by its unit tests.
func TestReasonixLiveAOConformance(t *testing.T) {
	if os.Getenv("AO_LIVE_REASONIX") != "1" {
		t.Skip("set AO_LIVE_REASONIX=1 and AO_REASONIX_TEST_BINARY to qualify a CLI")
	}
	binary := os.Getenv("AO_REASONIX_TEST_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("AO_REASONIX_TEST_BINARY must name an absolute CLI path")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("CLI unavailable: %v", err)
	}
	tmuxBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("live conformance requires tmux: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	fixtureHome, workspace, dataDir := t.TempDir(), t.TempDir(), t.TempDir()
	const configured = "AO_CONFIGURED_GUIDANCE_5905"
	const memory = "AO_PROJECT_GUIDANCE_5905"
	const private = "AO_PRIVATE_GUIDANCE_café_5905\nKeep this standing guidance private.\n"
	const task = "--literal café 日本語 task\nsecond line of the task"
	const restoredTask = "--continue résumé 中文 task"
	requests := make(chan liveReasonixRequest, 16)
	var sequence atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request liveReasonixRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		select {
		case requests <- request:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"AO_REASONIX_REPLY_%d\"}}]}\n\ndata: [DONE]\n\n", sequence.Add(1))
	}))
	t.Cleanup(provider.Close)
	configPath := filepath.Join(fixtureHome, "config.toml")
	config := fmt.Sprintf(`default_model = "fake/model-a"
language = "en"
[telemetry]
cli_metrics = "off"
[agent]
system_prompt = %q
[[providers]]
name = "fake"
kind = "openai"
base_url = %q
models = ["model-a"]
default = "model-a"
api_key_env = "AO_REASONIX_CONFORMANCE_KEY"
`, configured, provider.URL+"/v1")
	writeLiveReasonixFile(t, configPath, config)
	agentsPath := filepath.Join(workspace, "AGENTS.md")
	writeLiveReasonixFile(t, agentsPath, memory)
	promptFile := filepath.Join(dataDir, "private host café.md")
	writeLiveReasonixFile(t, promptFile, private)
	// Start the private tmux server and all direct CLI probes without inherited
	// credentials, proxy settings, real profile paths, or a user tmux config.
	hookDir := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "AO_REASONIX_HOOK_DIR=" + hookDir, "AO_SESSION_ID=reasonix-live-5905", "HOME=" + fixtureHome, "USERPROFILE=" + fixtureHome, "REASONIX_HOME=" + fixtureHome, "AO_DATA_DIR=" + dataDir, "AO_REASONIX_CONFORMANCE_KEY=local-test-key", "REASONIX_LANG=en", "TERM=xterm-256color", "SHELL=/bin/sh"}
	command := func(runCtx context.Context, executable string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(runCtx, executable, args...)
		cmd.Env, cmd.Dir = env, workspace
		return cmd
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "--local", "user.name", "AO Conformance"},
		{"config", "--local", "user.email", "ao-conformance@example.invalid"},
		{"add", "AGENTS.md"},
		{"commit", "-qm", "test: seed conformance workspace"},
	} {
		if out, err := command(ctx, "git", args...).CombinedOutput(); err != nil {
			t.Fatalf("initialize Git fixture: %v\n%s", err, out)
		}
	}
	plugin := New()
	plugin.lookup = func(context.Context) (string, error) { return binary, nil }
	plugin.probe = func(probeCtx context.Context, path string, args ...string) ([]byte, error) {
		return command(probeCtx, path, args...).Output()
	}
	// Reasonix may migrate a normal config on first startup. Pin the baseline
	// after that documented startup behavior before checking host integration.
	if out, err := command(ctx, binary, "run", "--help").CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture: %v\n%s", err, out)
	}
	baseline, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for path, want := range map[string]string{configPath: string(baseline), agentsPath: memory} {
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Errorf("host integration changed %s: %v", filepath.Base(path), err)
			}
		}
	})
	socket := fmt.Sprintf("ao-reasonix-conformance-%d-%d", os.Getpid(), time.Now().UnixNano())
	runtime := tmux.New(tmux.Options{Binary: tmuxBinary, LegacyBinary: tmuxBinary, SocketName: socket, Shell: "/bin/sh", Timeout: 5 * time.Second})
	var handle ports.RuntimeHandle
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if handle.ID != "" {
			if err := runtime.Destroy(cleanupCtx, handle); err != nil {
				t.Errorf("destroy conformance runtime: %v", err)
			}
		}
		// The keeper makes the private server's clean environment persist across
		// destroy/restore; never address the machine's default socket.
		if out, err := command(cleanupCtx, tmuxBinary, "-L", socket, "kill-server").CombinedOutput(); err != nil {
			t.Errorf("stop private tmux server: %v\n%s", err, out)
		}
	})
	if out, err := command(ctx, tmuxBinary, "-f", "/dev/null", "-L", socket, "new-session", "-d", "-s", "fixture-keeper", "/bin/sh").CombinedOutput(); err != nil {
		t.Fatalf("start private tmux server: %v\n%s", err, out)
	}
	launchCfg := ports.LaunchConfig{SessionID: "reasonix-live-5905", Kind: domain.KindWorker, WorkspacePath: workspace, DataDir: dataDir, SystemPromptFile: promptFile, Prompt: task, Config: ports.AgentConfig{Model: "fake/model-a"}}
	argv, err := plugin.GetLaunchCommand(ctx, launchCfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range argv {
		if arg == task || strings.Contains(arg, private) {
			t.Fatal("task or private instructions embedded in launch argv")
		}
	}
	runtimeEnv := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		runtimeEnv[key] = value
	}
	create := func(argv []string) {
		t.Helper()
		var err error
		handle, err = runtime.Create(ctx, ports.RuntimeConfig{SessionID: domain.SessionID(launchCfg.SessionID), WorkspacePath: workspace, Argv: argv, Env: runtimeEnv})
		if err != nil {
			t.Fatalf("AO runtime Create: %v", err)
		}
		// Match a realistic AO browser terminal; deterministic width also prevents
		// footer wrapping from obscuring the strict readiness evidence.
		if out, err := command(ctx, tmuxBinary, "-L", socket, "resize-window", "-t", "="+handle.ID, "-x", "120", "-y", "40").CombinedOutput(); err != nil {
			t.Fatalf("size conformance terminal: %v\n%s", err, out)
		}
	}
	if err := plugin.GetAgentHooks(ctx, ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: launchCfg.SessionID, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	create(argv)
	waitLiveReasonixReady(ctx, t, runtime, handle, plugin, "", promptFile)
	if err := runtime.SendMessage(ctx, handle, task); err != nil {
		t.Fatal(err)
	}
	assertLiveReasonixRoles(t, liveReasonixReceive(ctx, t, requests), private, configured, memory, task)
	waitLiveReasonixReady(ctx, t, runtime, handle, plugin, "AO_REASONIX_REPLY_1", promptFile)
	nativeID := liveReasonixHookID(ctx, t, hookDir, "session-start", 1)
	if manifestID := liveReasonixNativeID(t, fixtureHome); manifestID != nativeID {
		t.Fatalf("hook identity %q does not match native manifest %q", nativeID, manifestID)
	}
	for _, event := range []string{"user-prompt-submit", "stop"} {
		if got := liveReasonixHookID(ctx, t, hookDir, event, 1); got != nativeID {
			t.Fatalf("%s hook identity %q != %q", event, got, nativeID)
		}
	}
	if err := runtime.Destroy(ctx, handle); err != nil {
		t.Fatal(err)
	}
	handle = ports.RuntimeHandle{}
	if got := liveReasonixHookID(ctx, t, hookDir, "session-end", 1); got != nativeID {
		t.Fatalf("shutdown hook identity %q != %q", got, nativeID)
	}
	// Exact resume must select the canonical native ID even when an unrelated
	// workspace file has that name; permissive --resume gives the file priority.
	writeLiveReasonixFile(t, filepath.Join(workspace, nativeID), "not a Reasonix transcript\n")
	updated := private + "UPDATED_STANDING_GUIDANCE_5905\n"
	writeLiveReasonixFile(t, promptFile, updated)
	restored, ok, err := plugin.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ports.SessionRef{ID: launchCfg.SessionID, WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeID}}, SystemPromptFile: promptFile, Config: launchCfg.Config, Prompt: restoredTask})
	if err != nil || !ok {
		t.Fatalf("exact restore: ok=%v err=%v", ok, err)
	}
	create(restored)
	waitLiveReasonixReady(ctx, t, runtime, handle, plugin, "", promptFile)
	if err := runtime.SendMessage(ctx, handle, restoredTask); err != nil {
		t.Fatal(err)
	}
	assertLiveReasonixRoles(t, liveReasonixReceive(ctx, t, requests), updated, configured, memory, task, restoredTask)
	waitLiveReasonixReady(ctx, t, runtime, handle, plugin, "AO_REASONIX_REPLY_2", promptFile)
	for _, event := range []string{"session-start", "user-prompt-submit", "stop"} {
		if got := liveReasonixHookID(ctx, t, hookDir, event, 2); got != nativeID {
			t.Fatalf("restored %s hook identity %q != %q", event, got, nativeID)
		}
	}
	if got := liveReasonixNativeID(t, fixtureHome); got != nativeID {
		t.Fatalf("restore changed native identity: %q != %q", got, nativeID)
	}
	// Teardown must finish even after the task context is cancelled. Native
	// SessionEnd is asynchronous with tmux session destruction; wait for the
	// observer to finish writing before TempDir cleanup removes its directory.
	cancel()
	cleanupCtx, stopCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCleanup()
	if err := runtime.Destroy(cleanupCtx, handle); err != nil {
		t.Fatal(err)
	}
	handle = ports.RuntimeHandle{}
	if got := liveReasonixHookID(cleanupCtx, t, hookDir, "session-end", 2); got != nativeID {
		t.Fatalf("final shutdown hook identity %q != %q", got, nativeID)
	}
	t.Log("proved AO tmux launch, literal multiline Unicode task delivery, system/user role separation, native hook identity capture, and exact native restore; AO CLI hook routing is tested separately")
}

func writeLiveReasonixFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveReasonixReceive(ctx context.Context, t *testing.T, requests <-chan liveReasonixRequest) liveReasonixRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-ctx.Done():
		t.Fatalf("provider request missing: %v", ctx.Err())
		return liveReasonixRequest{}
	}
}

func waitLiveReasonixReady(ctx context.Context, t *testing.T, runtime *tmux.Runtime, handle ports.RuntimeHandle, plugin *Plugin, response, promptPath string) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var output string
	var err error
	for {
		output, err = runtime.GetOutput(ctx, handle, 80)
		if err == nil {
			if strings.Contains(output, "AO_PRIVATE_GUIDANCE") || strings.Contains(output, promptPath) {
				t.Fatal("private prompt content or path leaked to terminal")
			}
			if state, valid := plugin.DetectTerminalActivity(output); valid && state == domain.ActivityIdle && strings.Contains(output, response) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("readiness cancelled: %v; capture err=%v\n%s", ctx.Err(), err, output)
		case <-deadline.C:
			t.Fatalf("strict readiness not proven; capture err=%v\nFINAL CAPTURE:\n%s", err, output)
		case <-ticker.C:
		}
	}
}

func assertLiveReasonixRoles(t *testing.T, request liveReasonixRequest, private, configured, memory string, tasks ...string) {
	t.Helper()
	var system strings.Builder
	var users []string
	for _, message := range request.Messages {
		var content string
		if err := json.Unmarshal(message.Content, &content); err != nil {
			t.Fatalf("non-text provider message: %v", err)
		}
		if message.Role == "system" {
			system.WriteString(content)
		} else if strings.Contains(content, "AO_PRIVATE_GUIDANCE") {
			t.Fatalf("standing guidance leaked to %s role", message.Role)
		}
		if message.Role == "user" {
			users = append(users, content)
		}
	}
	standing := system.String()
	if strings.Count(standing, "AO_PRIVATE_GUIDANCE") != 1 || !strings.Contains(standing, private) {
		t.Fatal("provider system role missing exactly one current private block")
	}
	configuredAt, memoryAt, privateAt := strings.Index(standing, configured), strings.Index(standing, memory), strings.Index(standing, private)
	if configuredAt < 0 || memoryAt <= configuredAt || privateAt <= memoryAt {
		t.Fatal("system guidance composition order changed")
	}
	for _, task := range tasks {
		found := false
		for _, user := range users {
			if user == task || strings.HasSuffix(user, "\n\n"+task) {
				found = true
			}
		}
		if !found {
			t.Fatalf("user-role history lacks exact literal task %q; users=%q", task, users)
		}
		if strings.Contains(standing, task) {
			t.Fatalf("user task appeared in system role: %q", task)
		}
	}
}

func liveReasonixNativeID(t *testing.T, fixtureHome string) string {
	t.Helper()
	var ids []string
	err := filepath.WalkDir(fixtureHome, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "manifest.json" {
			return err
		}
		if filepath.Base(filepath.Dir(filepath.Dir(path))) != "sessions-v4" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var manifest struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			return err
		}
		ids = append(ids, manifest.SessionID)
		return nil
	})
	if err != nil || len(ids) != 1 || !ValidSessionID(ids[0]) {
		t.Fatalf("expected one native fixture session, got %q: %v", ids, err)
	}
	return ids[0]
}

func liveReasonixHookID(ctx context.Context, t *testing.T, hookDir, event string, minimum int) string {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		entries, err := os.ReadDir(hookDir)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), event+".") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			payload, err := os.ReadFile(filepath.Join(hookDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if !IsMainSessionHook(event, payload) {
				t.Fatalf("native %s payload is not recognized as a root hook: %s", event, payload)
			}
			ids = append(ids, HookSessionID(payload))
		}
		if len(ids) >= minimum {
			for _, id := range ids {
				if id != ids[0] {
					t.Fatalf("native %s hooks switched identity: %q", event, ids)
				}
			}
			return ids[0]
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for native %s hook: %v", event, ctx.Err())
		case <-deadline.C:
			t.Fatalf("native %s hook was not captured", event)
		case <-ticker.C:
		}
	}
}
