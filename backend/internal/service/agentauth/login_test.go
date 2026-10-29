package agentauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

type guidedTerminalFake struct {
	mu         sync.Mutex
	input      shellterm.OpenCommandTerminalInput
	output     string
	alive      bool
	keys       []string
	closed     bool
	closeErr   error
	closeCalls int
}

func (f *guidedTerminalFake) OpenCommandTerminal(_ context.Context, input shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = input
	f.alive = true
	return shellterm.ShellTerminal{HandleID: "private-auth-pty"}, nil
}
func (f *guidedTerminalFake) ObserveAuthTerminal(_ context.Context, _ string) (shellterm.AuthTerminalObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return shellterm.AuthTerminalObservation{Output: f.output, Alive: f.alive}, nil
}
func (f *guidedTerminalFake) SendAuthTerminalKey(_ context.Context, _ string, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, action)
	return nil
}
func (f *guidedTerminalFake) CloseShellTerminal(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closed = true
	return nil
}

func (f *guidedTerminalFake) IsShellTerminalChildAlive(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive && !f.closed, nil
}

func TestEveryCommandPlanHasAnInlineLoginMethod(t *testing.T) {
	for _, plan := range plans {
		if len(plan.command) == 0 || plan.LaunchMode != LaunchTerminal {
			continue
		}
		if got := loginMethods(plan.AgentID); len(got) == 0 {
			t.Errorf("%s has no inline login method", plan.AgentID)
		}
	}
}

func TestGuidedNativeLoginDoesNotStartAnotherCommandAfterCleanupFailure(t *testing.T) {
	pty := &guidedTerminalFake{closeErr: errors.New("child still running")}
	svc := New(foundExecutable("opencode"), pty)
	t.Cleanup(svc.Close)
	started, err := svc.StartLogin(t.Context(), "opencode", "native", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CancelLogin(t.Context(), "opencode", started.ID)
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_CANCEL_PENDING" {
		t.Fatalf("cancel error=%v", err)
	}
	_, err = svc.StartLogin(t.Context(), "opencode", "native", "")
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_CLEANUP_PENDING" {
		t.Fatalf("restart error=%v", err)
	}
	pty.mu.Lock()
	calls := pty.closeCalls
	pty.mu.Unlock()
	if calls != 3 {
		t.Fatalf("cleanup calls=%d", calls)
	}
}

