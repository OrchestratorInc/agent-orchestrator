package coder

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook/cleanuphooktest"
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
	// deleteBuilds makes a delete transition start a pending delete build,
	// as Coder does, which the workspace then reports.
	deleteBuilds bool
}

// setBuild replaces the workspace's latest build status and transition.
func (f *cleanupCoder) setBuild(status, transition string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.view.LatestBuild.ID = uuid.NewString()
	f.view.LatestBuild.Status = status
	f.view.LatestBuild.Transition = transition
}

func (f *cleanupCoder) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID && request.Method == http.MethodGet:
		f.mu.Lock()
		view := f.view
		f.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(view)
	case request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID+"/builds" && request.Method == http.MethodPost:
		var body map[string]string
		_ = json.NewDecoder(request.Body).Decode(&body)
		f.mu.Lock()
		f.transitions = append(f.transitions, body["transition"])
		deleteBuilds := f.deleteBuilds
		f.mu.Unlock()
		if deleteBuilds && body["transition"] == "delete" {
			f.setBuild("pending", "delete")
		}
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
		// "{nonce}" in the output stands for the marker nonce of the Cleanup
		// the hook command carries, as its commands script would print it.
		printed := strings.ReplaceAll(f.output, "{nonce}", cleanupScriptNonce(f.t, request.URL.Query().Get("command")))
		_, _ = fmt.Fprintf(output, "%s\r\n\r\n%s:%s\r\n", strings.ReplaceAll(printed, "\n", "\r\n"), cleanupMarker, f.status)
	default:
		f.fakeByoCoder.ServeHTTP(writer, request)
	}
}

var cleanupPayloadPattern = regexp.MustCompile(`ao_payload=\W*([A-Za-z0-9+/=]{16,})`)

// cleanupScript decodes the Cleanup commands script a hook command carries;
// "" when it carries none.
func cleanupScript(t *testing.T, command string) string {
	t.Helper()
	match := cleanupPayloadPattern.FindStringSubmatch(command)
	if match == nil {
		return ""
	}
	compressed, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gunzip payload: %v", err)
	}
	script, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gunzip payload: %v", err)
	}
	return string(script)
}

var cleanupNoncePattern = regexp.MustCompile(`@@ao-step-([0-9a-f]{16}) `)

// cleanupScriptNonce is the marker nonce of the Cleanup a hook command
// carries; "" when it carries none.
func cleanupScriptNonce(t *testing.T, command string) string {
	t.Helper()
	if match := cleanupNoncePattern.FindStringSubmatch(cleanupScript(t, command)); match != nil {
		return match[1]
	}
	return ""
}

// eventRecorder collects the events a provider appends to the session.
type eventRecorder struct {
	mu     sync.Mutex
	events []worker.ScriptStepEvent
}

func (r *eventRecorder) record(_ context.Context, eventType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event, ok := payload.(worker.ScriptStepEvent); ok && eventType == worker.ScriptStepEventType {
		r.events = append(r.events, event)
	}
}

