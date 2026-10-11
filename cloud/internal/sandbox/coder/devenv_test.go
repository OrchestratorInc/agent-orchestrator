package coder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/coder/websocket"
)

// devEnvCoder is a Coder deployment for the dev-environment options: its
// workspace probe also reports the login user, its ready check answers from
// readyResults (the last entry repeats), and anything else is a bootstrap
// upload captured by the embedded fakeByoCoder.
type devEnvCoder struct {
	*fakeByoCoder
	user         string
	readyResults []readyProbeResult

	readyMu    sync.Mutex
	readyCalls int
}

func (f *devEnvCoder) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/api/v2/workspaceagents/"+testAgentID+"/pty" {
		f.fakeByoCoder.ServeHTTP(writer, request)
		return
	}
	command := request.URL.Query().Get("command")
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.mu.Unlock()
	connection, err := websocket.Accept(writer, request, nil)
	if err != nil {
		f.t.Errorf("accept websocket: %v", err)
		return
	}
	defer connection.CloseNow()
	output := websocket.NetConn(context.Background(), connection, websocket.MessageBinary)
	defer output.Close()
	switch {
	case strings.Contains(command, workspaceProbe):
		if strings.Contains(command, "id -un") {
			_, _ = fmt.Fprintf(output, "%s:%s:%s:%s\r\n", workspaceProbe, f.user, f.machine, f.home)
		} else {
			_, _ = fmt.Fprintf(output, "%s:%s:%s\r\n", workspaceProbe, f.machine, f.home)
		}
	case strings.Contains(command, readyProbeMarker):
		f.readyMu.Lock()
		result := f.readyResults[min(f.readyCalls, len(f.readyResults)-1)]
		f.readyCalls++
		f.readyMu.Unlock()
		_, _ = fmt.Fprintf(output, "%s\r\n\r\n%s:%d\r\n", result.output, readyProbeMarker, result.exitCode)
	default:
		f.captureUpload(command, output)
	}
}

func (f *devEnvCoder) readyProbeCalls() int {
	f.readyMu.Lock()
	defer f.readyMu.Unlock()
	return f.readyCalls
}

func newDevEnvCoder(t *testing.T, readyAt *time.Time, results ...readyProbeResult) *devEnvCoder {
	view := byoWorkspace("ready", "amd64")
	view.LatestBuild.Resources[0].Agents[0].ReadyAt = readyAt
	if len(results) == 0 {
		results = []readyProbeResult{{exitCode: 0}}
	}
	return &devEnvCoder{
		fakeByoCoder: &fakeByoCoder{t: t, view: view, machine: "x86_64", home: "/home/ubuntu"},
		user:         "ubuntu",
		readyResults: results,
	}
}

type stageRecorder struct {
	mu      sync.Mutex
	reports []sandbox.StageReport
}

func (r *stageRecorder) observe(_ context.Context, report sandbox.StageReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, report)
}

func (r *stageRecorder) stage(name string) (sandbox.StageReport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := len(r.reports) - 1; index >= 0; index-- {
		if r.reports[index].Stage == name {
			return r.reports[index], true
		}
	}
	return sandbox.StageReport{}, false
}

// devEnvBootstrap runs one bootstrap against fake with a client configured by
// configure, returning the error and the stage reports.
func devEnvBootstrap(t *testing.T, fake http.Handler, configure func(*Client)) (error, *stageRecorder) {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL, nil)
	configure(client)
	recorder := &stageRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = sandbox.WithStageObserver(ctx, recorder.observe)
	return client.BootstrapWorker(ctx, testWorkspaceID, multiArchBootstrap(sandbox.CoderHomeDurableRoot)), recorder
}