func TestGuidedNativeLoginHidesTerminalAndCancels(t *testing.T) {
	// No OpenCode catalog, so OpenCode keeps its fixed key methods.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pty := &guidedTerminalFake{output: "private credential=do-not-return\n> OpenAI\n"}
	svc := New(foundExecutable("opencode"), pty)
	t.Cleanup(svc.Close)
	plan, err := svc.Plan(t.Context(), "opencode")
	if err != nil || len(plan.Methods) < 4 || plan.Methods[3].ID != "native" {
		t.Fatalf("plan methods=%+v err=%v", plan.Methods, err)
	}
	started, err := svc.StartLogin(t.Context(), "opencode", "native", "")
	if err != nil {
		t.Fatal(err)
	}
	if started.NativePrompt != "waiting" || started.Status != "running" {
		t.Fatalf("started=%+v", started)
	}
	var current LoginSnapshot
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err = svc.Login(t.Context(), "opencode", started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.NativePrompt == "menu" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if current.NativePrompt != "menu" || current.SelectedOption != "OpenAI" {
		t.Fatalf("prompt=%+v", current)
	}
	encoded, _ := json.Marshal(current)
	if strings.Contains(string(encoded), "do-not-return") || strings.Contains(string(encoded), "private-auth-pty") {
		t.Fatalf("leaked terminal: %s", encoded)
	}
	if _, err := svc.ActOnLogin(t.Context(), "opencode", started.ID, "next"); err != nil {
		t.Fatal(err)
	}
	pty.mu.Lock()
	if !reflect.DeepEqual(pty.keys, []string{"next"}) || !reflect.DeepEqual(pty.input.Argv[1:], []string{"auth", "login"}) {
		t.Fatalf("keys=%v argv=%v", pty.keys, pty.input.Argv)
	}
	pty.mu.Unlock()
	if _, err := svc.CancelLogin(t.Context(), "opencode", started.ID); err != nil {
		t.Fatal(err)
	}
	pty.mu.Lock()
	closed := pty.closed
	pty.mu.Unlock()
	if !closed {
		t.Fatal("hidden PTY was not closed")
	}
}

func TestNativePromptRequiresFallbackForUnreviewedOutput(t *testing.T) {
	for _, output := range []string{"secret=do-not-return\n> Custom provider", "Enter your API key: do-not-return", "Choose a provider\n", "Choose a provider\n1) Anthropic\n2) Custom gateway\n"} {
		kind, label, options, _ := nativePrompt(output)
		if kind != "terminal_required" || label != "" || options != nil {
			t.Fatalf("output=%q kind=%q label=%q options=%v", output, kind, label, options)
		}
	}
	// A lone numbered line is ordinary output text, not a menu.
	if kind, _, _, _ := nativePrompt("1. Read the docs first"); kind != "waiting" {
		t.Fatalf("lone numbered line kind=%q", kind)
	}
}

func TestNativePromptRecognizesNumberedMenus(t *testing.T) {
	kind, selected, options, _ := nativePrompt("Select a provider\n1) Anthropic\n2) OpenAI\n3) OpenRouter\n")
	if kind != "menu" || selected != "" || !reflect.DeepEqual(options, []string{"Anthropic", "OpenAI", "OpenRouter"}) {
		t.Fatalf("kind=%q selected=%q options=%v", kind, selected, options)
	}
	kind, selected, options, _ = nativePrompt("1) Anthropic\n2) Custom provider\n")
	if kind != "terminal_required" || selected != "" || options != nil {
		t.Fatalf("unreviewed numbered menu kind=%q options=%v", kind, options)
	}
}

func TestCopilotAndKimiLoginLinksStayOnTheirHosts(t *testing.T) {
	for _, tc := range []struct {
		agent, url string
		ok         bool
	}{
		{"copilot", "https://github.com/login/device", true},
		{"copilot", "https://github.com/login/oauth/authorize?client_id=x", true},
		{"copilot", "https://evil.example/login/device", false},
		{"kimi", "https://auth.kimi.ai/device?user_code=ABCD-1234", true},
		{"kimi", "https://auth.kimi.com/device?user_code=ABCD-1234", true},
		{"kimi", "https://kimi.example.com/device", false},
	} {
		if got := safeLoginURL(tc.agent, tc.url); got != tc.ok {
			t.Errorf("safeLoginURL(%s, %s) = %v", tc.agent, tc.url, got)
		}
	}
}

func TestNativePromptReadsOnlyTheLatestMenuWithItsDigits(t *testing.T) {
	// An earlier menu in scrollback, then the current one with a cursor line.
	output := "1) Anthropic\n2) OpenAI\nSaved.\nSelect a provider\n\u276f 1) OpenRouter\n  2) Anthropic\n"
	kind, _, options, digits := nativePrompt(output)
	if kind != "menu" || !reflect.DeepEqual(options, []string{"OpenRouter", "Anthropic"}) || !reflect.DeepEqual(digits, []string{"1", "2"}) {
		t.Fatalf("kind=%q options=%v digits=%v", kind, options, digits)
	}
}

func TestGuidedNativeLoginDrivesNumberedMenuWithDigitsOnly(t *testing.T) {
	pty := &guidedTerminalFake{output: "Select a provider\n1) Anthropic\n2) OpenAI\n"}
	svc := New(foundExecutable("goose"), pty)
	t.Cleanup(svc.Close)
	started, err := svc.StartLogin(t.Context(), "goose", "native", "")
	if err != nil {
		t.Fatal(err)
	}
	var current LoginSnapshot
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err = svc.Login(t.Context(), "goose", started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.NativePrompt == "menu" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if current.NativePrompt != "menu" || !reflect.DeepEqual(current.NativeOptions, []string{"Anthropic", "OpenAI"}) {
		t.Fatalf("snapshot=%+v", current)
	}
	if _, err := svc.ActOnLogin(t.Context(), "goose", started.ID, "next"); err == nil {
		t.Fatal("accepted navigation on a numbered menu")
	}
	if _, err := svc.ActOnLogin(t.Context(), "goose", started.ID, "3"); err == nil {
		t.Fatal("accepted a digit outside the recognized options")
	}
	if _, err := svc.ActOnLogin(t.Context(), "goose", started.ID, "2"); err != nil {
		t.Fatal(err)
	}
	// A CLI that reads a line needs Enter after the digit.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if current, _ = svc.Login(t.Context(), "goose", started.ID); current.NativePrompt == "menu" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := svc.ActOnLogin(t.Context(), "goose", started.ID, "select"); err != nil {
		t.Fatalf("Enter on a numbered menu: %v", err)
	}
	pty.mu.Lock()
	if !reflect.DeepEqual(pty.keys, []string{"2", "select"}) {
		t.Fatalf("keys=%v", pty.keys)
	}
	pty.mu.Unlock()
	if _, err := svc.CancelLogin(t.Context(), "goose", started.ID); err != nil {
		t.Fatal(err)
	}
}

func TestLoginMethodsInvokeOnlyFixedCommands(t *testing.T) {
	for _, tc := range []struct {
		agent, method string
		args          []string
		secret        bool
	}{
		{"codex", "browser", []string{"login"}, false},
		{"codex", "device_code", []string{"login", "--device-auth"}, false},
		{"codex", "api_key", []string{"login", "--with-api-key"}, true},
		{"codex", "access_token", []string{"login", "--with-access-token"}, true},
		{"claude-code", "browser", []string{"auth", "login", "--claudeai"}, false},
		{"claude-code", "console", []string{"auth", "login", "--console"}, false},
		{"claude-code", "sso", []string{"auth", "login", "--sso"}, false},
		{"cline", "browser", []string{"auth", "cline"}, false},
		{"kiro", "browser", []string{"login"}, false},
		{"cursor", "browser", []string{"login"}, false},
		{"continue", "browser", []string{"login"}, false},
		{"grok", "browser", []string{"login"}, false},
		{"command-code", "browser", []string{"login"}, false},
		{"copilot", "browser", []string{"login", "--web-flow"}, false},
		{"copilot", "device_code", []string{"login", "--device-code"}, false},
		{"copilot", "copilot_token", []string{"login", "--with-token"}, true},
		{"kimi", "device_code_global", []string{"login", "--region", "global"}, false},
		{"kimi", "device_code_cn", []string{"login", "--region", "mainland-cn"}, false},
		{"opencode", "anthropic_key", nil, true},
		{"opencode", "openai_key", nil, true},
		{"opencode", "openrouter_key", nil, true},
		{"opencode-v2", "anthropic_key", []string{"auth", "import", "--standalone"}, true},
		{"opencode-v2", "openai_key", []string{"auth", "import", "--standalone"}, true},
		{"opencode-v2", "openrouter_key", []string{"auth", "import", "--standalone"}, true},
		{"codewhale", "codewhale_deepseek_key", []string{"auth", "set", "--provider", "deepseek", "--api-key-stdin"}, true},
		{"codewhale", "codewhale_openai_key", []string{"auth", "set", "--provider", "openai", "--api-key-stdin"}, true},
		{"codewhale", "codewhale_openrouter_key", []string{"auth", "set", "--provider", "openrouter", "--api-key-stdin"}, true},
		{"aider", "aider_openai_key", nil, true},
		{"aider", "aider_anthropic_key", nil, true},
		{"aider", "aider_openrouter_key", nil, true},
		{"aider", "aider_deepseek_key", nil, true},
		{"aider", "aider_gemini_key", nil, true},
		{"gemini", "gemini_api_key", nil, true},
	} {
		t.Run(tc.agent+"/"+tc.method, func(t *testing.T) {
			args, secret, err := loginArgs(tc.agent, tc.method)
			if err != nil || !reflect.DeepEqual(args, tc.args) || secret != tc.secret {
				t.Fatalf("args=%v secret=%v err=%v", args, secret, err)
			}
		})
	}
}

func TestUILoginRejectsInvalidInputBeforeLaunch(t *testing.T) {
	svc := New(foundExecutable("codex"), nil)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd { t.Fatal("launched invalid login"); return nil }
	for _, tc := range []struct{ agent, method, secret string }{
		{"unknown", "browser", ""}, {"kiro", "device_code", ""}, {"cursor", "api_key", "test"}, {"continue", "api_key", "test"}, {"grok", "device_code", ""}, {"command-code", "api_key", "test"}, {"opencode-v2", "browser", ""}, {"cline", "api_key", "test"}, {"codewhale", "provider_from_client", "test"}, {"codex", "--arbitrary", ""},
		{"aider", "aider_custom_key", ""}, {"aider", "browser", ""}, {"gemini", "browser", ""}, {"gemini", "api_key", "test"},
		{"codex", "browser", "secret"}, {"codex", "api_key", ""}, {"codex", "api_key", "one\ntwo"},
		{"codex", "api_key", strings.Repeat("s", 16*1024+1)}, {"claude-code", "api_key", "secret"},
	} {
		_, err := svc.StartLogin(t.Context(), tc.agent, tc.method, tc.secret)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("expected invalid input: %v", err)
		}
	}
}

