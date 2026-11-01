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
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

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
		{"claude-code", "setup_token", nil, true},
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
		{"unknown", "browser", ""}, {"cursor", "browser", ""}, {"codex", "--arbitrary", ""},
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

func TestUILoginSavesClaudeSetupTokenWithoutNativeCommand(t *testing.T) {
	dataDir := t.TempDir()
	svc := NewWithAgentResolver(executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil }), nil, nil, dataDir)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("setup token launched a native command")
		return nil
	}
	t.Cleanup(svc.Close)
	for _, invalid := range []string{"", "sk-ant-api03-not-a-setup-token", "sk-ant-oat01 has spaces", "sk-ant-oat"} {
		_, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", invalid)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("accepted invalid setup token %q: %v", invalid, err)
		}
	}
	if _, err := os.Stat(claudecode.LocalOAuthTokenPath(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid token was saved: %v", err)
	}
	const token = "sk-ant-oat01-ui-setup-token"
	started, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", "  "+token+"  ")
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "succeeded" || started.Method != "setup_token" {
		t.Fatalf("started=%+v", started)
	}
	data, _ := json.Marshal(started)
	if strings.Contains(string(data), token) {
		t.Fatalf("token leaked into snapshot: %s", data)
	}
	saved, err := os.ReadFile(claudecode.LocalOAuthTokenPath(dataDir))
	if err != nil || string(saved) != token {
		t.Fatalf("saved=%q err=%v", saved, err)
	}
	// A finished save does not block a later sign-in or cancellation.
	if _, err := svc.CancelLogin(t.Context(), "claude-code", started.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", token); err != nil {
		t.Fatalf("replacing the setup token: %v", err)
	}
}

func TestUILoginNativeClaudeSignInClearsSavedSetupToken(t *testing.T) {
	svc := loginTestService(t, "succeed")
	svc.executables = executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil })
	svc.dataDir = t.TempDir()
	if err := claudecode.SaveLocalOAuthToken(svc.dataDir, []byte("sk-ant-oat01-earlier-setup-token")); err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartLogin(t.Context(), "claude-code", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, pollErr := svc.Login(t.Context(), "claude-code", started.ID)
		if pollErr != nil {
			t.Fatal(pollErr)
		}
		if snapshot.Status != "running" {
			if snapshot.Status != "succeeded" {
				t.Fatalf("snapshot=%+v", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native login did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(claudecode.LocalOAuthTokenPath(svc.dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("earlier setup token survived a newer sign-in: %v", err)
	}
}

// survivingAuthTerminals models shellterm after a daemon restart in the same
// app launch: earlier command terminals are still listed and may still run.
type survivingAuthTerminals struct {
	terminals []shellterm.ShellTerminal
	alive     map[string]bool
	probeErr  error
	listErr   error
	probes    []string
}

func (*survivingAuthTerminals) OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	return shellterm.ShellTerminal{HandleID: "new-terminal"}, nil
}

func (s *survivingAuthTerminals) IsShellTerminalChildAlive(_ context.Context, handleID string) (bool, error) {
	s.probes = append(s.probes, handleID)
	return s.alive[handleID], s.probeErr
}

func (s *survivingAuthTerminals) ListCommandTerminalsForCurrentAppRun(context.Context) ([]shellterm.ShellTerminal, error) {
	return s.terminals, s.listErr
}

func TestLoginExcludesTerminalLoginSurvivingDaemonRestart(t *testing.T) {
	dataDir := t.TempDir()
	terminals := &survivingAuthTerminals{
		terminals: []shellterm.ShellTerminal{
			{HandleID: "github", Title: "Connect GitHub"},
			{HandleID: "claude", Title: "Log in to Claude Code"},
			{HandleID: "codex", Title: "Log in to Codex"},
		},
		alive: map[string]bool{"github": true, "claude": true, "codex": true},
	}
	// A fresh Service, as after a daemon restart: no terminalLogins yet.
	svc := NewWithAgentResolver(executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil }), nil, terminals, dataDir)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("admitted a background login while the terminal login still runs")
		return nil
	}
	t.Cleanup(svc.Close)
	_, err := svc.StartLogin(t.Context(), "codex", "browser", "")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_IN_PROGRESS" {
		t.Fatalf("expected the surviving Codex terminal to exclude login: %v", err)
	}
	if !reflect.DeepEqual(terminals.probes, []string{"codex"}) {
		t.Fatalf("probes = %v, want only the Codex terminal", terminals.probes)
	}
	if _, err := svc.Start(t.Context(), "codex"); !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_IN_PROGRESS" {
		t.Fatalf("expected the adopted terminal to exclude terminal login too: %v", err)
	}
	// Each harness is excluded only by its own surviving terminal.
	if _, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", "sk-ant-oat01-unrelated-harness"); !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_IN_PROGRESS" {
		t.Fatalf("expected the Claude terminal to exclude Claude login: %v", err)
	}
	terminals.alive = map[string]bool{}
	terminals.probes = nil
	if _, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", "sk-ant-oat01-after-terminal-exit"); err != nil {
		t.Fatalf("login after the terminal exited: %v", err)
	}
}

func TestLoginRecognizesRenamedTerminalByHarnessWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	terminals := &survivingAuthTerminals{
		terminals: []shellterm.ShellTerminal{{HandleID: "claude-renamed", Title: "renamed tab", WorkingDir: filepath.Join(dataDir, "auth-workspace", "claude-code")}},
		alive:     map[string]bool{"claude-renamed": true},
	}
	svc := NewWithAgentResolver(executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil }), nil, terminals, dataDir)
	t.Cleanup(svc.Close)
	_, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", "sk-ant-oat01-renamed-terminal")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_IN_PROGRESS" {
		t.Fatalf("expected the harness workspace terminal to exclude login: %v", err)
	}
}

func TestLoginFailsClosedWhenSurvivingTerminalsCannotBeChecked(t *testing.T) {
	for name, terminals := range map[string]*survivingAuthTerminals{
		"list":  {listErr: errors.New("store unavailable")},
		"probe": {terminals: []shellterm.ShellTerminal{{HandleID: "claude", Title: "Log out of Claude Code"}}, probeErr: errors.New("runtime unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewWithAgentResolver(executableFinderFunc(func(name string) (string, error) { return "/test/bin/" + name, nil }), nil, terminals, t.TempDir())
			t.Cleanup(svc.Close)
			_, err := svc.StartLogin(t.Context(), "claude-code", "setup_token", "sk-ant-oat01-unchecked-terminal")
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_TERMINAL_UNKNOWN" {
				t.Fatalf("expected an inconclusive check to refuse login: %v", err)
			}
		})
	}
}