// The workspace user mode probes the login user, keeps the user's real HOME,
// passes the checkout-reuse hint, and launches without creating an account or
// switching users through sudo.
func TestWorkspaceUserBootstrapRunsAsLoginUser(t *testing.T) {
	t.Parallel()
	fake := newDevEnvCoder(t, nil)
	err, stages := devEnvBootstrap(t, fake, func(client *Client) {
		client.runAsWorkspaceUser = true
		client.reuseCheckout = true
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	commands := fake.recordedCommands()
	if !strings.Contains(commands[0], "id -un") {
		t.Fatalf("probe does not ask for the login user: %s", commands[0])
	}
	env := fake.lastArchive()["worker.env"]
	for _, want := range []string{
		"HOME='/home/ubuntu'\n",
		"AO_WORKSPACE_DIR='/home/ubuntu/repository'\n",
		ReuseExistingCheckoutEnv + "='1'\n",
		"GIT_TERMINAL_PROMPT='0'\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("worker.env missing %q:\n%s", want, env)
		}
	}
	last := commands[len(commands)-1]
	for _, unwanted := range []string{"useradd", "sudo -n -b -u", "sudo -n -u", "chown -R", "usermod -aG docker"} {
		if strings.Contains(last, unwanted) {
			t.Errorf("workspace user bootstrap contains %q", unwanted)
		}
	}
	install, ok := stages.stage(sandbox.StageWorkerInstall)
	if !ok || install.Attrs["worker_user"] != "workspace:ubuntu" || install.Attrs["checkout_reuse"] != "enabled" {
		t.Fatalf("worker install stage = %+v", install)
	}
	if probe, ok := stages.stage(sandbox.StageDurableRootProbe); !ok || probe.Attrs["workspace_user"] != "ubuntu" {
		t.Fatalf("probe stage = %+v", probe)
	}
}

// The default path is unchanged apart from the docker group membership: no
// login-user probe, the AO home as HOME, no reuse hint, and the dedicated
// worker user created and launched through sudo.
func TestDefaultBootstrapKeepsDedicatedWorkerUser(t *testing.T) {
	t.Parallel()
	fake := newDevEnvCoder(t, nil)
	err, stages := devEnvBootstrap(t, fake, func(*Client) {})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	commands := fake.recordedCommands()
	if strings.Contains(commands[0], "id -un") {
		t.Fatalf("default probe asks for the login user: %s", commands[0])
	}
	env := fake.lastArchive()["worker.env"]
	if !strings.Contains(env, "HOME='/home/ubuntu/.ao/home'\n") || strings.Contains(env, ReuseExistingCheckoutEnv) ||
		strings.Contains(env, "GIT_TERMINAL_PROMPT") {
		t.Fatalf("default worker.env:\n%s", env)
	}
	last := commands[len(commands)-1]
	for _, want := range []string{"useradd -m", "usermod -aG docker", "sudo -n -b -u"} {
		if !strings.Contains(last, want) {
			t.Errorf("default bootstrap missing %q", want)
		}
	}
	if install, _ := stages.stage(sandbox.StageWorkerInstall); install.Attrs["worker_user"] != domain.CoderWorkerUserAOWorker ||
		install.Attrs["checkout_reuse"] != "disabled" {
		t.Fatalf("worker install stage = %+v", install)
	}
	if _, ran := stages.stage(sandbox.StageReadyProbe); ran {
		t.Fatal("ready probe ran without a configured check")
	}
}

// unquoteBootstrap recovers the script inside "sh -lc '<script>'".
func unquoteBootstrap(t *testing.T, command string) string {
	t.Helper()
	quoted, found := strings.CutPrefix(command, "sh -lc ")
	if !found || len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
		t.Fatalf("unexpected bootstrap command shape: %.80s", command)
	}
	return strings.ReplaceAll(quoted[1:len(quoted)-1], `'"'"'`, `'`)
}

func shellSyntaxCheck(t *testing.T, script string) {
	t.Helper()
	process := exec.Command("sh", "-n")
	process.Stdin = strings.NewReader(script)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap script does not parse: %v\n%s", err, output)
	}
}

