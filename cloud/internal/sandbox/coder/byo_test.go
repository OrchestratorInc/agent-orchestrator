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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/coder/websocket"
)

// guardArchPattern extracts the architecture an archive's bootstrap command
// expects from its uname -m guard (inside the outer single-quoted script).
var guardArchPattern = regexp.MustCompile(`"\$ao_arch" != '"'"'([a-z0-9]+)'"'"'`)

// fakeByoCoder is a Coder deployment whose workspace runs on a given CPU and
// home directory. Its PTY behaves like the real bootstrap script at the
// markers AO depends on: the probe reports uname -m and $HOME, the arch guard
// rejects a build for another CPU, the preinstalled check misses, and the full
// upload is captured.
type fakeByoCoder struct {
	t       *testing.T
	view    workspace
	machine string
	home    string

	mu       sync.Mutex
	commands []string
	archives []map[string]string
}

func (f *fakeByoCoder) recordedCommands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *fakeByoCoder) lastArchive() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.archives) == 0 {
		return nil
	}
	return f.archives[len(f.archives)-1]
}

func (f *fakeByoCoder) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/api/v2/workspaces/" + testWorkspaceID:
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(f.view)
	case "/api/v2/workspaceagents/" + testAgentID + "/pty":
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
		if strings.Contains(command, workspaceProbe) {
			_, _ = fmt.Fprintf(output, "%s:%s:%s\r\n", workspaceProbe, f.machine, f.home)
			return
		}
		if match := guardArchPattern.FindStringSubmatch(command); match != nil &&
			match[1] != normalizeArchitecture(f.machine) {
			_, _ = fmt.Fprintf(output, "%s:%s\r\n", architectureMismatch, f.machine)
			return
		}
		if strings.Contains(command, preinstalledMiss) {
			_, _ = io.WriteString(output, preinstalledMiss+"\r\n")
			return
		}
		f.captureUpload(command, output)
	default:
		http.Error(writer, "unexpected route", http.StatusNotFound)
	}
}

func (f *fakeByoCoder) captureUpload(command string, connection io.ReadWriter) {
	match := regexp.MustCompile(`target=([0-9]+)`).FindStringSubmatch(command)
	if len(match) != 2 {
		f.t.Errorf("bootstrap command did not include payload length")
		return
	}
	wanted, _ := strconv.Atoi(match[1])
	_, _ = io.WriteString(connection, bootstrapReady+"\r\n")
	decoder := json.NewDecoder(connection)
	var encoded strings.Builder
	expected := 0
	for {
		var input struct {
			Data string `json:"data"`
		}
		if err := decoder.Decode(&input); err != nil {
			f.t.Errorf("decode PTY input: %v", err)
			return
		}
		parts := strings.SplitN(strings.TrimSuffix(input.Data, "\n"), ":", 4)
		if len(parts) != 4 {
			continue
		}
		sequence, _ := strconv.Atoi(parts[1])
		declared, _ := strconv.Atoi(parts[2])
		if parts[0] == "data" && sequence == expected && len(parts[3]) == declared {
			encoded.WriteString(parts[3])
			expected++
			_, _ = fmt.Fprintf(connection, "%s:%d\r\n", bootstrapUploadACK, expected)
			continue
		}
		if parts[0] == "done" && encoded.Len() == wanted {
			_, _ = io.WriteString(connection, bootstrapUploadDone+"\r\n")
			break
		}
	}
	archive, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil {
		f.t.Errorf("decode archive: %v", err)
		return
	}
	f.mu.Lock()
	f.archives = append(f.archives, readArchive(f.t, archive))
	f.mu.Unlock()
	_, _ = io.WriteString(connection, bootstrapOK+"\r\n")
}

func byoWorkspace(lifecycle, architecture string) workspace {
	view := healthyWorkspace()
	view.LatestBuild.Resources[0].Agents[0].LifecycleState = lifecycle
	view.LatestBuild.Resources[0].Agents[0].Architecture = architecture
	return view
}