func TestLoginOutputExposesOnlyRecognizedPrompts(t *testing.T) {
	svc := New(nil, nil)
	op := &loginOperation{snapshot: LoginSnapshot{AgentID: "codex", Method: "device_code", Status: "running"}}
	output := &loginOutput{service: svc, operation: op}
	_, _ = output.Write([]byte("private output secret\nhttps://auth.openai.com/codex/dev"))
	if op.snapshot.LoginURL != "" {
		t.Fatal("published an incomplete URL")
	}
	_, _ = output.Write([]byte("ice\n\x1b[32mABCD-EFGHI\x1b[0m\nhttps://evil.example/oauth/authorize\n"))
	if op.snapshot.LoginURL != "https://auth.openai.com/codex/device" || op.snapshot.DeviceCode != "ABCD-EFGHI" {
		t.Fatalf("snapshot=%+v", op.snapshot)
	}
	data, _ := json.Marshal(op.snapshot)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "secret") || strings.Contains(string(data), "evil") {
		t.Fatalf("leaked raw output: %s", data)
	}
	_, _ = output.Write([]byte(strings.Repeat("x", 32*1024)))
	if len(output.tail) > 16*1024 {
		t.Fatal("unbounded output")
	}
	for _, raw := range []string{"http://auth.openai.com/codex/device", "https://auth.openai.com.evil.test/codex/device", "https://evil@auth.openai.com/codex/device", "https://auth.openai.com:444/codex/device", "https://auth.openai.com/other", "https://claude.ai/oauth/authorize"} {
		if safeLoginURL("codex", raw) {
			t.Fatalf("accepted unsafe URL: %s", raw)
		}
	}
	if !safeLoginURL("cline", "https://app.cline.bot/auth/cli") {
		t.Fatal("rejected Cline's account host")
	}
	for _, raw := range []string{"http://app.cline.bot/auth/cli", "https://app.cline.bot.evil.test/auth/cli", "https://evil@app.cline.bot/auth/cli", "https://app.cline.bot:444/auth/cli"} {
		if safeLoginURL("cline", raw) {
			t.Fatalf("accepted unsafe Cline URL: %s", raw)
		}
	}
}

