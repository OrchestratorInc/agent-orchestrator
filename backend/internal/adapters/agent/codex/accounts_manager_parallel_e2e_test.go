//go:build e2e && linux

package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManagedRouteInstalledParallel(t *testing.T) {
	if os.Getenv("AO_ROUTE_PARALLEL_SANDBOX") == "1" {
		t.Run("overlap", testInstalledRouteOverlap)
		t.Run("shared-account-distinct-sessions", testInstalledRouteSharedAccount)
		t.Run("wrong-account-oracle", testInstalledRouteWrongAccount)
		t.Run("disable-a-preserves-b", testInstalledRouteDisable)
		t.Run("restart-revision", testInstalledRouteRestart)
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
	scratch := t.TempDir()
	runner := filepath.Join(scratch, "runner")
	build := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "-o", runner, "./cmd/ao-accounts-manager")
	build.Dir = "../../../../../accounts-manager/runner"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build isolated runner: %v\n%s", err, out)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, wrap, "--die-with-parent", "--unshare-net", "--unshare-pid",
		"--ro-bind", "/", "/", "--tmpfs", "/home", "--tmpfs", "/run", "--tmpfs", "/tmp",
		"--proc", "/proc", "--dev", "/dev", "--bind", scratch, scratch, "--chdir", scratch,
		self, "-test.run=^TestManagedRouteInstalledParallel$", "-test.v", "-test.timeout=80s")
	child.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + scratch, "TMPDIR=" + scratch,
		"SHELL=/bin/sh", "GIN_MODE=release", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0",
		"AO_ROUTE_PARALLEL_SANDBOX=1", "AO_ROUTE_TEST_BINARY=" + binary, "AO_ROUTE_TEST_RUNNER=" + runner}
	child.WaitDelay = time.Second
	output, err := child.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal("isolated installed-process proof failed", err)
	}
}

type installedRouteOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *installedRouteOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *installedRouteOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type installedRouteStream struct {
	account  string
	entered  chan string
	release  chan struct{}
	once     sync.Once
	requests int
}

type installedRouteFixture struct {
	t             *testing.T
	ctx           context.Context
	root          string
	home          string
	baseURL       string
	model         string
	runner        *exec.Cmd
	runnerLog     installedRouteOutput
	client        *core.ManagementClient
	transport     *http.Transport
	keys          map[string]string
	refs          map[string]string
	routes        map[string]core.RouteCapability
	mu            sync.Mutex
	bindings      core.BindingSnapshot
	renewBindings bool
	streams       map[string]*installedRouteStream
	originals     map[string][]byte
}

func (f *installedRouteFixture) Endpoint() (core.Endpoint, bool) {
	return core.Endpoint{BaseURL: f.baseURL, ManagementToken: "test-route-management"}, true
}