func multiArchBootstrap(durableRoot string) sandbox.WorkerBootstrap {
	return sandbox.WorkerBootstrap{
		Binary: []byte("amd64-worker"), Destination: "/usr/local/bin/ao-worker",
		HelperBinary: []byte("amd64-helper"), HelperDestination: "/usr/local/bin/ao",
		Builds: map[string]sandbox.WorkerBuild{
			sandbox.ArchAMD64: {Binary: []byte("amd64-worker"), HelperBinary: []byte("amd64-helper")},
			sandbox.ArchARM64: {Binary: []byte("arm64-worker"), HelperBinary: []byte("arm64-helper")},
		},
		User: "ao-worker",
		Environment: map[string]string{
			"AO_WORKER_EXPECTED_SHA256":        sha256Hex([]byte("amd64-worker")),
			"AO_WORKER_HELPER_EXPECTED_SHA256": sha256Hex([]byte("amd64-helper")),
			"AO_WORKSPACE_DIR":                 rootPath(durableRoot, "repository"),
			"HOME":                             rootPath(durableRoot, ".ao/home"),
		},
		DurableRoot: durableRoot, DurableIdentity: "session-1",
	}
}

func rootPath(root, rest string) string { return root + "/" + rest }

func bootstrapAgainst(t *testing.T, fake *fakeByoCoder, bootstrap sandbox.WorkerBootstrap) error {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.BootstrapWorker(ctx, testWorkspaceID, bootstrap)
}

func TestAgentReadinessFollowsLifecycleState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status, lifecycle string
		want              bool
	}{
		{"connected", "created", false},
		{"connected", "starting", false},
		{"connected", "ready", true},
		// A failed or overrunning startup script leaves a usable workspace that
		// Coder reports as unhealthy: AO must bootstrap it, not wait forever.
		{"connected", "start_error", true},
		{"connected", "start_timeout", true},
		{"connected", "shutting_down", false},
		{"connecting", "ready", false},
		{"disconnected", "ready", false},
	}
	client := &Client{}
	for _, tc := range cases {
		view := byoWorkspace(tc.lifecycle, "arm64")
		agent := &view.LatestBuild.Resources[0].Agents[0]
		agent.Status = tc.status
		unhealthy := tc.lifecycle == "start_error" || tc.lifecycle == "start_timeout"
		agent.Health.Healthy = !unhealthy
		view.Health.Healthy = !unhealthy
		if got := agentReady(*agent); got != tc.want {
			t.Errorf("agentReady(%s, %s) = %v, want %v", tc.status, tc.lifecycle, got, tc.want)
		}
		wantState := sandbox.StateProvisioning
		if tc.want {
			wantState = sandbox.StateRunning
		}
		if got := client.toEnvironment(view).State; got != wantState {
			t.Errorf("toEnvironment(%s, %s).State = %q, want %q", tc.status, tc.lifecycle, got, wantState)
		}
	}
}

// While the agent's startup script is still running, bootstrapping must report
// "not ready yet" without ever opening a PTY.
func TestBootstrapWaitsForStartingAgentWithoutOpeningPTY(t *testing.T) {
	t.Parallel()
	fake := &fakeByoCoder{t: t, view: byoWorkspace("starting", "arm64"), machine: "aarch64"}
	err := bootstrapAgainst(t, fake, multiArchBootstrap("/home/coder"))
	if !errors.Is(err, sandbox.ErrWorkspaceNotReady) {
		t.Fatalf("bootstrap error = %v, want ErrWorkspaceNotReady", err)
	}
	if commands := fake.recordedCommands(); len(commands) != 0 {
		t.Fatalf("opened %d PTYs while the agent was starting", len(commands))
	}
}