// Golden-ish rendering for both worker users, full and preinstalled: the
// docker group line only for the dedicated user, no account creation and no
// sudo user switch for the workspace user, and every variant parses.
func TestBootstrapScriptRenderingForBothWorkerUsers(t *testing.T) {
	t.Parallel()
	base := sandbox.WorkerBootstrap{
		Binary: []byte("worker"), Destination: "/usr/local/bin/ao-worker",
		HelperBinary: []byte("helper"), HelperDestination: "/usr/local/bin/ao",
		User: "ao-worker", DurableRoot: "/home/ubuntu", DurableIdentity: "session-1",
		Environment: map[string]string{"AO_CLOUD_PUBLIC_URL": "https://ao.example.com"},
	}
	for _, preinstalled := range []bool{false, true} {
		dedicated := unquoteBootstrap(t, bootstrapCommandForArchive(base, sandbox.ArchAMD64, 100, preinstalled))
		shellSyntaxCheck(t, dedicated)
		for _, want := range []string{
			"sudo -n id -u 'ao-worker' >/dev/null 2>&1 || sudo -n useradd -m 'ao-worker'\n" +
				"if getent group docker >/dev/null 2>&1 || grep -q '^docker:' /etc/group 2>/dev/null; then\n" +
				"  sudo -n usermod -aG docker 'ao-worker' 2>/dev/null || sudo -n adduser 'ao-worker' docker >/dev/null 2>&1 || true\nfi\n",
			"sudo -n -b -u 'ao-worker' $ao_setsid sh -c",
		} {
			if !strings.Contains(dedicated, want) {
				t.Errorf("dedicated-user script (preinstalled=%v) missing:\n%s", preinstalled, want)
			}
		}

		workspace := base
		workspace.RunAsWorkspaceUser = true
		script := unquoteBootstrap(t, bootstrapCommandForArchive(workspace, sandbox.ArchAMD64, 100, preinstalled))
		shellSyntaxCheck(t, script)
		for _, unwanted := range []string{"useradd", "usermod", "sudo -n -b", "sudo -n -u", "chown -R", "chgrp", "pkill -u 'ao-worker'"} {
			if strings.Contains(script, unwanted) {
				t.Errorf("workspace-user script (preinstalled=%v) contains %q", preinstalled, unwanted)
			}
		}
		for _, want := range []string{
			"pkill -u \"$(id -u)\" -x 'ao-worker' 2>/dev/null || true\n",
			"GIT_TERMINAL_PROMPT=0 $ao_setsid sh -c 'exec nohup '",
			"install -m 0600 \"$stage/worker.env\" '/home/ubuntu/.ao/worker/worker.env'\n",
			"kill -0 \"$worker_pid\"",
		} {
			if !strings.Contains(script, want) {
				t.Errorf("workspace-user script (preinstalled=%v) missing %q", preinstalled, want)
			}
		}
	}
}

