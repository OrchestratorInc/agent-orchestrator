//go:build e2e && linux

package codexappserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedChatInstalledParallel(t *testing.T) {
	if os.Getenv("AO_MANAGED_CHAT_SANDBOX") == "1" {
		testManagedChatInstalledParallel(t)
		return
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("installed client required")
	}
	wrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("network-isolated fixture requires bubblewrap")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, wrap, "--die-with-parent", "--unshare-net", "--unshare-pid",
		"--ro-bind", "/", "/", "--tmpfs", "/home", "--tmpfs", "/run", "--tmpfs", "/tmp",
		"--ro-bind", self, "/tmp/managed-chat-test",
		"--proc", "/proc", "--dev", "/dev", "--bind", scratch, scratch, "--chdir", scratch,
		"/tmp/managed-chat-test", "-test.run=^TestManagedChatInstalledParallel$", "-test.v", "-test.timeout=60s")
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + scratch, "TMPDIR=" + scratch,
		"SHELL=/bin/sh", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0", "AO_MANAGED_CHAT_SANDBOX=1", "AO_MANAGED_CHAT_BINARY=" + binary}
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal("network-isolated installed Chat proof failed", err)
	}
}

func installedManagedDriver(t *testing.T) *ManagedDriver {
	t.Helper()
	d := NewManaged(fixedCodexPlugin(os.Getenv("AO_MANAGED_CHAT_BINARY")), nil)
	// This fixture proves the installed protocol and HTTP boundary, not host recovery.
	d.open = func(ctx context.Context, cfg persistenthost.Config) (managedHost, error) {
		command := exec.CommandContext(ctx, cfg.Argv[0], cfg.Argv[1:]...)
		command.Env, command.Dir = cfg.Env, cfg.Workdir
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdin, err := command.StdinPipe()
		if err != nil {
			return managedHost{}, err
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return managedHost{}, err
		}
		if err := command.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return managedHost{}, err
		}
		var once sync.Once
		stop := func() error {
			once.Do(func() {
				_ = stdin.Close()
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				_ = command.Wait()
			})
			return nil
		}
		t.Cleanup(func() { _ = stop() })
		return managedHost{process: &process{stdin: stdin, stdout: stdout, stop: stop, terminate: stop}, identity: cfg.OwnershipFingerprint}, nil
	}
	return d
}

func testManagedChatInstalledParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	nativeHome := t.TempDir()
	originals := map[string]string{
		"auth.json":   `{"auth_mode":"apikey","OPENAI_API_KEY":"synthetic-device-key"}`,
		"config.toml": "model_provider='openai'\ncli_auth_credentials_store='file'\n",
	}
	for name, contents := range originals {
		if err := os.WriteFile(filepath.Join(nativeHome, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", nativeHome)
	t.Setenv("OPENAI_API_KEY", "synthetic-device-env-key")
	entered := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var revoked atomic.Bool
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Model string          `json:"model"`
			Input json.RawMessage `json:"input"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-route-")
		if (account != "a" && account != "b") || !strings.Contains(string(body.Input), "only-account-"+account) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if account == "a" && revoked.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintln(w, `{"error":{"message":"synthetic route revoked","type":"invalid_api_key"}}`)
			return
		}
		select {
		case entered <- account:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event any) {
			payload, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			w.(http.Flusher).Flush()
		}
		response := map[string]any{"id": "response-" + account, "object": "response", "status": "in_progress", "output": []any{}}
		emit(map[string]any{"type": "response.created", "response": response})
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		item := map[string]any{"id": "message-" + account, "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "account-" + account, "annotations": []any{}}}}
		emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		response["status"], response["output"] = "completed", []any{item}
		response["usage"] = map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2,
			"input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}}
		emit(map[string]any{"type": "response.completed", "response": response})
	}))
	t.Cleanup(func() { finish(); server.CloseClientConnections(); server.Close() })
	var escapedAuthorization atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer synthetic-route-") {
			escapedAuthorization.Add(1)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(func() { proxy.CloseClientConnections(); proxy.Close() })
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	t.Setenv("NO_PROXY", "upper.internal")
	t.Setenv("no_proxy", "lower.internal")
	root := t.TempDir()
	conversations := make(map[string]ports.ChatConversation)
	configs := make(map[string]ports.ChatStartConfig)
	for _, account := range []string{"a", "b"} {
		driver := installedManagedDriver(t)
		cfg := ports.ChatStartConfig{SessionID: domain.SessionID("installed-" + account), ControllerGeneration: "generation-" + account,
			DataDir: root, WorkspacePath: t.TempDir(), Model: "gpt-5.4", Effort: "medium",
			Env:   map[string]string{managedCodexTokenEnv: "synthetic-route-" + account},
			Route: &ports.AgentProviderRoute{BaseURL: server.URL, TokenEnv: managedCodexTokenEnv}}
		conversation, err := driver.Start(ctx, cfg)
		if err != nil {
			t.Fatal("installed managed Chat start failed", account, err)
		}
		conversations[account] = conversation
		configs[account] = cfg
		if _, err := conversation.SendTurn(ctx, ports.ChatUserMessage{Text: "only-account-" + account}); err != nil {
			t.Fatal("installed managed turn failed", account, err)
		}
	}
	seen := make(map[string]bool)
	for len(seen) < 2 {
		select {
		case account := <-entered:
			seen[account] = true
		case <-ctx.Done():
			t.Fatal("both pinned requests did not overlap", ctx.Err())
		}
	}
	finish()
	for account, conversation := range conversations {
		awaitInstalledManagedTurn(ctx, t, conversation, domain.TurnStateCompleted, "account-"+account)
	}
	first := conversations["a"]
	nativeID := first.ProviderConversationID()
	if _, err := first.(ports.ChatHistoryReader).ReadHistory(ctx); err != nil {
		t.Fatal("managed native history could not be observed", err)
	}
	if err := first.(interface{ Terminate() error }).Terminate(); err != nil {
		t.Fatal(err)
	}
	cfg := configs["a"]
	resumed, err := installedManagedDriver(t).Resume(ctx, ports.ChatResumeConfig{
		SessionID: cfg.SessionID, ControllerGeneration: "generation-a-restart", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Model: cfg.Model, Effort: cfg.Effort, Env: cfg.Env, Route: cfg.Route, ProviderConversationID: nativeID})
	if err != nil || resumed.ProviderConversationID() != nativeID {
		t.Fatal("private history did not survive a provider restart", err)
	}
	history, err := resumed.(ports.ChatHistoryReader).ReadHistory(ctx)
	found := false
	for _, event := range history {
		found = found || event.Kind == ports.ChatEventMessageCompleted && event.Text == "account-a"
	}
	if err != nil || !found {
		t.Fatal("restarted managed profile lost its settled history", err)
	}
	revoked.Store(true)
	if _, err := resumed.SendTurn(ctx, ports.ChatUserMessage{Text: "only-account-a after-revocation"}); err != nil {
		t.Fatal("revocation control failed before the HTTP request", err)
	}
	awaitInstalledManagedTurn(ctx, t, resumed, domain.TurnStateFailed, "")
	if _, err := conversations["b"].SendTurn(ctx, ports.ChatUserMessage{Text: "only-account-b still-authorized"}); err != nil {
		t.Fatal(err)
	}
	awaitInstalledManagedTurn(ctx, t, conversations["b"], domain.TurnStateCompleted, "account-b")
	for name, contents := range originals {
		got, err := os.ReadFile(filepath.Join(nativeHome, name))
		if err != nil || string(got) != contents {
			t.Fatal("managed workflow modified native profile", name, err)
		}
	}
	if escapedAuthorization.Load() != 0 {
		t.Fatal("managed authorization escaped to the ambient proxy")
	}
}

func awaitInstalledManagedTurn(ctx context.Context, t *testing.T, conversation ports.ChatConversation, want domain.TurnState, wantText string) {
	t.Helper()
	text := ""
	for {
		select {
		case event, ok := <-conversation.Events():
			if !ok {
				t.Fatal("installed conversation closed before completion")
			}
			if event.Kind == ports.ChatEventMessageCompleted {
				text = event.Text
			}
			if event.Kind == ports.ChatEventTurnCompleted {
				if event.TurnState != want || text != wantText {
					t.Fatal("incorrect account response or turn state", event.TurnState, text)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("installed managed turn did not complete", ctx.Err())
		}
	}
}