func TestBootstrapInstallsArm64BuildAndAdvertisesItsHashes(t *testing.T) {
	t.Parallel()
	fake := &fakeByoCoder{t: t, view: byoWorkspace("ready", "arm64"), machine: "aarch64"}
	if err := bootstrapAgainst(t, fake, multiArchBootstrap("/home/coder")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	files := fake.lastArchive()
	if files["ao-worker"] != "arm64-worker" || files["ao"] != "arm64-helper" {
		t.Fatalf("archive binaries = %q/%q, want the arm64 build", files["ao-worker"], files["ao"])
	}
	for _, want := range []string{
		"AO_WORKER_EXPECTED_SHA256='" + sha256Hex([]byte("arm64-worker")) + "'",
		"AO_WORKER_HELPER_EXPECTED_SHA256='" + sha256Hex([]byte("arm64-helper")) + "'",
		"AO_WORKER_EXPECTED_ARCH='arm64'",
	} {
		if !strings.Contains(files["worker.env"], want) {
			t.Errorf("worker.env missing %s:\n%s", want, files["worker.env"])
		}
	}
	for _, command := range fake.recordedCommands() {
		if match := guardArchPattern.FindStringSubmatch(command); match == nil || match[1] != "arm64" {
			t.Errorf("bootstrap command does not guard on arm64: %v", match)
		}
	}
}

// An agent that does not declare its architecture is assumed amd64; the
// in-script uname -m guard then reports the real CPU and AO retries once with
// the matching build instead of installing a binary the workspace cannot run.
func TestBootstrapRetriesWithArchitectureDetectedByUname(t *testing.T) {
	t.Parallel()
	fake := &fakeByoCoder{t: t, view: byoWorkspace("ready", ""), machine: "aarch64"}
	if err := bootstrapAgainst(t, fake, multiArchBootstrap("/home/coder")); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if files := fake.lastArchive(); files["ao-worker"] != "arm64-worker" {
		t.Fatalf("installed %q, want the arm64 build after the uname -m mismatch", files["ao-worker"])
	}
	commands := fake.recordedCommands()
	if match := guardArchPattern.FindStringSubmatch(commands[0]); match == nil || match[1] != "amd64" {
		t.Fatalf("first attempt guard = %v, want amd64", match)
	}
}

func TestBootstrapReportsUnsupportedArchitecture(t *testing.T) {
	t.Parallel()
	for name, fake := range map[string]*fakeByoCoder{
		"declared by agent": {view: byoWorkspace("ready", "armv7"), machine: "armv7l"},
		"detected by uname": {view: byoWorkspace("ready", ""), machine: "riscv64"},
	} {
		t.Run(name, func(t *testing.T) {
			fake.t = t
			err := bootstrapAgainst(t, fake, multiArchBootstrap("/home/coder"))
			var startupErr *sandbox.StartupError
			if !errors.As(err, &startupErr) || startupErr.Code != sandbox.StartupErrorUnsupportedArchitecture {
				t.Fatalf("bootstrap error = %v, want unsupported architecture", err)
			}
			if !strings.Contains(startupErr.Message, "isn't supported") {
				t.Fatalf("message = %q, want a human explanation", startupErr.Message)
			}
			if fake.lastArchive() != nil {
				t.Fatal("uploaded a worker to an unsupported CPU")
			}
		})
	}
}

// A deployment without the arm64 build cannot serve an arm64 workspace and
// must say so instead of installing the amd64 binary.
func TestBootstrapWithoutArm64BuildIsUnsupported(t *testing.T) {
	t.Parallel()
	fake := &fakeByoCoder{t: t, view: byoWorkspace("ready", "arm64"), machine: "aarch64"}
	bootstrap := multiArchBootstrap("/home/coder")
	delete(bootstrap.Builds, sandbox.ArchARM64)
	err := bootstrapAgainst(t, fake, bootstrap)
	var startupErr *sandbox.StartupError
	if !errors.As(err, &startupErr) || startupErr.Code != sandbox.StartupErrorUnsupportedArchitecture {
		t.Fatalf("bootstrap error = %v, want unsupported architecture", err)
	}
}

// A $HOME durable root resolves to the workspace user's real home (probed in
// the workspace), so a template that does not run as "coder" still works.
func TestBootstrapResolvesHomeDurableRoot(t *testing.T) {
	t.Parallel()
	fake := &fakeByoCoder{t: t, view: byoWorkspace("ready", "arm64"), machine: "aarch64", home: "/home/ahmad"}
	if err := bootstrapAgainst(t, fake, multiArchBootstrap(sandbox.CoderHomeDurableRoot)); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	files := fake.lastArchive()
	for _, want := range []string{
		"AO_WORKSPACE_DIR='/home/ahmad/repository'",
		"HOME='/home/ahmad/.ao/home'",
	} {
		if !strings.Contains(files["worker.env"], want) {
			t.Errorf("worker.env missing %s:\n%s", want, files["worker.env"])
		}
	}
	commands := fake.recordedCommands()
	if !strings.Contains(commands[0], workspaceProbe) {
		t.Fatalf("first PTY command = %q, want the workspace probe", commands[0])
	}
	if last := commands[len(commands)-1]; !strings.Contains(last, "durable_root='\"'\"'/home/ahmad'\"'\"'") {
		t.Fatalf("bootstrap command does not use the resolved home: %s", last)
	}
}

func TestBootstrapRejectsUnsafeProbedHome(t *testing.T) {
	t.Parallel()
	fake := &fakeByoCoder{t: t, view: byoWorkspace("ready", "amd64"), machine: "x86_64", home: "/"}
	err := bootstrapAgainst(t, fake, multiArchBootstrap(sandbox.CoderHomeDurableRoot))
	var startupErr *sandbox.StartupError
	if !errors.As(err, &startupErr) || startupErr.Code != sandbox.StartupErrorDurableRootUnavailable {
		t.Fatalf("bootstrap error = %v, want durable root unavailable", err)
	}
}

// A PTY that closes before the bootstrap is ready is classified as retry-later
// with a human explanation, and each attempt records the agent's state.
func TestBootstrapTerminalClosedIsRetryLater(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v2/workspaces/" + testWorkspaceID:
			writeWorkspace(t, writer, "running", "connected", true)
		default:
			connection, err := websocket.Accept(writer, request, nil)
			if err != nil {
				return
			}
			_ = connection.Close(websocket.StatusNormalClosure, "")
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := client.BootstrapWorker(ctx, testWorkspaceID, multiArchBootstrap("/home/coder"))
	if !errors.Is(err, sandbox.ErrWorkspaceNotReady) {
		t.Fatalf("bootstrap error = %v, want ErrWorkspaceNotReady", err)
	}
	var startupErr *sandbox.StartupError
	if !errors.As(err, &startupErr) || startupErr.Code != sandbox.StartupErrorTerminalUnavailable ||
		!strings.Contains(startupErr.Message, "couldn't open a terminal") {
		t.Fatalf("startup error = %+v, want terminal unavailable", startupErr)
	}
	if !strings.Contains(err.Error(), `attempt 1: `) || !strings.Contains(err.Error(), `agent status="connected" lifecycle="ready"`) {
		t.Fatalf("error does not record the agent state per attempt: %v", err)
	}
}

func TestClassifyBootstrapError(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err      error
		code     string
		notReady bool
	}{
		"terminal": {
			err:  fmt.Errorf("launch: %w", fmt.Errorf("%w: closed", errTerminalNotReady)),
			code: sandbox.StartupErrorTerminalUnavailable, notReady: true,
		},
		"mount": {
			err:  errors.New("coder: worker bootstrap failed: configured Coder durable root is not a mounted directory"),
			code: sandbox.StartupErrorDurableRootUnavailable,
		},
		"identity": {
			err:  errors.New("coder: worker bootstrap failed: Coder durable root belongs to a different AO session"),
			code: sandbox.StartupErrorDurableRootUnavailable,
		},
		"sudo": {
			err:  errors.New("coder: worker bootstrap failed: sudo: a password is required"),
			code: sandbox.StartupErrorBootstrapFailed,
		},
		"other": {err: errors.New("boom"), code: sandbox.StartupErrorBootstrapFailed},
	}
	for name, tc := range cases {
		err := classifyBootstrapError(tc.err, "/home/coder")
		var startupErr *sandbox.StartupError
		if !errors.As(err, &startupErr) || startupErr.Code != tc.code || startupErr.Message == "" {
			t.Errorf("%s: classified %v, want code %s", name, err, tc.code)
			continue
		}
		if got := errors.Is(err, sandbox.ErrWorkspaceNotReady); got != tc.notReady {
			t.Errorf("%s: ErrWorkspaceNotReady = %v, want %v", name, got, tc.notReady)
		}
		if !errors.Is(err, tc.err) && !tc.notReady {
			t.Errorf("%s: classification dropped the operator detail", name)
		}
	}
	if err := classifyBootstrapError(context.Canceled, "/x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was reclassified: %v", err)
	}
}