func (r *eventRecorder) all() []worker.ScriptStepEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]worker.ScriptStepEvent(nil), r.events...)
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
	client.cleanupTracker = &cleanuphook.Tracker{}
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
	// A run whose limit is long past: it never reports.
	wedged := make(chan struct{})
	defer close(wedged)
	client.runs().Start(context.Background(), key, "", 0, -time.Hour, func() { <-wedged }, func(string) {})
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
		command := exec.Command("sh", "-c", cleanupHookCommand(sandbox.CoderHomeDurableRoot, 10*time.Second, "", ""))
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
	if command := cleanupHookCommand("/srv/ao", time.Second, "", dedicatedWorkerUser); !strings.Contains(command, "'/srv/ao/.ao/worker/"+cleanupHookFile+"'") {
		t.Fatalf("absolute root command = %q", command)
	}
	// The login user may run the Coder agent carrying the command: Coder's
	// Cleanup stops none of its processes.
	if command := cleanupHookCommand("/srv/ao", time.Second, "", dedicatedWorkerUser); strings.Contains(command, "ao_stop") {
		t.Fatalf("coder command stops processes: %q", command)
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
	if script := cleanupScript(t, command); !strings.Contains(script, "sh -c 'docker compose -p current down'") ||
		cleanupScriptNonce(t, command) == "" {
		t.Fatalf("hook command does not carry the current Cleanup with its markers: %q", script)
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
	current := []string{`echo "current-cleanup $AO_SESSION_ID $(basename "$PWD")"`}
	nonce := worker.NewScriptMarkerNonce()
	payload, fits := cleanupCommandsPayload(current, nonce)
	if !fits {
		t.Fatal("a one-line Cleanup does not fit the payload limit")
	}
	command := exec.Command("sh", "-c", cleanupHookCommand(sandbox.CoderHomeDurableRoot, 10*time.Second, payload, ""))
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
	// What the hook command printed before its status splits into the run's
	// steps, read with the run's own nonce.
	printed, _, _ := strings.Cut(string(output), cleanupMarker+":")
	events := worker.CleanupStepEvents(printed, worker.CleanupRun{RunID: "r", Nonce: nonce, Commands: current})
	if len(events) != 1 || events[0].Status != worker.SetupSucceeded || events[0].Output != want ||
		events[0].Command != current[0] || !strings.HasSuffix(events[0].LogPath, "cleanup-state/output") {
		t.Fatalf("events = %+v", events)
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
	if _, fits := cleanupCommandsPayload(huge, worker.NewScriptMarkerNonce()); fits {
		t.Fatal("80 KiB of random Cleanup fits the URL payload limit")
	}
}

// A Cleanup run's steps are appended to the session: split by the markers of
// the run's own Cleanup, or of the installed one, or (an older commands
// script) as one step. Nothing is appended when no hook ran.
func TestDeleteRecordsCleanupStepsOnTheSession(t *testing.T) {
	t.Parallel()
	run := func(status, output string, commands []string) []worker.ScriptStepEvent {
		t.Helper()
		fake, client := newCleanupCoder(t, "running", status, output)
		client.cleanupInline = 5 * time.Second
		close(fake.release)
		recorder := &eventRecorder{}
		ctx := sandbox.WithSessionEventRecorder(context.Background(), recorder.record)
		if commands != nil {
			ctx = sandbox.WithCleanupCommands(ctx, commands)
		}
		if err := client.Delete(ctx, testWorkspaceID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		return recorder.all()
	}

	current := []string{"make down", "docker compose down", "rm -rf tmp"}
	events := run("3", "@@ao-step-{nonce} log /home/ubuntu/.ao/worker/cleanup-state/output\n"+
		"@@ao-step-{nonce} 1 3 start 1792344586000 / make down\nstopped\n@@ao-step-{nonce} 1 3 end 0 120\n"+
		"@@ao-step-{nonce} 2 3 start 1792344586120 / docker compose down\n"+
		"@@ao-step-0123456789abcdef 2 3 end 0 1\nGITHUB_TOKEN=gh"+"p_abcdefghijklmnopqrstuvwxyz0123456789\n"+
		"cleanup step 2 failed: exit status 3\nran as ubuntu (groups: ubuntu) in /home/ubuntu/app\n"+
		"@@ao-step-{nonce} 2 3 end 3 900\nfull cleanup output: /home/ubuntu/.ao/worker/cleanup-state/output", current)
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	runID := events[0].RunID
	for index, event := range events {
		if !worker.ValidScriptStepEvent(event) || event.Phase != worker.ScriptPhaseCleanup || event.RunID != runID ||
			event.Step != index+1 || event.Total != 3 || event.Command != current[index] {
			t.Fatalf("event %d = %+v", index, event)
		}
	}
	if events[0].Status != worker.SetupSucceeded || events[0].Output != "stopped" || *events[0].DurationMS != 120 {
		t.Fatalf("step 1 = %+v", events[0])
	}
	failed := events[1]
	if failed.Status != worker.SetupFailed || *failed.ExitCode != 3 || failed.User != "ubuntu" ||
		!strings.Contains(failed.Output, "@@ao-step-0123456789abcdef 2 3 end 0 1") ||
		!strings.Contains(failed.Output, "GITHUB_TOKEN=[REDACTED]") || strings.Contains(failed.Output, "ghp_") {
		t.Fatalf("step 2 = %+v", failed)
	}
	if events[2].Status != worker.ScriptStepSkipped {
		t.Fatalf("step 3 = %+v", events[2])
	}

	// The installed commands script (no current Cleanup read) carries its
	// own nonce, read from the output.
	installed := run("0", "@@ao-step-00000000000000aa 1 1 start 0 api make clean\ncleaned\n@@ao-step-00000000000000aa 1 1 end 0 5", nil)
	if len(installed) != 1 || installed[0].Status != worker.SetupSucceeded || installed[0].Repository != "api" ||
		installed[0].Command != "make clean" || installed[0].Output != "cleaned" || installed[0].StartedAt != "" {
		t.Fatalf("installed script events = %+v", installed)
	}

	// An older commands script without markers is one step.
	older := run("4", "tearing down\ncleanup step 1 failed: exit status 4", nil)
	// It does not say whom it ran as: the worker account the hook runs as.
	if len(older) != 1 || older[0].Status != worker.SetupFailed || *older[0].ExitCode != 4 ||
		!strings.HasPrefix(older[0].Output, "tearing down") || older[0].Total != 1 || older[0].User != dedicatedWorkerUser {
		t.Fatalf("older script events = %+v", older)
	}

	if skipped := run(cleanupSkipped, "", nil); len(skipped) != 0 {
		t.Fatalf("a run without a hook appended %+v", skipped)
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

// A Cleanup that does not run is never silent: the hook command's reason (here
// the login user had no passwordless sudo) and a workspace that is not
// running both mark each of the project's Cleanup steps skipped with a reason
// on the session, once.
func TestDeleteShowsASkippedCleanupOnEveryStep(t *testing.T) {
	t.Parallel()
	commands := []string{"docker compose down", "echo done"}
	for name, test := range map[string]struct {
		build, status, reason string
	}{
		"hook skipped": {build: "running", status: cleanupSkipped + " no_sudo", reason: "sudo"},
		"stopped":      {build: "stopped", status: "0", reason: "not running"},
	} {
		fake, client := newCleanupCoder(t, test.build, test.status, "")
		close(fake.release)
		recorder := &cleanuphooktest.Recorder{}
		ctx := sandbox.WithCleanupCommands(recorder.Context(context.Background()), commands)
		if err := client.Delete(ctx, testWorkspaceID); err != nil {
			t.Fatalf("%s: delete: %v", name, err)
		}
		stage := recorder.WaitStage(t)
		if stage.Err != nil || stage.Attrs["result"] != cleanupSkipped {
			t.Fatalf("%s: stage = %+v", name, stage)
		}
		// A re-issued Delete reports nothing again.
		_ = client.Delete(ctx, testWorkspaceID)
		events := recorder.Events(t)
		if len(events) != len(commands) {
			t.Fatalf("%s: events = %+v", name, events)
		}
		for index, event := range events {
			if event.Status != worker.ScriptStepSkipped || event.Phase != worker.ScriptPhaseCleanup ||
				event.Command != commands[index] || !strings.Contains(event.Hint, test.reason) {
				t.Fatalf("%s: step %d = %+v", name, index+1, event)
			}
		}
	}
}

// A re-issued Delete after the delete build started (pending, then failed)
// reports no second Cleanup: not a false "not running" run after a Cleanup
// that ran, nor a second one for a stopped workspace.
func TestReissuedDeleteDuringTheDeleteBuildReportsNoSecondCleanup(t *testing.T) {
	t.Parallel()
	commands := []string{"make down"}
	ran := "@@ao-step-{nonce} 1 1 start 0 / make down\nstopped\n@@ao-step-{nonce} 1 1 end 0 5\n"
	for name, build := range map[string]string{"ran": "running", "stopped": "stopped"} {
		fake, client := newCleanupCoder(t, build, "0", ran)
		fake.deleteBuilds = true
		close(fake.release)
		client.cleanupInline = 5 * time.Second
		recorder := &cleanuphooktest.Recorder{}
		ctx := sandbox.WithCleanupCommands(recorder.Context(context.Background()), commands)
		if err := client.Delete(ctx, testWorkspaceID); err != nil {
			t.Fatalf("%s: delete: %v", name, err)
		}
		recorder.WaitStage(t)
		before := len(recorder.Events(t))
		_ = client.Delete(ctx, testWorkspaceID) // the delete build is pending
		fake.setBuild(buildStatusFailed, "delete")
		_ = client.Delete(ctx, testWorkspaceID) // the delete build failed
		events := recorder.Events(t)
		if len(events) != before || before != len(commands) {
			t.Fatalf("%s: events %d before the delete build, %d after: %+v", name, before, len(events), events)
		}
		if runs, _, _ := fake.snapshot(); (name == "ran") != (runs == 1) || runs > 1 {
			t.Fatalf("%s: hook runs = %d", name, runs)
		}
	}
}

// A Delete while the workspace is still starting (a resume) neither deletes
// it nor reports its Cleanup as not run: a later Delete, once it runs, runs
// the Cleanup.
func TestDeleteOfAStartingWorkspaceRunsCleanupOnceItRuns(t *testing.T) {
	t.Parallel()
	fake, client := newCleanupCoder(t, "starting", "0", "")
	close(fake.release)
	client.cleanupInline = 5 * time.Second
	recorder := &cleanuphooktest.Recorder{}
	ctx := sandbox.WithCleanupCommands(recorder.Context(context.Background()), []string{"make down"})
	if err := client.Delete(ctx, testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if runs, transitions, _ := fake.snapshot(); runs != 0 || len(transitions) != 0 {
		t.Fatalf("a starting workspace: hook runs = %d, transitions = %v", runs, transitions)
	}
	if _, ok := recorder.Stage(); ok {
		t.Fatal("a starting workspace reported its Cleanup")
	}
	fake.setBuild("running", "start")
	if err := client.Delete(ctx, testWorkspaceID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if runs, transitions, _ := fake.snapshot(); runs != 1 || len(transitions) != 1 || transitions[0] != "delete" {
		t.Fatalf("once running: hook runs = %d, transitions = %v", runs, transitions)
	}
}
