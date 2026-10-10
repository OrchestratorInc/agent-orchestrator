package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedCodexPersistentHostReceivesPrivateHTTPConfiguration(t *testing.T) {
	driver, _ := newTestDriver(t)
	driver.persistent = true
	wantedFailure := errors.New("host capture complete")
	var captured persistenthost.Config
	driver.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		captured = cfg
		return nil, wantedFailure
	}
	env := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "test-private-session-ticket", "CODEX_HOME": t.TempDir()}
	workspace := t.TempDir()
	data := t.TempDir()
	_, err := driver.Start(context.Background(), ports.ChatStartConfig{SessionID: "existing-session", WorkspacePath: workspace, DataDir: data, Env: env})
	if !errors.Is(err, wantedFailure) {
		t.Fatalf("capture failure was lost=%v", err)
	}
	if captured.SessionID != "existing-session" || captured.Workdir != workspace || captured.DataDir != data {
		t.Fatal("managed configuration lost persistent host ownership")
	}
	expected := agentlaunch.CodexProxyArgv([]string{"codex", "app-server"}, env)
	if !reflect.DeepEqual(captured.Argv, expected) {
		t.Fatalf("persistent host arguments=%v", captured.Argv)
	}
	if strings.Contains(strings.Join(captured.Argv, " "), env["AO_PROXY_TICKET"]) {
		t.Fatal("persistent process arguments contain session ticket")
	}
	joined := strings.Join(captured.Argv, " ")
	for _, setting := range []string{`model_provider="ao-managed"`, `wire_api="responses"`, `requires_openai_auth=false`, `supports_websockets=false`} {
		if !strings.Contains(joined, setting) {
			t.Fatalf("persistent launch missing configuration %s", setting)
		}
	}
	values := map[string]string{}
	for _, entry := range captured.Env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["AO_PROXY_TICKET"] != env["AO_PROXY_TICKET"] || values["CODEX_HOME"] != env["CODEX_HOME"] {
		t.Fatal("persistent host did not receive private launch environment")
	}
}
func TestManagedCodexPreparedHostUsesCurrentTicketRatherThanStaleLaunchEnv(t *testing.T) {
	driver, _ := newTestDriver(t)
	driver.persistent = true
	capturedStop := errors.New("prepared host captured")
	old := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4000", "AO_PROXY_TICKET": "old-private-ticket"}
	fresh := map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:5000", "AO_PROXY_TICKET": "current-private-ticket"}
	preparedCount := 0
	var cfg persistenthost.Config
	driver.connectHost = func(ctx context.Context, next persistenthost.Config) (*persistenthost.Transport, error) {
		cfg = next
		if next.Prepare == nil {
			t.Fatal("managed launch dropped lazy preparation")
		}
		prepared, err := next.Prepare(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(prepared.Argv, agentlaunch.CodexProxyArgv([]string{"codex", "app-server"}, fresh)) {
			t.Fatal("prepared host kept stale proxy address")
		}
		for _, arg := range prepared.Argv {
			if strings.Contains(arg, "private-ticket") {
				t.Fatal("prepared argv contains private ticket")
			}
		}
		values := map[string]string{}
		for _, entry := range prepared.Env {
			key, value, ok := strings.Cut(entry, "=")
			if ok {
				values[key] = value
			}
		}
		if values["AO_PROXY_TICKET"] != "current-private-ticket" {
			t.Fatal("prepared host kept stale session ticket")
		}
		return nil, capturedStop
	}
	_, err := driver.Start(context.Background(), ports.ChatStartConfig{SessionID: "s", WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Env: old, PrepareEnv: func(context.Context) (map[string]string, error) { preparedCount++; return fresh, nil }})
	if !errors.Is(err, capturedStop) || preparedCount != 1 {
		t.Fatalf("prepared host=%v calls=%d", err, preparedCount)
	}
	if strings.Contains(strings.Join(cfg.Argv, " "), "current-private-ticket") {
		t.Fatal("eager host configuration exposed fresh private ticket")
	}
	if old["AO_PROXY_ENDPOINT"] != "http://127.0.0.1:4000" || old["AO_PROXY_TICKET"] != "old-private-ticket" {
		t.Fatal("provider launch mutated caller's previous configuration")
	}
}
func TestManagedCodexPreparedHostCannotFallBackAfterPreparationFailure(t *testing.T) {
	driver, _ := newTestDriver(t)
	driver.persistent = true
	prepareFailure := errors.New("account routing unavailable")
	hostFailure := errors.New("captured host")
	called := false
	driver.connectHost = func(ctx context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		prepared, err := cfg.Prepare(ctx)
		if !errors.Is(err, prepareFailure) || len(prepared.Argv) != 0 || len(prepared.Env) != 0 {
			t.Fatal("failed preparation supplied native fallback configuration")
		}
		called = true
		return nil, hostFailure
	}
	_, err := driver.Start(context.Background(), ports.ChatStartConfig{SessionID: "s", WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Env: map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "ticket"}, PrepareEnv: func(context.Context) (map[string]string, error) { return nil, prepareFailure }})
	if !errors.Is(err, hostFailure) || !called {
		t.Fatal("failed launch preparation did not reach persistent host fence")
	}
}
func TestManagedCodexResumeRetainsNativeThreadAndRouteEnvironment(t *testing.T) {
	driver, server := newTestDriver(t)
	server.reply("thread/resume", `{"thread":{"id":"saved-native-thread","turns":[]}}`)
	original := driver.spawn
	var env []string
	driver.spawn = func(ctx context.Context, bin, workdir string, values []string) (*process, error) {
		env = append([]string(nil), values...)
		return original(ctx, bin, workdir, values)
	}
	config := ports.ChatResumeConfig{SessionID: "saved-session", WorkspacePath: t.TempDir(), ProviderConversationID: "saved-native-thread", Env: map[string]string{"AO_PROXY_ENDPOINT": "http://127.0.0.1:4567", "AO_PROXY_TICKET": "saved-ticket"}}
	conversation, err := driver.Resume(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conversation.Close() }()
	if conversation.ProviderConversationID() != "saved-native-thread" {
		t.Fatal("managed resume created a different native conversation")
	}
	if !server.sentMethod("thread/resume") || server.sentMethod("thread/start") {
		t.Fatal("managed resume replaced native history with a new thread")
	}
	found := false
	for _, entry := range env {
		if entry == "AO_PROXY_TICKET=saved-ticket" {
			found = true
		}
	}
	if !found {
		t.Fatal("native resume lost saved session ticket")
	}
	if server.sentMethod("account/login/start") {
		t.Fatal("managed resume invoked device login")
	}
	// Codex resumes a thread on the provider it was created with, whatever the
	// process was launched to use. A thread that began on this computer's own
	// sign-in only reaches the account helper if the resume names it.
	if got := resumeProvider(t, server); got != "ao-managed" {
		t.Fatalf("thread resumed on provider %q; a native thread would keep its native sign-in", got)
	}
}

