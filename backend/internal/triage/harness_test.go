package triage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty/ptyregistry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The integration child is a controlled executable, never an installed agent.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "pty-host":
			os.Exit(conpty.RunHost(os.Args[2:], os.Stdout))
		case "triage-fake-harness":
			cwd, _ := os.Getwd()
			raw, _ := json.Marshal(map[string]any{"args": os.Args[2:], "cwd": cwd, "account": os.Getenv("FAKE_SELECTED_ACCOUNT")})
			fmt.Printf("NATIVE_READY:%s\n", raw)
			scanner := bufio.NewScanner(os.Stdin)
			if os.Getenv("FAKE_STARTUP_DIALOG") == "1" {
				fmt.Println("NATIVE_AUTH_REQUIRED")
				if !scanner.Scan() || scanner.Text() != "accept-native-dialog" {
					os.Exit(2)
				}
				fmt.Println("NATIVE_PROMPT_READY")
			}
			for scanner.Scan() {
				fmt.Printf("NATIVE_RECEIVED:%s\n", scanner.Text())
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

type fakeHarnessAgent struct {
	ports.Agent
	afterStart bool
}

func (a fakeHarnessAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	exe, err := os.Executable()
	return []string{exe, "triage-fake-harness", "--model", cfg.Config.Model, "--effort", cfg.Config.Effort, "--permissions", string(cfg.Permissions), "--mode", cfg.Config.Mode, "--prompt", cfg.Prompt}, err
}
func (a fakeHarnessAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if a.afterStart {
		return ports.PromptDeliveryAfterStart, nil
	}
	return ports.PromptDeliveryInCommand, nil
}

func TestNativeTerminalAttachesOnceAndPreservesSettingsAcrossInput(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native detached PTY requires macOS or Linux")
	}
	dataDir := t.TempDir()
	run := Run{ID: "native-terminal", Request: StartRequest{Selection: Selection{Harness: "fixture", Model: "configured-model", Effort: "xhigh", Permissions: "auto", Mode: "native-mode"}}}
	workspace, _, err := diagnosticWorkspace(dataDir, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rt, err := newHarnessRuntime(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ptyregistry.SetRunFilePath("") })
	terminal, err := launchTerminalAgent(ctx, run, workspace, dataDir, agentStartupPrompt(), diagnosticEnv(os.Environ(), map[string]string{"FAKE_SELECTED_ACCOUNT": "selected-profile"}), fakeHarnessAgent{}, rt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	output := make(chan string, 16)
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := terminal.Read(buffer)
			if n > 0 {
				output <- string(buffer[:n])
			}
			if err != nil {
				close(output)
				return
			}
		}
	}()
	readUntil := func(marker string) string {
		t.Helper()
		var text strings.Builder
		for !strings.Contains(text.String(), marker) {
			select {
			case chunk, ok := <-output:
				if !ok {
					t.Fatal("terminal closed early")
				}
				text.WriteString(chunk)
			case <-ctx.Done():
				t.Fatalf("terminal never emitted %q: %s", marker, text.String())
			}
		}
		return text.String()
	}
	startup := readUntil("NATIVE_READY:")
	for _, wanted := range []string{"configured-model", "xhigh", "auto", "native-mode", "selected-profile", "triage.md", workspace} {
		if !strings.Contains(startup, wanted) {
			t.Fatalf("launch lost %q: %s", wanted, startup)
		}
	}
	for _, message := range []string{"first correction", "second correction"} {
		if _, err := terminal.Write([]byte(message + "\n")); err != nil {
			t.Fatal(err)
		}
		readUntil("NATIVE_RECEIVED:" + message)
	}
	if err := terminal.Resize(40, 120); err != nil {
		t.Fatal(err)
	}
	entries, complete, err := ptyregistry.Scan(ctx)
	if err != nil || !complete || len(entries) != 1 || entries[0].SessionID != "triage-"+run.ID {
		t.Fatalf("native process changed or leaked: %#v, %v", entries, err)
	}
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	entries, complete, err = ptyregistry.Scan(ctx)
	if err != nil || !complete || len(entries) != 0 {
		t.Fatalf("native terminal leaked after stop: %#v, %v", entries, err)
	}
}

type testTerminalStream struct {
	io.ReadWriteCloser
	mu         sync.Mutex
	input      strings.Builder
	rows, cols uint16
	closed     int
}

func (s *testTerminalStream) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.input.Write(data)
}
func (s *testTerminalStream) Resize(rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows, s.cols = rows, cols
	return nil
}
func (s *testTerminalStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	if s.ReadWriteCloser != nil {
		return s.ReadWriteCloser.Close()
	}
	return nil
}

type testHarnessRuntime struct {
	ports.Runtime
	stream    *testTerminalStream
	created   ports.RuntimeConfig
	messages  []string
	destroys  int
	attachErr error
	alive     bool
	probeErr  error
}

func (r *testHarnessRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.created = cfg
	return ports.RuntimeHandle{ID: string(cfg.SessionID)}, nil
}
func (r *testHarnessRuntime) Attach(context.Context, ports.RuntimeHandle, uint16, uint16) (ports.Stream, error) {
	return r.stream, r.attachErr
}
func (r *testHarnessRuntime) Destroy(context.Context, ports.RuntimeHandle) error {
	r.destroys++
	return nil
}
func (r *testHarnessRuntime) IsChildAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return r.alive, r.probeErr
}
func (r *testHarnessRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, text string) error {
	r.messages = append(r.messages, text)
	return nil
}