// durableRootScript runs for real against a temp directory with a sudo shim:
// a bring-your-own root that is not a mount point is created and accepted,
// while the strict deployment policy refuses it.
func TestDurableRootScriptMountPolicy(t *testing.T) {
	t.Parallel()
	shimDir := t.TempDir()
	shim := "#!/bin/sh\n[ \"$1\" = -n ] && shift\nexec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "sudo"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(root string, requireMount bool) (string, error) {
		command := exec.Command("sh", "-c", "set -eu\n"+durableRootScript(root, requireMount)+"echo __DONE__\n")
		command.Env = append(os.Environ(), "PATH="+shimDir+":"+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		return string(output), err
	}

	missing := filepath.Join(t.TempDir(), "home", "ahmad")
	if output, err := run(missing, false); err != nil || !strings.Contains(output, "__DONE__") {
		t.Fatalf("lenient policy on a missing root: %v\n%s", err, output)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("lenient policy did not create the root: %v", err)
	}
	if output, err := run(missing, true); err == nil || !strings.Contains(output, "is not a mounted directory") {
		t.Fatalf("strict policy accepted an unmounted root: %v\n%s", err, output)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(missing, link); err != nil {
		t.Fatal(err)
	}
	if output, err := run(link, false); err == nil || !strings.Contains(output, "symbolic link") {
		t.Fatalf("lenient policy accepted a symlinked root: %v\n%s", err, output)
	}
}

func TestWorkspaceNameWithPrefix(t *testing.T) {
	t.Parallel()
	const sessionID = "1A2B3C4D-5E6F-4a7b-8c9d-0e1f2a3b4c5d"
	if got := WorkspaceNameWithPrefix("", sessionID); got != WorkspaceName(sessionID) {
		t.Fatalf("empty prefix = %q, want the default %q", got, WorkspaceName(sessionID))
	}
	if got := WorkspaceNameWithPrefix("ahmad", sessionID); got != "ahmad-1a2b3c4d5e6f" {
		t.Fatalf("prefixed name = %q, want ahmad-1a2b3c4d5e6f", got)
	}
	longest := strings.Repeat("a", domain.MaxCoderWorkspaceNamePrefix)
	got := WorkspaceNameWithPrefix(longest, sessionID)
	if len(got) != maxWorkspaceNameLength || !strings.HasPrefix(got, longest+"-") {
		t.Fatalf("20-character prefix produced %q (%d chars), want exactly 32", got, len(got))
	}
	if got := WorkspaceNameWithPrefix("dev", "session-1"); !regexp.MustCompile(`^dev-[0-9a-f]{12}$`).MatchString(got) {
		t.Fatalf("non-UUID session id produced %q, want a hashed id", got)
	}
	for _, prefix := range []string{"ahmad", longest, "a1-b2"} {
		name := WorkspaceNameWithPrefix(prefix, sessionID)
		if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(name) || len(name) > 32 {
			t.Errorf("WorkspaceNameWithPrefix(%q) = %q is not a valid Coder workspace name", prefix, name)
		}
	}
}

// A session whose project set a prefix creates, finds, and validates its
// workspace under the prefixed name.
func TestScopedClientUsesWorkspaceNamePrefix(t *testing.T) {
	t.Parallel()
	const sessionID = "9f8e7d6c-5b4a-4321-8fed-cba987654321"
	var created string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body createWorkspaceRequest
		_ = json.NewDecoder(request.Body).Decode(&body)
		created = body.Name
		view := healthyWorkspace()
		view.Name, view.OwnerName, view.TemplateID = body.Name, "planned-owner", body.TemplateID
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(view)
	}))
	defer server.Close()
	base := newTestClient(t, server.URL, nil)
	provider, err := base.ForSandbox(domain.Sandbox{
		SessionID: sessionID,
		ResourceProfile: json.RawMessage(fmt.Sprintf(
			`{"coder":{"baseUrl":%q,"owner":"planned-owner","templateId":%q,"durableRoot":"$HOME","workspaceNamePrefix":"elevenx"}}`,
			server.URL, testTemplateID)),
	})
	if err != nil {
		t.Fatalf("scope client: %v", err)
	}
	if _, err := provider.Create(context.Background(), sandbox.Spec{SessionID: sessionID}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if created != "elevenx-9f8e7d6c5b4a" {
		t.Fatalf("created workspace %q, want elevenx-9f8e7d6c5b4a", created)
	}
}