// resumeProvider is the provider the client named when it resumed its thread.
func resumeProvider(t *testing.T, server *scriptedServer) string {
	t.Helper()
	resume := server.awaitFrame(func(f frame) bool { return f.Method == "thread/resume" })
	var params struct {
		ModelProvider string `json:"modelProvider"`
	}
	if err := json.Unmarshal(resume.Params, &params); err != nil {
		t.Fatal(err)
	}
	return params.ModelProvider
}

func TestCodexResumeOutsideAccountManagerNamesNoProvider(t *testing.T) {
	driver, server := newTestDriver(t)
	server.reply("thread/resume", `{"thread":{"id":"saved-native-thread","turns":[]}}`)
	conversation, err := driver.Resume(context.Background(), ports.ChatResumeConfig{SessionID: "saved-session", WorkspacePath: t.TempDir(), ProviderConversationID: "saved-native-thread"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conversation.Close() }()
	// The thread stays wherever it was created.
	if got := resumeProvider(t, server); got != "" {
		t.Fatalf("an unmanaged resume named provider %q", got)
	}
}
func TestManagedCodexDirectProcessLaunchKeepsTicketOutOfArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture; persistent-host argv tests cover all platforms")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "codex-fixture")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{"AO_PROXY_ENDPOINT=http://127.0.0.1:4567", "AO_PROXY_TICKET=private-test-ticket"}
	proc, err := spawnAppServer(context.Background(), bin, root, env)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proc.stop() }()
	data, err := io.ReadAll(proc.stdout)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := agentlaunch.CodexProxyArgvFromEnv([]string{bin, "app-server"}, env)[1:]
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("direct launch args=%v", args)
	}
	if strings.Contains(string(data), "private-test-ticket") {
		t.Fatal("direct process exposed ticket in command arguments")
	}
	if !strings.Contains(string(data), "supports_websockets=false") {
		t.Fatal("direct managed launch retained WebSocket transport")
	}
}
func TestNativeCodexPersistentLaunchDoesNotGainManagedProvider(t *testing.T) {
	driver, _ := newTestDriver(t)
	driver.persistent = true
	stopped := errors.New("native host captured")
	captured := false
	driver.connectHost = func(_ context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		captured = true
		if !reflect.DeepEqual(cfg.Argv, []string{"codex", "app-server"}) {
			t.Fatal("unmanaged session was changed to a proxy provider")
		}
		for _, entry := range cfg.Env {
			if strings.HasPrefix(entry, "AO_PROXY_TICKET=") && entry != "AO_PROXY_TICKET=" {
				t.Fatal("unmanaged host received account ticket")
			}
		}
		return nil, stopped
	}
	_, err := driver.Start(context.Background(), ports.ChatStartConfig{SessionID: "native", WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Env: map[string]string{"AO_PROXY_ENDPOINT": "", "AO_PROXY_TICKET": ""}})
	if !errors.Is(err, stopped) || !captured {
		t.Fatal("native host was not captured")
	}
}