// The workspace-user launch really starts a detached worker as the current
// user, with the real HOME from worker.env and git prompts disabled, and
// records its pid. A preinstalled fake worker keeps the run free of sudo.
func TestWorkspaceUserLaunchScriptStartsWorker(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"sh", "sha256sum", "base64", "gzip", "tar", "install"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is unavailable", tool)
		}
	}
	root := t.TempDir()
	report := filepath.Join(root, "report")
	worker := []byte("#!/bin/sh\nprintf 'home=%s prompt=%s\\n' \"$HOME\" \"$GIT_TERMINAL_PROMPT\" >" + shellQuote(report) + "\nexec sleep 30\n")
	destination := filepath.Join(root, "aotestworker")
	if err := os.WriteFile(destination, worker, 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrap := sandbox.WorkerBootstrap{
		Binary: worker, Destination: destination, User: "ao-worker",
		DurableRoot: root, DurableIdentity: "session-1", RunAsWorkspaceUser: true,
		Environment: map[string]string{"HOME": root, "GIT_TERMINAL_PROMPT": "0"},
	}
	archive, err := bootstrapLaunchArchive(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(archive)
	command := bootstrapCommandForArchive(bootstrap, "", len(encoded), true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Run the script without the login shell, so the PATH below survives
	// (a macOS login shell rebuilds PATH).
	process := exec.CommandContext(ctx, "sh", "-c", unquoteBootstrap(t, command))
	process.Stdin = strings.NewReader(fmt.Sprintf("data:0:%d:%s\ndone:1:0:\n", len(encoded), encoded))
	if runtime.GOOS == "darwin" {
		// macOS base64 reads a file only through -i; the workspace is Linux.
		shims := t.TempDir()
		shim := "#!/bin/sh\nif [ \"$1\" = -d ] && [ -n \"${2:-}\" ]; then exec /usr/bin/base64 -d -i \"$2\"; fi\nexec /usr/bin/base64 \"$@\"\n"
		if err := os.WriteFile(filepath.Join(shims, "base64"), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
		process.Env = append(os.Environ(), "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	output, err := process.CombinedOutput()
	pidFile := filepath.Join(root, ".ao", "worker", "worker.pid")
	t.Cleanup(func() {
		if raw, readErr := os.ReadFile(pidFile); readErr == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil {
				if worker, findErr := os.FindProcess(pid); findErr == nil {
					_ = worker.Kill()
				}
			}
		}
	})
	if err != nil || !strings.Contains(string(output), bootstrapOK) {
		t.Fatalf("bootstrap failed: %v\n%s", err, output)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, readErr := os.ReadFile(report)
		if readErr == nil && strings.Contains(string(raw), "\n") {
			if got, want := strings.TrimSpace(string(raw)), "home="+root+" prompt=0"; got != want {
				t.Fatalf("worker environment = %q, want %q", got, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never reported (bootstrap output: %s)", output)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if identity, err := os.ReadFile(filepath.Join(root, ".ao", "durable-session-id")); err != nil || strings.TrimSpace(string(identity)) != "session-1" {
		t.Fatalf("durable identity = %q, %v", identity, err)
	}
}

// A ready check that fails, then passes, holds the worker back until it
// passes; the stage records the attempts.
func TestReadyProbePollsUntilItPasses(t *testing.T) {
	t.Parallel()
	readyAt := time.Now()
	fake := newDevEnvCoder(t, &readyAt,
		readyProbeResult{exitCode: 1, output: "bootstrapping"},
		readyProbeResult{exitCode: 1, output: "bootstrapping"},
		readyProbeResult{exitCode: 0, output: "Ready"},
	)
	err, stages := devEnvBootstrap(t, fake, func(client *Client) {
		client.readyProbe = "grep -q '^Ready' ~/.bootstrap-status"
		client.readyProbeTimeout = time.Minute
		client.readyProbeInterval = 10 * time.Millisecond
		client.readyProbeInlineWindow = 5 * time.Second
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if fake.readyProbeCalls() != 3 || fake.lastArchive() == nil {
		t.Fatalf("ready calls = %d, uploaded = %v; want 3 calls then the worker", fake.readyProbeCalls(), fake.lastArchive() != nil)
	}
	probe, ok := stages.stage(sandbox.StageReadyProbe)
	if !ok || probe.Err != nil || probe.Attrs["attempts"] != "3" || probe.Attrs["exit_code"] != "0" {
		t.Fatalf("ready probe stage = %+v", probe)
	}
	var readyCommand string
	for _, command := range fake.recordedCommands() {
		if strings.Contains(command, readyProbeMarker) {
			readyCommand = command
		}
	}
	if !strings.HasPrefix(readyCommand, "sh -lc ") || !strings.Contains(readyCommand, "bootstrap-status") {
		t.Fatalf("ready check command = %q", readyCommand)
	}
}

// Inside its timeout, a check that has not passed hands the session back as
// not ready (retry later) once the inline window is used, without installing.
func TestReadyProbeNotYetPassedIsRetryLater(t *testing.T) {
	t.Parallel()
	readyAt := time.Now()
	fake := newDevEnvCoder(t, &readyAt, readyProbeResult{exitCode: 1, output: "still starting"})
	err, _ := devEnvBootstrap(t, fake, func(client *Client) {
		client.readyProbe = "test -f ~/.ready"
		client.readyProbeTimeout = time.Hour
		client.readyProbeInterval = 10 * time.Millisecond
		client.readyProbeInlineWindow = 50 * time.Millisecond
	})
	if !errors.Is(err, sandbox.ErrWorkspaceNotReady) {
		t.Fatalf("error = %v, want retry-later", err)
	}
	var startupErr *sandbox.StartupError
	if errors.As(err, &startupErr) {
		t.Fatalf("a pending check recorded a startup error: %v", startupErr)
	}
	if fake.lastArchive() != nil {
		t.Fatal("worker installed before the ready check passed")
	}
}

// Past its timeout (anchored to the agent's ready time), the check fails
// startup with workspace_setup_not_ready and its last output, bounded and
// redacted.
func TestReadyProbeTimeoutFailsWithOutput(t *testing.T) {
	t.Parallel()
	readyAt := time.Now().Add(-2 * time.Hour)
	fake := newDevEnvCoder(t, &readyAt, readyProbeResult{
		exitCode: 1,
		output:   "Bootstrapping step 3/5\nGITHUB_TOKEN=ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789",
	})
	err, stages := devEnvBootstrap(t, fake, func(client *Client) {
		client.readyProbe = "grep -q '^Ready' ~/.bootstrap-status"
		client.readyProbeTimeout = 15 * time.Minute
		client.readyProbeInterval = 10 * time.Millisecond
	})
	var startupErr *sandbox.StartupError
	if !errors.As(err, &startupErr) || startupErr.Code != sandbox.StartupErrorWorkspaceSetupNotReady {
		t.Fatalf("error = %v, want workspace_setup_not_ready", err)
	}
	if errors.Is(err, sandbox.ErrWorkspaceNotReady) {
		t.Fatal("a timed-out check is retry-later; want definitive")
	}
	for _, want := range []string{"15 minutes", "Bootstrapping step 3/5"} {
		if !strings.Contains(startupErr.Message, want) {
			t.Errorf("message %q missing %q", startupErr.Message, want)
		}
	}
	if strings.Contains(err.Error(), "ghp_abcdefghij") {
		t.Fatalf("probe output was not redacted: %v", err)
	}
	if fake.lastArchive() != nil || fake.readyProbeCalls() != 1 {
		t.Fatalf("uploaded = %v, ready calls = %d; want one check and no worker", fake.lastArchive() != nil, fake.readyProbeCalls())
	}
	if probe, ok := stages.stage(sandbox.StageReadyProbe); !ok || probe.Err == nil {
		t.Fatalf("ready probe stage = %+v, want failed", probe)
	}
}

// The ready check script runs the probe in a subshell and reports its output
// tail and exit status after the marker.
func TestReadyProbeScriptReportsOutputAndExitCode(t *testing.T) {
	t.Parallel()
	output, err := exec.Command("sh", "-c", readyProbeScript("echo 'not ready yet'; exit 3")).CombinedOutput()
	if err != nil {
		t.Fatalf("run ready check script: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "not ready yet") || !strings.Contains(text, readyProbeMarker+":3") {
		t.Fatalf("ready check output = %q", text)
	}
	output, err = exec.Command("sh", "-c", readyProbeScript("true")).CombinedOutput()
	if err != nil || !strings.Contains(string(output), readyProbeMarker+":0") {
		t.Fatalf("passing check output = %q, %v", output, err)
	}
}

func TestTemplateParametersReadsActiveVersion(t *testing.T) {
	t.Parallel()
	const versionID = "5c8d3f0e-4d2b-4a63-9a8e-3b2b2d6a7c02"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v2/templates/" + testTemplateID:
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": testTemplateID, "active_version_id": versionID})
		case "/api/v2/templateversions/" + versionID + "/rich-parameters":
			_, _ = io.WriteString(writer, `[
				{"name":"region","display_name":"Region","description":"Where to run","type":"string","form_type":"dropdown",
				 "mutable":false,"required":true,"default_value":"eu","options":[{"name":"Europe","value":"eu","description":"EU","icon":"x"},{"name":"US","value":"us"}]},
				{"name":"repos","type":"list(string)","form_type":"multi-select","mutable":true,"default_value":"[]"},
				{"name":"gpu","type":"bool","mutable":true,"default_value":"false"}
			]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	parameters, err := client.TemplateParameters(context.Background(), testTemplateID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parameters) != 3 {
		t.Fatalf("parameters = %+v", parameters)
	}
	region := parameters[0]
	if region.Name != "region" || region.DisplayName != "Region" || region.Type != "string" || region.FormType != "dropdown" ||
		region.Mutable || !region.Required || region.DefaultValue != "eu" || region.Order != 0 ||
		len(region.Options) != 2 || region.Options[0] != (TemplateParameterOption{Name: "Europe", Value: "eu", Description: "EU"}) {
		t.Fatalf("region = %+v", region)
	}
	if parameters[1].Type != "list(string)" || parameters[1].Order != 1 || parameters[2].Type != "bool" || parameters[2].Order != 2 {
		t.Fatalf("parameters = %+v", parameters)
	}
	if _, err := client.TemplateParameters(context.Background(), "6f1d2c3b-0000-4000-8000-000000000000"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("unknown template error = %v, want ErrTemplateNotFound", err)
	}
	if _, err := client.TemplateParameters(context.Background(), "not-a-uuid"); err == nil {
		t.Fatal("non-UUID template accepted")
	}
}

// Diagnostics name who the worker runs as and whether checkout reuse and a
// ready check were configured.
func TestDiagnoseReportsDevEnvironment(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v2/workspaces/"+testWorkspaceID {
			_ = json.NewEncoder(writer).Encode(healthyWorkspace())
			return
		}
		_ = json.NewEncoder(writer).Encode([]any{})
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	client.runAsWorkspaceUser = true
	client.reuseCheckout = true
	client.readyProbe = "true"
	client.readyProbeTimeout = 900 * time.Second
	diagnostics, err := client.Diagnose(context.Background(), testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	summary := diagnostics.Summary
	if summary["worker_user"] != "workspace" || summary["checkout_reuse"] != "enabled" ||
		summary["ready_probe"] != "configured" || summary["ready_probe_timeout_seconds"] != "900" {
		t.Fatalf("summary = %v", summary)
	}
}

// ForSandbox carries the session profile's dev-environment options onto the
// session client.
func TestForSandboxCarriesDevEnvironment(t *testing.T) {
	t.Parallel()
	base := newTestClient(t, "https://coder.example.com", nil)
	provider, err := base.ForSandbox(domain.Sandbox{
		SessionID: "session-1",
		ResourceProfile: json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"o","templateId":"` + testTemplateID +
			`","durableRoot":"$HOME","workerUser":"workspace","reuseExistingCheckout":true,"readyProbe":"test -f ~/.ready"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := provider.(*Client)
	if !client.runAsWorkspaceUser || !client.reuseCheckout || client.readyProbe != "test -f ~/.ready" ||
		client.readyProbeTimeout != time.Duration(domain.DefaultCoderReadyProbeTimeoutSeconds)*time.Second {
		t.Fatalf("session client = %+v", client)
	}
	plain, err := base.ForSandbox(domain.Sandbox{
		SessionID:       "session-1",
		ResourceProfile: json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"o","templateId":"` + testTemplateID + `","durableRoot":"$HOME"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if client := plain.(*Client); client.runAsWorkspaceUser || client.reuseCheckout || client.readyProbe != "" {
		t.Fatalf("default session client = %+v", client)
	}
}