// The subprocess verifies stdin without touching any real harness credentials.
func TestUILoginHelper(t *testing.T) {
	mode := os.Getenv("AO_TEST_UI_LOGIN")
	if mode == "" {
		return
	}
	switch mode {
	case "claude-code":
		fmt.Println("If the browser didn't open, use https://platform.claude.com/oauth/authorize?code=true&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback")
		fmt.Print("Paste code here if prompted > ")
		code, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || code != "one-time-code#state\n" {
			os.Exit(5)
		}
		fmt.Println("https://platform.claude.com/oauth/authorize?code=one-time-code#state")
	case "secret":
		data, _ := io.ReadAll(os.Stdin)
		if string(data) != "test-secret\n" {
			os.Exit(3)
		}
		fmt.Println("test-secret https://auth.openai.com/oauth/authorize?secret=test-secret")
	case "opencode-v2":
		var credentials []struct {
			ID            string `json:"id"`
			IntegrationID string `json:"integrationID"`
			Value         struct {
				Type string `json:"type"`
				Key  string `json:"key"`
			} `json:"value"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&credentials); err != nil || len(credentials) != 1 || credentials[0].ID == "" || credentials[0].IntegrationID != "anthropic" || credentials[0].Value.Type != "key" || credentials[0].Value.Key != "test-secret" || !strings.HasSuffix(os.Getenv("XDG_DATA_HOME"), "opencode-v2-home") {
			os.Exit(3)
		}
	case "fail":
		fmt.Println("private failure details")
		os.Exit(4)
	case "wait":
		fmt.Println("https://auth.openai.com/codex/device\nABCD-EFGHI")
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func loginTestService(t *testing.T, mode string) *Service {
	t.Helper()
	svc := New(foundExecutable("codex"), nil)
	svc.commandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return process.CommandContext(ctx, os.Args[0], "-test.run=^TestUILoginHelper$")
	}
	t.Setenv("AO_TEST_UI_LOGIN", mode)
	t.Cleanup(svc.Close)
	return svc
}

func awaitLogin(t *testing.T, svc *Service, id string, predicate func(LoginSnapshot) bool) LoginSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := svc.Login(t.Context(), "codex", id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("login did not reach expected state")
	return LoginSnapshot{}
}

func TestUILoginSecretUsesStdinAndDiscardsOutput(t *testing.T) {
	svc := loginTestService(t, "secret")
	started, err := svc.StartLogin(t.Context(), "codex", "api_key", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.Status != "running" })
	if result.Status != "succeeded" || result.LoginURL != "" {
		t.Fatalf("result=%+v", result)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "test-secret") {
		t.Fatalf("credential leaked: %s", data)
	}
}

func TestCodewhaleProviderKeyUsesNativeStdinWithoutExposingOutput(t *testing.T) {
	t.Setenv("AO_TEST_UI_LOGIN", "secret")
	svc := New(foundExecutable("codewhale"), nil)
	svc.commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if !reflect.DeepEqual(args, []string{"auth", "set", "--provider", "deepseek", "--api-key-stdin"}) {
			t.Fatalf("unexpected Codewhale args: %v", args)
		}
		return process.CommandContext(ctx, os.Args[0], "-test.run=^TestUILoginHelper$")
	}
	t.Cleanup(svc.Close)
	started, err := svc.StartLogin(t.Context(), "codewhale", "codewhale_deepseek_key", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		result, err := svc.Login(t.Context(), "codewhale", started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "running" {
			data, _ := json.Marshal(result)
			if result.Status != "succeeded" || result.LoginURL != "" || strings.Contains(string(data), "test-secret") {
				t.Fatalf("result=%s", data)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Codewhale credential import did not finish")
}

func TestOpenCodeV2ImportUsesStdinAndIsolatedNativeHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("AO_TEST_UI_LOGIN", "opencode-v2")
	svc := New(foundExecutable("opencode"), nil)
	svc.commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if !reflect.DeepEqual(args, []string{"auth", "import", "--standalone"}) {
			t.Fatalf("unexpected OpenCode 2 import args: %v", args)
		}
		return process.CommandContext(ctx, os.Args[0], "-test.run=^TestUILoginHelper$")
	}
	t.Cleanup(svc.Close)
	started, err := svc.StartLogin(t.Context(), "opencode-v2", "anthropic_key", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		result, err := svc.Login(t.Context(), "opencode-v2", started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "running" {
			if result.Status != "succeeded" || result.LoginURL != "" {
				t.Fatalf("result=%+v", result)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("OpenCode 2 import did not finish")
}

func TestUILoginFailureDoesNotExposeProcessOutput(t *testing.T) {
	svc := loginTestService(t, "fail")
	started, err := svc.StartLogin(t.Context(), "codex", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.Status != "running" })
	if result.Status != "failed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestUILoginCancellationAndOperationIsolation(t *testing.T) {
	svc := loginTestService(t, "wait")
	started, err := svc.StartLogin(t.Context(), "codex", "device_code", "")
	if err != nil {
		t.Fatal(err)
	}
	awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.DeviceCode != "" })
	if _, err := svc.StartLogin(t.Context(), "codex", "browser", ""); err == nil {
		t.Fatal("allowed concurrent login")
	}
	if _, err := svc.Start(t.Context(), "codex"); err == nil {
		t.Fatal("allowed a terminal login over a background login")
	}
	if _, err := svc.CancelLogin(t.Context(), "claude-code", started.ID); err == nil {
		t.Fatal("cancelled another harness login")
	}
	cancelled, err := svc.CancelLogin(t.Context(), "codex", started.ID)
	if err != nil || cancelled.Status != "cancelled" || cancelled.LoginURL != "" || cancelled.DeviceCode != "" {
		t.Fatalf("result=%+v err=%v", cancelled, err)
	}
	next, err := svc.StartLogin(t.Context(), "codex", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CancelLogin(t.Context(), "codex", started.ID); err == nil {
		t.Fatal("stale operation survived replacement")
	}
	if _, err := svc.Login(t.Context(), "codex", next.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUILoginTimeoutAndShutdownStopProcesses(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		svc := loginTestService(t, "wait")
		if !shutdown {
			svc.loginTimeout = 100 * time.Millisecond
		}
		started, err := svc.StartLogin(t.Context(), "codex", "browser", "")
		if err != nil {
			t.Fatal(err)
		}
		if shutdown {
			svc.Close()
		}
		result := awaitLogin(t, svc, started.ID, func(s LoginSnapshot) bool { return s.Status != "running" })
		want := "timed_out"
		if shutdown {
			want = "cancelled"
		}
		if result.Status != want {
			t.Fatalf("result=%+v", result)
		}
		if shutdown {
			if _, err := svc.StartLogin(t.Context(), "codex", "browser", ""); err == nil {
				t.Fatal("started after shutdown")
			}
		}
	}
}

func TestClaudeFallbackURLsFromNativeCLIOutput(t *testing.T) {
	// Sanitized URL forms reproduced with Claude Code 2.1.310 in review
	// https://github.com/OrchestratorInc/agent-orchestrator/pull/7163#discussion_r4373785021.
	for _, raw := range []string{
		"https://claude.com/cai/oauth/authorize?code=true&client_id=redacted&state=redacted",
		"https://claude.com/cai/oauth/authorize?code=true&state=redacted&login_method=sso",
		"https://platform.claude.com/oauth/authorize?code=true&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback",
	} {
		svc := New(nil, nil)
		op := &loginOperation{snapshot: LoginSnapshot{AgentID: "claude-code", Status: "running"}}
		output := &loginOutput{service: svc, operation: op}
		_, _ = output.Write([]byte("If the browser didn't open, visit this URL:\n\n" + raw + "\n\nPaste code here if prompted > "))
		if op.snapshot.LoginURL != raw || !op.snapshot.AcceptsCode {
			t.Fatalf("rejected native fallback URL %q", raw)
		}
	}
	// The native browser tab may use a callback on the daemon's host. Only
	// manual-code URLs can be reopened safely from another host's desktop.
	svc := New(nil, nil)
	op := &loginOperation{snapshot: LoginSnapshot{AgentID: "claude-code", Status: "running"}}
	output := &loginOutput{service: svc, operation: op}
	_, _ = output.Write([]byte("https://claude.com/cai/oauth/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A1234%2Fcallback\n"))
	if op.snapshot.LoginURL != "" || op.snapshot.AcceptsCode {
		t.Fatal("published a host-local callback as a manual-code fallback")
	}
	for _, raw := range []string{"https://claude.com/oauth/authorize", "https://claude.com/cai/oauth/authorize/extra", "https://claude.com.evil.test/cai/oauth/authorize", "https://user@claude.com/cai/oauth/authorize", "https://claude.com:444/cai/oauth/authorize"} {
		if safeLoginURL("claude-code", raw) {
			t.Fatalf("accepted unsafe URL %q", raw)
		}
	}
}

func TestUILoginClaudeCodeUsesOpenStdinAndClearsPrompts(t *testing.T) {
	svc := loginTestService(t, "claude-code")
	svc.executables = executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil })
	started, err := svc.StartLogin(t.Context(), "claude-code", "console", "")
	if err != nil {
		t.Fatalf("start=%+v err=%v", started, err)
	}
	for _, code := range []string{"", "one\ntwo", "one\x00two", strings.Repeat("x", 4097)} {
		if _, err := svc.SubmitLoginCode(t.Context(), "claude-code", started.ID, code); err == nil {
			t.Fatal("accepted invalid code")
		}
	}
	if _, err := svc.SubmitLoginCode(t.Context(), "codex", started.ID, "one-time-code#state"); err == nil {
		t.Fatal("accepted code for another harness")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, pollErr := svc.Login(t.Context(), "claude-code", started.ID)
		if pollErr != nil {
			t.Fatal(pollErr)
		}
		if snapshot.LoginURL != "" {
			if !snapshot.AcceptsCode {
				t.Fatal("fallback URL did not offer code submission")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native process did not wait for a fallback code")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := svc.SubmitLoginCode(t.Context(), "claude-code", started.ID, "one-time-code#state")
	if err != nil || result.AcceptsCode || result.LoginURL != "" {
		t.Fatalf("submit=%+v err=%v", result, err)
	}
	if _, err := svc.SubmitLoginCode(t.Context(), "claude-code", started.ID, "one-time-code#state"); err == nil {
		t.Fatal("accepted duplicate code submission")
	}
	for {
		result, err = svc.Login(t.Context(), "claude-code", started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native login did not finish after reading code")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result.Status != "succeeded" || result.AcceptsCode || result.LoginURL != "" {
		t.Fatalf("finished=%+v", result)
	}
}

func TestUILoginCodeCancellationClosesBlockedInput(t *testing.T) {
	svc := New(nil, nil)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	loginCtx, stop := context.WithCancel(t.Context())
	defer stop()
	op := &loginOperation{
		snapshot: LoginSnapshot{ID: "blocked", AgentID: "claude-code", Status: "running", AcceptsCode: true},
		stdin:    writer, cancel: stop,
	}
	svc.operations[op.snapshot.ID] = op
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := svc.SubmitLoginCode(ctx, "claude-code", "blocked", "private-code"); finished <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := svc.Login(t.Context(), "claude-code", "blocked")
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.AcceptsCode {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("code submission did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_CODE_FAILED" || strings.Contains(err.Error(), "private-code") {
			t.Fatalf("unexpected submission error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled submission left its pipe writer blocked")
	}
	if loginCtx.Err() == nil {
		t.Fatal("cancelled submission did not stop the native login")
	}
	snapshot, _ := svc.Login(t.Context(), "claude-code", "blocked")
	if snapshot.Status != "failed" || snapshot.AcceptsCode {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

type blockedAuthTerminal struct {
	entered chan struct{}
	release chan struct{}
}

func (*blockedAuthTerminal) IsShellTerminalChildAlive(context.Context, string) (bool, error) {
	return false, nil
}

func (b *blockedAuthTerminal) OpenCommandTerminal(ctx context.Context, _ shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	close(b.entered)
	select {
	case <-b.release:
		return shellterm.ShellTerminal{}, nil
	case <-ctx.Done():
		return shellterm.ShellTerminal{}, ctx.Err()
	}
}

func TestTerminalStartupDoesNotBlockLoginPolling(t *testing.T) {
	svc := loginTestService(t, "wait")
	svc.executables = executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil })
	opener := &blockedAuthTerminal{entered: make(chan struct{}), release: make(chan struct{})}
	svc.terminals = opener
	started, err := svc.StartLogin(t.Context(), "codex", "device_code", "")
	if err != nil {
		t.Fatal(err)
	}
	awaitLogin(t, svc, started.ID, func(snapshot LoginSnapshot) bool { return snapshot.DeviceCode != "" })
	terminalDone := make(chan error, 1)
	go func() { _, startErr := svc.Start(t.Context(), "claude-code"); terminalDone <- startErr }()
	defer func() {
		close(opener.release)
		if startErr := <-terminalDone; startErr != nil {
			t.Errorf("terminal startup: %v", startErr)
		}
	}()
	select {
	case <-opener.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal startup did not begin")
	}
	polled := make(chan error, 1)
	go func() { _, pollErr := svc.Login(t.Context(), "codex", started.ID); polled <- pollErr }()
	select {
	case pollErr := <-polled:
		if pollErr != nil {
			t.Fatal(pollErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal I/O blocked background login polling")
	}
	if _, err := svc.StartLogin(t.Context(), "claude-code", "browser", ""); err == nil {
		t.Fatal("allowed a background login during the same harness's terminal startup")
	}
}

func TestTerminalLoginExcludesBackgroundUntilConfirmedExit(t *testing.T) {
	svc := loginTestService(t, "wait")
	launches := 0
	commandContext := svc.commandContext
	svc.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		launches++
		return commandContext(ctx, name, args...)
	}
	opener := &recordingTerminalOpener{terminal: shellterm.ShellTerminal{HandleID: "retained-terminal"}, alive: true}
	svc.terminals = opener
	if _, err := svc.Start(t.Context(), "codex"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.StartLogin(t.Context(), "codex", "device_code", "")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_IN_PROGRESS" || apiErr.Kind != apierr.KindConflict {
		t.Fatalf("background login over live terminal = %v, want AGENT_AUTH_IN_PROGRESS conflict", err)
	}
	if launches != 0 {
		t.Fatalf("launched %d background commands while terminal login was live", launches)
	}
	if _, err := svc.Logout(t.Context(), "codex"); err == nil || opener.calls != 1 {
		t.Fatalf("logout overlapped terminal login: calls=%d err=%v", opener.calls, err)
	}
	opener.alive, opener.probeErr = false, errors.New("runtime probe unavailable")
	_, err = svc.StartLogin(t.Context(), "codex", "device_code", "")
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_TERMINAL_UNKNOWN" {
		t.Fatalf("inconclusive terminal probe = %v, want AGENT_AUTH_TERMINAL_UNKNOWN", err)
	}
	opener.alive, opener.probeErr = true, nil
	if _, err := svc.StartLogin(t.Context(), "codex", "device_code", ""); err == nil {
		t.Fatal("failed probe forgot the still-live terminal")
	}
	if launches != 0 {
		t.Fatalf("launched %d background commands before confirmed terminal exit", launches)
	}
	// A retained terminal/scrollback handle does not block a new login after
	// the runtime confirms that its child, rather than its host, has exited.
	opener.alive = false
	if _, err := svc.StartLogin(t.Context(), "codex", "device_code", ""); err != nil {
		t.Fatalf("confirmed terminal child exit did not release exclusion: %v", err)
	}
	if launches != 1 {
		t.Fatalf("background launches after confirmed terminal exit = %d, want 1", launches)
	}
}