func newInstalledRouteFixture(t *testing.T) *installedRouteFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	f := &installedRouteFixture{t: t, ctx: ctx, root: t.TempDir(), refs: make(map[string]string),
		routes: make(map[string]core.RouteCapability), streams: make(map[string]*installedRouteStream),
		keys:     map[string]string{"a": "test-upstream-secret-a", "b": "test-upstream-secret-b"},
		bindings: core.BindingSnapshot{Revision: 1}, renewBindings: true, originals: make(map[string][]byte)}
	t.Cleanup(cancel)
	f.home = filepath.Join(f.root, "native-home")
	f.write("native-home/auth.json", `{"auth_mode":"apikey","OPENAI_API_KEY":"test-native-file-secret"}`)
	f.write("native-home/config.toml", "cli_auth_credentials_store='file'\nmodel_provider='openai'\ncheck_for_update_on_startup=false\n[analytics]\nenabled=false\n")
	for _, name := range []string{"auth.json", "config.toml"} {
		f.originals[name], _ = os.ReadFile(filepath.Join(f.home, name))
	}
	f.transport = http.DefaultTransport.(*http.Transport).Clone()
	f.transport.Proxy = nil
	t.Cleanup(f.transport.CloseIdleConnections)
	f.client = core.NewManagementClient(f, &http.Client{Transport: f.transport, Timeout: 3 * time.Second})
	upstream := httptest.NewServer(http.HandlerFunc(f.upstream))
	t.Cleanup(func() { upstream.CloseClientConnections(); upstream.Close() })
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	f.baseURL = fmt.Sprintf("http://127.0.0.1:%d", address.Port)
	f.write("state/control.key", "test-route-control\n")
	f.write("state/management.key", "test-route-management\n")
	f.write("state/routing.key", strings.Repeat("31", 32)+"\n")
	if err := os.Mkdir(filepath.Join(f.root, "state", "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.write("state/config.yaml", fmt.Sprintf("host: 127.0.0.1\nport: %d\nauth-dir: %s\napi-keys: [test-route-client]\nremote-management:\n  allow-remote: false\n  secret-key: ''\n  disable-control-panel: true\n  disable-auto-update-panel: true\nplugins:\n  enabled: false\npprof:\n  enable: false\ndiscovery:\n  enabled: false\nrequest-log: false\nlogging-to-file: false\nusage-statistics-enabled: false\n", address.Port, filepath.Join(f.root, "state", "auth")))
	t.Cleanup(f.stop)
	f.start()
	for _, account := range []string{"a", "b"} {
		credential, err := f.client.AddAPIKey(ctx, core.APIKeyInput{OperationID: "installed-" + account,
			Provider: core.ProviderCodex, Key: f.keys[account], BaseURL: upstream.URL + "/v1"})
		if err != nil || credential.Verification != "verified" {
			t.Fatal("fixture account was not verified", err)
		}
		f.refs[account] = credential.Ref
		id, err := f.client.CredentialPublicID(credential.Ref)
		if err != nil {
			t.Fatal(err)
		}
		f.bindings.Bindings = append(f.bindings.Bindings, core.RouteBinding{SessionID: "installed-" + account,
			Provider: "codex", Mode: "managed", AccountID: id, Revision: 1})
	}
	f.reconcile()
	for index, account := range []string{"a", "b"} {
		route, err := f.client.MintRoute(ctx, core.ProviderCodex, f.refs[account], "installed-"+account, f.bindings.Bindings[index].AccountID, 1)
		if err != nil {
			t.Fatal(err)
		}
		f.routes[account] = route
	}
	models, err := f.client.ListCredentialModels(ctx, f.refs["a"])
	if err != nil || len(models) == 0 {
		t.Fatal("fixture model catalog missing", err)
	}
	f.model = models[0].ID
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.mu.Lock()
				if f.renewBindings {
					_ = f.client.SynchronizeBindings(ctx, f.bindings)
				}
				f.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-joined })
	t.Cleanup(func() {
		f.safeOutput(f.runnerLog.String())
		for name, original := range f.originals {
			current, err := os.ReadFile(filepath.Join(f.home, name))
			if err != nil || !bytes.Equal(current, original) {
				t.Error("managed requests changed the native credential profile", name)
			}
		}
	})
	f.nativeStatus()
	return f
}

func (f *installedRouteFixture) write(name, contents string) {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *installedRouteFixture) start() {
	f.t.Helper()
	f.runner = exec.CommandContext(f.ctx, os.Getenv("AO_ROUTE_TEST_RUNNER"), "serve", "--state-dir", filepath.Join(f.root, "state"))
	f.runner.Stdout, f.runner.Stderr = &f.runnerLog, &f.runnerLog
	if err := f.runner.Start(); err != nil {
		f.t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := f.client.ListCredentials(f.ctx); err == nil {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatal("isolated runner did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *installedRouteFixture) stop() {
	if f.runner != nil {
		if f.runner.Process != nil {
			_ = f.runner.Process.Kill()
			_ = f.runner.Wait()
		}
		f.runner = nil
	}
}

func (f *installedRouteFixture) reconcile() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.client.SynchronizeBindings(f.ctx, f.bindings); err != nil {
		f.t.Fatal(err)
	}
}

func (f *installedRouteFixture) upstream(w http.ResponseWriter, r *http.Request) {
	account := ""
	for candidate, key := range f.keys {
		if r.Header.Get("Authorization") == "Bearer "+key {
			account = candidate
		}
	}
	if account == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model"}]}`)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	var selected *installedRouteStream
	for marker, stream := range f.streams {
		encoded, _ := json.Marshal(marker)
		if bytes.Contains(body, encoded) {
			selected = stream
			selected.requests++
			break
		}
	}
	f.mu.Unlock()
	if selected == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	selected.once.Do(func() { selected.entered <- account })
	if selected.account != account {
		w.WriteHeader(http.StatusUnauthorized)
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
	case <-r.Context().Done():
		return
	case <-selected.release:
	}
	item := map[string]any{"id": "message-" + account, "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": "account-" + account, "annotations": []any{}}}}
	emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
	response["status"], response["output"] = "completed", []any{item}
	response["usage"] = map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}}
	emit(map[string]any{"type": "response.completed", "response": response})
}

func (f *installedRouteFixture) stream(marker, account string) *installedRouteStream {
	f.t.Helper()
	stream := &installedRouteStream{account: account, entered: make(chan string, 1), release: make(chan struct{})}
	f.mu.Lock()
	f.streams[marker] = stream
	f.mu.Unlock()
	return stream
}

