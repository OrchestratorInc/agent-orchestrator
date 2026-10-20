package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// A workspace-user session's worker environment carries the workspace name
// and the working directory; the protection flag reaches either worker user.
func TestWorkspaceUserBootstrapCarriesLifecycleEnvironment(t *testing.T) {
	t.Parallel()
	fake := newDevEnvCoder(t, nil)
	err, _ := devEnvBootstrap(t, fake, func(client *Client) {
		client.runAsWorkspaceUser = true
		client.workingDirectory = "~/work/{repo}"
		client.protectSecretFiles = true
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	env := fake.lastArchive()["worker.env"]
	for _, want := range []string{
		WorkingDirectoryEnv + "='~/work/{repo}'\n",
		ProtectSecretFilesEnv + "='1'\n",
		workspaceNameEnv + "='" + WorkspaceName("session-1") + "'\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("worker.env missing %q:\n%s", want, env)
		}
	}
}

func TestDefaultBootstrapCarriesOnlySecretFileProtection(t *testing.T) {
	t.Parallel()
	fake := newDevEnvCoder(t, nil)
	err, _ := devEnvBootstrap(t, fake, func(client *Client) {
		client.protectSecretFiles = true
		// Settings that need the workspace user are never sent without it.
		client.workingDirectory = "~/work/{repo}"
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	env := fake.lastArchive()["worker.env"]
	if !strings.Contains(env, ProtectSecretFilesEnv+"='1'\n") {
		t.Fatalf("worker.env lacks the protection flag:\n%s", env)
	}
	for _, unwanted := range []string{WorkingDirectoryEnv, workspaceNameEnv} {
		if strings.Contains(env, unwanted) {
			t.Errorf("ao-worker worker.env contains %s:\n%s", unwanted, env)
		}
	}

	plain := newDevEnvCoder(t, nil)
	if err, _ := devEnvBootstrap(t, plain, func(*Client) {}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if env := plain.lastArchive()["worker.env"]; strings.Contains(env, ProtectSecretFilesEnv) {
		t.Fatalf("an AO-managed session got the protection flag:\n%s", env)
	}
}

func TestForSandboxCarriesLifecycleSettings(t *testing.T) {
	t.Parallel()
	base := newTestClient(t, "https://coder.example.com", nil)
	profile := func(extra string) json.RawMessage {
		return json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"o","templateId":"` + testTemplateID +
			`","durableRoot":"$HOME"` + extra + `}}`)
	}
	provider, err := base.ForSandbox(domain.Sandbox{SessionID: "session-1", ResourceProfile: profile(
		`,"workerUser":"workspace","workingDirectory":"~/work/{repo}","protectSecretFiles":true`)})
	if err != nil {
		t.Fatal(err)
	}
	client := provider.(*Client)
	if client.workingDirectory != "~/work/{repo}" || !client.protectSecretFiles ||
		client.durableRoot != sandbox.CoderHomeDurableRoot {
		t.Fatalf("session client = %+v", client)
	}
	// A profile written before the fields existed keeps the historical
	// behavior, and an ao-worker profile never carries a working directory.
	for _, extra := range []string{``, `,"workingDirectory":"~/x"`} {
		provider, err := base.ForSandbox(domain.Sandbox{SessionID: "session-1", ResourceProfile: profile(extra)})
		if err != nil {
			t.Fatal(err)
		}
		if client := provider.(*Client); client.workingDirectory != "" || client.protectSecretFiles {
			t.Fatalf("profile %q gave session client %+v", extra, client)
		}
	}
}

// cleanupCoder answers the Cleanup hook command with a fixed result, after
// release is closed, and records the workspace transitions.
type cleanupCoder struct {
	*fakeByoCoder
	status  string
	output  string
	release chan struct{}

	mu          sync.Mutex
	transitions []string
	hookRuns    int
	hookCommand string
}

func (f *cleanupCoder) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID+"/builds" && request.Method == http.MethodPost:
		var body map[string]string
		_ = json.NewDecoder(request.Body).Decode(&body)
		f.mu.Lock()
		f.transitions = append(f.transitions, body["transition"])
		f.mu.Unlock()
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{}`))
	case request.URL.Path == "/api/v2/workspaceagents/"+testAgentID+"/pty" &&
		strings.Contains(request.URL.Query().Get("command"), cleanupMarker):
		f.mu.Lock()
		f.hookRuns++
		f.hookCommand = request.URL.Query().Get("command")
		f.mu.Unlock()
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			f.t.Errorf("accept websocket: %v", err)
			return
		}
		defer connection.CloseNow()
		<-f.release
		output := websocket.NetConn(context.Background(), connection, websocket.MessageBinary)
		defer output.Close()
		_, _ = fmt.Fprintf(output, "%s\r\n\r\n%s:%s\r\n", f.output, cleanupMarker, f.status)
	default:
		f.fakeByoCoder.ServeHTTP(writer, request)
	}
}

func (f *cleanupCoder) snapshot() (runs int, transitions []string, command string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hookRuns, append([]string(nil), f.transitions...), f.hookCommand
}

func newCleanupCoder(t *testing.T, buildStatus, status, output string) (*cleanupCoder, *Client) {
	t.Helper()
	view := byoWorkspace("ready", "amd64")
	view.LatestBuild.Status = buildStatus
	view.LatestBuild.ID = uuid.NewString()
	fake := &cleanupCoder{
		fakeByoCoder: &fakeByoCoder{t: t, view: view, machine: "x86_64", home: "/home/ubuntu"},
		status:       status, output: output, release: make(chan struct{}),
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL, nil)
	client.durableRoot = sandbox.CoderHomeDurableRoot
	client.cleanupWait = 5 * time.Second
	client.cleanupInline = 50 * time.Millisecond
	return fake, client
}

// waitFor polls until ready reports true or fails the test.
func waitFor(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Delete of a running workspace runs the project's Cleanup in the background
// without holding the caller, defers the delete until it finishes, runs it
// only once for the build, and never lets a failure block the delete.
func TestDeleteRunsProjectCleanupOnceInTheBackground(t *testing.T) {
	t.Parallel()
	fake, client := newCleanupCoder(t, "running", "7", "compose: no such service")
	stages := &stageRecorder{}
	ctx := sandbox.WithStageObserver(context.Background(), stages.observe)
	if err := client.Delete(ctx, testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	waitFor(t, "the cleanup hook to start", func() bool { runs, _, _ := fake.snapshot(); return runs == 1 })
	if err := client.Delete(ctx, testWorkspaceID); err != nil {
		t.Fatalf("re-issued delete: %v", err)
	}
	if _, transitions, _ := fake.snapshot(); len(transitions) != 0 {
		t.Fatalf("deleted while cleanup ran: %v", transitions)
	}
	close(fake.release)
	waitFor(t, "the cleanup stage", func() bool { _, ok := stages.stage(StageCleanup); return ok })
	if err := client.Delete(ctx, testWorkspaceID); err != nil {
		t.Fatalf("delete after cleanup: %v", err)
	}
	runs, transitions, command := fake.snapshot()
	if runs != 1 || len(transitions) != 1 || transitions[0] != "delete" {
		t.Fatalf("hook runs = %d, transitions = %v", runs, transitions)
	}
	stage, _ := stages.stage(StageCleanup)
	if stage.Err == nil || !strings.Contains(stage.Attrs["output"], "no such service") {
		t.Fatalf("cleanup stage = %+v", stage)
	}
	if !strings.Contains(command, `"$HOME"/.ao/worker/`+cleanupHookFile) {
		t.Fatalf("hook command = %q", command)
	}
}

// A Cleanup that finishes within the inline wait deletes the workspace in the
// same Delete call. Staging found the second call never came: the worker keeps
// heartbeating until its compute is gone, and each heartbeat pushed the
// sandbox's next reconcile 30s out, so a deletion waiting for another tick was
// never reconciled again.
func TestDeleteAfterAQuickCleanupDeletesInTheSameCall(t *testing.T) {
	t.Parallel()
	fake, client := newCleanupCoder(t, "running", "0", "stack down")
	client.cleanupInline = 5 * time.Second
	close(fake.release)
	if err := client.Delete(context.Background(), testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	runs, transitions, _ := fake.snapshot()
	if runs != 1 || len(transitions) != 1 || transitions[0] != "delete" {
		t.Fatalf("hook runs = %d, transitions = %v; want the cleanup then the delete in one call", runs, transitions)
	}
}

// A Cleanup run that never reports cannot hold a deletion past its own limit.
func TestDeleteProceedsWhenCleanupNeverReports(t *testing.T) {
	t.Parallel()
	fake, client := newCleanupCoder(t, "running", "0", "")
	defer close(fake.release)
	key := string(testWorkspaceID) + "/" + fake.view.LatestBuild.ID
	limit := time.Minute
	cleanupRuns.Store(key, &cleanupRun{
		done:    make(chan struct{}),
		started: time.Now().Add(-(limit + cleanupStaleAfter + time.Second)),
		limit:   limit,
	})
	t.Cleanup(func() { cleanupRuns.Delete(key) })
	if err := client.Delete(context.Background(), testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, transitions, _ := fake.snapshot(); len(transitions) != 1 || transitions[0] != "delete" {
		t.Fatalf("transitions = %v; a wedged cleanup run blocked the delete", transitions)
	}
}

// Stop and pause never run Cleanup; a stopped workspace is deleted at once.
func TestCleanupRunsOnlyBeforeDeletingARunningWorkspace(t *testing.T) {
	t.Parallel()
	fake, client := newCleanupCoder(t, "running", "0", "")
	close(fake.release)
	if err := client.Stop(context.Background(), testWorkspaceID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := client.Pause(context.Background(), testWorkspaceID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if runs, transitions, _ := fake.snapshot(); runs != 0 || len(transitions) != 2 {
		t.Fatalf("stop/pause ran cleanup: runs = %d, transitions = %v", runs, transitions)
	}

	stopped, stoppedClient := newCleanupCoder(t, "stopped", "0", "")
	close(stopped.release)
	if err := stoppedClient.Delete(context.Background(), testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if runs, transitions, _ := stopped.snapshot(); runs != 0 || len(transitions) != 1 {
		t.Fatalf("a stopped workspace ran cleanup: runs = %d, transitions = %v", runs, transitions)
	}

	// A project without Cleanup commands (no hook) is reported as skipped.
	skipped, skippedClient := newCleanupCoder(t, "running", cleanupSkipped, "")
	close(skipped.release)
	stages := &stageRecorder{}
	ctx := sandbox.WithStageObserver(context.Background(), stages.observe)
	_ = skippedClient.Delete(ctx, testWorkspaceID)
	waitFor(t, "the skipped stage", func() bool { _, ok := stages.stage(StageCleanup); return ok })
	if stage, _ := stages.stage(StageCleanup); stage.Err != nil || stage.Attrs["result"] != cleanupSkipped {
		t.Fatalf("skipped stage = %+v", stage)
	}
}

// The hook command really runs the worker's hook and reports its status, or
// reports it skipped when the worker never installed one.
func TestCleanupHookCommandRunsTheInstalledHook(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	home := t.TempDir()
	run := func() string {
		command := exec.Command("sh", "-c", cleanupHookCommand(sandbox.CoderHomeDurableRoot, 10*time.Second, ""))
		command.Env = append(os.Environ(), "HOME="+home)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("hook command: %v: %s", err, output)
		}
		return string(output)
	}
	if output := run(); !strings.Contains(output, cleanupMarker+":"+cleanupSkipped) {
		t.Fatalf("no hook: %q", output)
	}
	hook := filepath.Join(home, ".ao", "worker", cleanupHookFile)
	if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho stopping stack\nexit 4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if output := run(); !strings.Contains(output, "stopping stack") || !strings.Contains(output, cleanupMarker+":4") {
		t.Fatalf("hook output: %q", output)
	}
	if command := cleanupHookCommand("/srv/ao", time.Second, ""); !strings.Contains(command, "'/srv/ao/.ao/worker/"+cleanupHookFile+"'") {
		t.Fatalf("absolute root command = %q", command)
	}
}

// Delete runs the project's Cleanup as it is when the workspace is deleted:
// the current commands travel with the hook command, an empty list runs
// nothing, and without a lookup the commands the worker installed run.
func TestDeleteUsesTheProjectsCurrentCleanup(t *testing.T) {
	t.Parallel()
	current, currentClient := newCleanupCoder(t, "running", "0", "")
	close(current.release)
	ctx := sandbox.WithCleanupCommands(context.Background(), []string{"docker compose -p current down"})
	if err := currentClient.Delete(ctx, testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	waitFor(t, "the cleanup hook", func() bool { runs, _, _ := current.snapshot(); return runs == 1 })
	_, _, command := current.snapshot()
	payload, fits := cleanupCommandsPayload([]string{"docker compose -p current down"})
	if !fits || !strings.Contains(command, payload) {
		t.Fatalf("hook command does not carry the current Cleanup: %q", command)
	}

	empty, emptyClient := newCleanupCoder(t, "running", "0", "")
	close(empty.release)
	if err := emptyClient.Delete(sandbox.WithCleanupCommands(context.Background(), nil), testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if runs, transitions, _ := empty.snapshot(); runs != 0 || len(transitions) != 1 || transitions[0] != "delete" {
		t.Fatalf("a project without Cleanup ran the hook: runs = %d, transitions = %v", runs, transitions)
	}

	installed, installedClient := newCleanupCoder(t, "running", "0", "")
	close(installed.release)
	if err := installedClient.Delete(context.Background(), testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	waitFor(t, "the cleanup hook", func() bool { runs, _, _ := installed.snapshot(); return runs == 1 })
	if _, _, command := installed.snapshot(); strings.Contains(command, "ao_payload=") {
		t.Fatalf("without a lookup the hook command must run the installed commands: %q", command)
	}
}

// The hook command replaces the installed Cleanup commands with the current
// ones and runs them through the worker's hook, in the session's directory
// with its environment; Cleanup too large to pass falls back to the installed
// commands.
func TestCleanupHookCommandRunsTheCurrentCommands(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"sh", "base64", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is unavailable")
		}
	}
	home, workspace := t.TempDir(), t.TempDir()
	dataDir := filepath.Join(home, ".ao", "worker")
	environment := worker.SessionScriptEnvironment{SessionID: "session-1", WorkspaceDir: workspace}
	if err := worker.WriteCleanupHook(dataDir, []string{"echo installed-cleanup"}, workspace, environment); err != nil {
		t.Fatal(err)
	}
	payload, fits := cleanupCommandsPayload([]string{`echo "current-cleanup $AO_SESSION_ID $(basename "$PWD")"`})
	if !fits {
		t.Fatal("a one-line Cleanup does not fit the payload limit")
	}
	command := exec.Command("sh", "-c", cleanupHookCommand(sandbox.CoderHomeDurableRoot, 10*time.Second, payload))
	command.Env = append(os.Environ(), "HOME="+home)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hook command: %v: %s", err, output)
	}
	want := "current-cleanup session-1 " + filepath.Base(workspace)
	if !strings.Contains(string(output), want) || strings.Contains(string(output), "installed-cleanup") ||
		!strings.Contains(string(output), cleanupMarker+":0") {
		t.Fatalf("hook output = %q, want the current Cleanup (%q)", output, want)
	}

	huge := make([]string, 20)
	for index := range huge {
		var step strings.Builder
		step.WriteString("echo " + strconv.Itoa(index))
		for step.Len() < 4<<10-40 {
			step.WriteString(" " + uuid.NewString())
		}
		huge[index] = step.String()
	}
	if _, fits := cleanupCommandsPayload(huge); fits {
		t.Fatal("80 KiB of random Cleanup fits the URL payload limit")
	}
}

// The reconciler's last Delete before it gives up skips Cleanup without
// asking Coder about the workspace.
func TestCleanupSkippedDeletesAtOnce(t *testing.T) {
	t.Parallel()
	c := &Client{baseURL: "http://127.0.0.1:1", durableRoot: sandbox.CoderHomeDurableRoot}
	if !c.cleanupFinished(sandbox.WithoutCleanup(context.Background()), "workspace-1") {
		t.Fatal("a Delete that skips Cleanup waited for it")
	}
}