func TestTerminalLaunchCleansFailedAttachAndDeliversNativeAfterStartPrompt(t *testing.T) {
	for _, attachFailure := range []bool{false, true} {
		rt := &testHarnessRuntime{stream: &testTerminalStream{}}
		if attachFailure {
			rt.attachErr = errors.New("attach failed")
		}
		terminal, err := launchTerminalAgent(context.Background(), Run{ID: "launch", Request: StartRequest{Selection: Selection{Harness: "fixture"}}}, t.TempDir(), t.TempDir(), "read triage.md", nil, fakeHarnessAgent{afterStart: true}, rt)
		if attachFailure {
			if err == nil || terminal != nil || rt.destroys != 1 {
				t.Fatalf("failed attach leaked: %v, %d", err, rt.destroys)
			}
			continue
		}
		if err == nil {
			err = <-terminal.startup
		}
		if err != nil || len(rt.messages) != 1 || rt.messages[0] != "read triage.md" {
			t.Fatalf("native prompt delivery = %#v, %v", rt.messages, err)
		}
		_ = terminal.Close()
		_ = terminal.Close()
		if rt.destroys != 1 || rt.stream.closed != 1 {
			t.Fatal("terminal cleanup was not idempotent")
		}
	}
}

func TestTerminalCommandsBoundNativeInputAndResize(t *testing.T) {
	stream := &testTerminalStream{}
	item := &investigation{terminal: &TerminalAgent{stream: stream}}
	for _, text := range []string{"", strings.Repeat("x", maxTerminalBytes+1)} {
		if err := item.terminalInput(Command{Text: text}); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	for _, size := range [][2]int{{0, 80}, {24, 0}, {1001, 80}, {24, 1001}} {
		if err := item.terminalResize(Command{Rows: size[0], Cols: size[1]}); err == nil {
			t.Fatal("invalid resize accepted")
		}
	}
	if err := item.terminalInput(Command{Text: "\x1b[A\r"}); err != nil {
		t.Fatal(err)
	}
	if err := item.terminalResize(Command{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	if stream.input.String() != "\x1b[A\r" || stream.rows != 40 || stream.cols != 120 {
		t.Fatal("native terminal commands changed")
	}
}

func TestAllTerminalHarnessesComeFromRegisteredAdapters(t *testing.T) {
	for _, harness := range []string{"amp", "codex", "claude-code", "pi", "gemini", "aider"} {
		if terminalHarnesses()[harness] == nil {
			t.Fatalf("missing registered %s", harness)
		}
	}
}

type startupDialogAgent struct{ fakeHarnessAgent }

func (startupDialogAgent) PromptReadinessHints(context.Context, ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{Patterns: []string{"NATIVE_PROMPT_READY"}, Timeout: 10 * time.Second, PollInterval: 10 * time.Millisecond}, nil
}

func TestNativeTerminalStartupDialogAcceptsInputBeforeInitialPrompt(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native detached PTY requires macOS or Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dataDir := t.TempDir()
	rt, err := newHarnessRuntime(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ptyregistry.SetRunFilePath("") })
	terminal, err := launchTerminalAgent(ctx, Run{ID: "startup-dialog"}, dataDir, dataDir, "read triage.md", diagnosticEnv(os.Environ(), map[string]string{"FAKE_STARTUP_DIALOG": "1"}), startupDialogAgent{fakeHarnessAgent{afterStart: true}}, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = terminal.Close() }()
	output := make(chan string, 16)
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := terminal.Read(buffer)
			if n > 0 {
				output <- string(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	waitFor := func(marker string) {
		t.Helper()
		var text strings.Builder
		for !strings.Contains(text.String(), marker) {
			select {
			case chunk := <-output:
				text.WriteString(chunk)
			case <-ctx.Done():
				t.Fatalf("startup never emitted %q: %s", marker, text.String())
			}
		}
	}
	waitFor("NATIVE_AUTH_REQUIRED")
	select {
	case err := <-terminal.startup:
		t.Fatalf("initial prompt ran before native dialog input: %v", err)
	default:
	}
	if err := terminal.Resize(32, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("accept-native-dialog\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-terminal.startup:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitFor("NATIVE_RECEIVED:read triage.md")
}

func TestAmpSelectedModeOverridesConfiguredDefault(t *testing.T) {
	for _, test := range []struct{ selected, configured, want string }{{"low", "high", "low"}, {"", "high", "high"}} {
		rt := &testHarnessRuntime{stream: &testTerminalStream{}}
		run := Run{ID: "amp-mode", Request: StartRequest{Selection: Selection{Harness: "amp", Model: test.selected, Mode: test.configured}}}
		terminal, err := launchTerminalAgent(context.Background(), run, t.TempDir(), t.TempDir(), "read triage.md", nil, fakeHarnessAgent{}, rt)
		if err != nil {
			t.Fatal(err)
		}
		_ = terminal.Close()
		args := strings.Join(rt.created.Argv, "|")
		if !strings.Contains(args, "--mode|"+test.want) || !strings.Contains(args, "--model||--effort") {
			t.Fatalf("native Amp mode changed: %s", args)
		}
	}
}