type installedRouteProcess struct {
	output installedRouteOutput
	events installedRouteOutput
	done   chan struct{}
	err    error
}

func (f *installedRouteFixture) launch(marker, account string) *installedRouteProcess {
	f.t.Helper()
	route := f.routes[account]
	command, err := (&Plugin{resolvedBinary: os.Getenv("AO_ROUTE_TEST_BINARY")}).GetLaunchCommand(f.ctx,
		ports.LaunchConfig{WorkspacePath: f.root, Config: ports.AgentConfig{Model: f.model},
			Route: &ports.AgentProviderRoute{BaseURL: route.BaseURL, TokenEnv: "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"}})
	if err != nil {
		f.t.Fatal(err)
	}
	args := append([]string{"exec", "--json", "--ephemeral", "--skip-git-repo-check"}, command[1:]...)
	for _, hook := range []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop"} {
		args = append(args, "-c", "hooks."+hook+"=[]")
	}
	args = append(args, "--", marker)
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	cmd := exec.CommandContext(ctx, command[0], args...)
	cmd.Dir = f.root
	cmd.Env = append(f.clientEnvironment(), "AO_ACCOUNTS_MANAGER_SESSION_TOKEN="+route.Token,
		"OPENAI_API_KEY=test-native-env-secret", "CODEX_API_KEY=test-exec-env-secret")
	cmd.WaitDelay = time.Second
	process := &installedRouteProcess{done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = &process.events, &process.output
	if err := cmd.Start(); err != nil {
		cancel()
		f.t.Fatal(err)
	}
	go func() { process.err = cmd.Wait(); close(process.done) }()
	f.t.Cleanup(func() { cancel(); <-process.done; f.safeOutput(process.text()) })
	return process
}

func (p *installedRouteProcess) text() string {
	return p.events.String() + p.output.String()
}

func (f *installedRouteFixture) completed(process *installedRouteProcess, account string) (bool, bool) {
	f.t.Helper()
	decoder := json.NewDecoder(strings.NewReader(process.events.String()))
	completed, message := false, false
	for {
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return completed, message
		} else if err != nil {
			f.t.Fatal("installed client emitted invalid JSON events")
		}
		completed = completed || event.Type == "turn.completed"
		message = message || event.Type == "item.completed" && event.Item.Type == "agent_message" && event.Item.Text == "account-"+account
	}
}

func (f *installedRouteFixture) clientEnvironment() []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + f.root, "CODEX_HOME=" + f.home,
		"XDG_CONFIG_HOME=" + f.root, "XDG_CACHE_HOME=" + f.root, "TMPDIR=" + f.root, "SHELL=/bin/sh", "TERM=dumb"}
}

func (f *installedRouteFixture) nativeStatus() {
	f.t.Helper()
	cmd := exec.CommandContext(f.ctx, os.Getenv("AO_ROUTE_TEST_BINARY"), "login", "status")
	cmd.Env = f.clientEnvironment()
	output, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("Logged in")) {
		f.t.Fatal("synthetic native file login no longer usable")
	}
	version := exec.CommandContext(f.ctx, os.Getenv("AO_ROUTE_TEST_BINARY"), "--version")
	version.Env = f.clientEnvironment()
	if result, err := version.Output(); err != nil {
		f.t.Fatal("installed client version unavailable", err)
	} else {
		f.t.Log(strings.TrimSpace(string(result)))
	}
}

func (f *installedRouteFixture) safeOutput(output string) string {
	f.t.Helper()
	secrets := []string{f.keys["a"], f.keys["b"], "test-route-management", "test-route-control", "test-route-client",
		"test-native-file-secret", "test-native-env-secret", "test-exec-env-secret"}
	for _, route := range f.routes {
		secrets = append(secrets, route.Token)
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(output, secret) {
			f.t.Error("process diagnostic exposed a synthetic credential")
			output = strings.ReplaceAll(output, secret, "[redacted]")
		}
	}
	return output
}

func (f *installedRouteFixture) awaitIdentity(stream *installedRouteStream, process *installedRouteProcess, expected string) {
	f.t.Helper()
	select {
	case identity := <-stream.entered:
		if identity != expected {
			f.t.Fatal("installed process used the wrong account", identity)
		}
	case <-process.done:
		f.t.Fatalf("client exited before overlap: %v\n%s", process.err, f.safeOutput(process.text()))
	case <-time.After(10 * time.Second):
		f.t.Fatalf("upstream did not observe account %s\n%s", expected, f.safeOutput(process.text()))
	}
}

func (f *installedRouteFixture) awaitComplete(process *installedRouteProcess, account string) {
	f.t.Helper()
	<-process.done
	output := f.safeOutput(process.text())
	completed, message := f.completed(process, account)
	if process.err != nil || !message || !completed {
		f.t.Fatalf("account %s did not complete: %v\n%s", account, process.err, output)
	}
}

func (f *installedRouteFixture) awaitDenied(process *installedRouteProcess, stream *installedRouteStream, wantCalls int) {
	f.t.Helper()
	<-process.done
	f.mu.Lock()
	calls := stream.requests
	f.mu.Unlock()
	output := f.safeOutput(process.text())
	completed, _ := f.completed(process, "")
	if process.err == nil || completed || calls != wantCalls {
		f.t.Fatalf("denied process completed or used a fallback: error=%t upstreamCalls=%d want=%d\n%s", process.err != nil, calls, wantCalls, output)
	}
}

func testInstalledRouteOverlap(t *testing.T) {
	f := newInstalledRouteFixture(t)
	a, b := f.stream("route-check-a", "a"), f.stream("route-check-b", "b")
	processA, processB := f.launch("route-check-a", "a"), f.launch("route-check-b", "b")
	f.awaitIdentity(a, processA, "a")
	f.awaitIdentity(b, processB, "b")
	close(a.release)
	close(b.release)
	f.awaitComplete(processA, "a")
	f.awaitComplete(processB, "b")
	f.nativeStatus()
}

func testInstalledRouteWrongAccount(t *testing.T) {
	f := newInstalledRouteFixture(t)
	stream := f.stream("wrong-account-oracle", "a")
	process := f.launch("wrong-account-oracle", "b")
	f.awaitIdentity(stream, process, "b")
	f.awaitDenied(process, stream, 1)
	f.nativeStatus()
}

func testInstalledRouteSharedAccount(t *testing.T) {
	f := newInstalledRouteFixture(t)
	f.mu.Lock()
	f.bindings.Revision++
	shared := core.RouteBinding{SessionID: "installed-c", Provider: "codex", Mode: "managed",
		AccountID: f.bindings.Bindings[0].AccountID, Revision: f.bindings.Revision}
	f.bindings.Bindings = append(f.bindings.Bindings, shared)
	f.mu.Unlock()
	f.reconcile()
	route, err := f.client.MintRoute(f.ctx, core.ProviderCodex, f.refs["a"], shared.SessionID, shared.AccountID, shared.Revision)
	if err != nil {
		t.Fatal(err)
	}
	f.routes["c"] = route
	a, c := f.stream("shared-account-a", "a"), f.stream("shared-account-c", "a")
	processA, processC := f.launch("shared-account-a", "a"), f.launch("shared-account-c", "c")
	f.awaitIdentity(a, processA, "a")
	f.awaitIdentity(c, processC, "a")
	close(a.release)
	close(c.release)
	f.awaitComplete(processA, "a")
	f.awaitComplete(processC, "a")
	f.nativeStatus()
}

func testInstalledRouteDisable(t *testing.T) {
	f := newInstalledRouteFixture(t)
	b := f.stream("surviving-b", "b")
	processB := f.launch("surviving-b", "b")
	f.awaitIdentity(b, processB, "b")
	if err := f.client.SetCredentialDisabled(f.ctx, f.refs["a"], true); err != nil {
		t.Fatal(err)
	}
	a := f.stream("disabled-a", "a")
	f.awaitDenied(f.launch("disabled-a", "a"), a, 0)
	select {
	case <-processB.done:
		t.Fatal("disabling A stopped B before its upstream response")
	default:
	}
	close(b.release)
	f.awaitComplete(processB, "b")
	f.nativeStatus()
}

func testInstalledRouteRestart(t *testing.T) {
	f := newInstalledRouteFixture(t)
	f.mu.Lock()
	f.renewBindings = false
	f.mu.Unlock()
	f.stop()
	f.start()
	before := f.stream("before-reconcile", "a")
	f.awaitDenied(f.launch("before-reconcile", "a"), before, 0)
	f.reconcile()
	f.mu.Lock()
	f.renewBindings = true
	f.mu.Unlock()
	a, b := f.stream("after-restart-a", "a"), f.stream("after-restart-b", "b")
	processA, processB := f.launch("after-restart-a", "a"), f.launch("after-restart-b", "b")
	f.awaitIdentity(a, processA, "a")
	f.awaitIdentity(b, processB, "b")
	close(a.release)
	close(b.release)
	f.awaitComplete(processA, "a")
	f.awaitComplete(processB, "b")
	f.mu.Lock()
	f.bindings.Revision++
	f.bindings.Bindings[0].Revision++
	f.mu.Unlock()
	f.reconcile()
	stale := f.stream("stale-revision", "a")
	f.awaitDenied(f.launch("stale-revision", "a"), stale, 0)
	f.nativeStatus()
}
