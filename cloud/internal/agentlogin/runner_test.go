package agentlogin

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
	"syscall"
	"testing"
	"time"
)

const fixtureAttempt = "d855bbec-c4f4-40ce-a730-39add5fa2506"
const fixtureToken = "sk-ant-oat01-fixture-abcdefghijklmnopqrstuvwxyz0123456789"
const fixtureClaudeURL = "https://claude.com/cai/oauth/authorize?client_id=stock-cli&code=true&code_challenge=fixture&code_challenge_method=S256&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&response_type=code&scope=user%3Ainference&state=fixture"

type fixtureControl struct {
	mu            sync.Mutex
	harness       string
	status        string
	code          string
	expires       time.Time
	events        []progress
	requestErr    string
	stopOnWaiting string
	codeToSubmit  string
	pollInterval  time.Duration
	process       *exec.Cmd
}

func (f *fixtureControl) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.URL.Path != "/api/cloud/v1/agent-logins/"+fixtureAttempt+"/runner" || req.Header.Get("Authorization") != "Bearer attempt-only-token" {
		f.requestErr = "wrong scoped runner request"
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if req.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{"harness": f.harness, "status": f.status, "expiresAt": f.expires, "code": f.code})
		f.code = ""
		return
	}
	var event progress
	if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&event) != nil {
		f.requestErr = "invalid progress request"
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.events = append(f.events, event)
	if event.Status == "waiting" && f.stopOnWaiting != "" {
		f.status = f.stopOnWaiting
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if event.Status == "waiting" && f.harness == "claude-code" {
		f.code = "one-time-browser-code#state"
		if f.codeToSubmit != "" {
			f.code = f.codeToSubmit
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Terminal control bytes would be interpreted by the PTY instead of submitted
// as an OAuth code. Reject them before acknowledging or writing input.
func TestNativeLoginRejectsTerminalControlInCode(t *testing.T) {
	for _, code := range []string{"code\x03#state", "code\x04#state", "code\t#state", "code\x7f#state"} {
		t.Run(fmt.Sprintf("%x", code), func(t *testing.T) {
			err, events, _ := runFixture(t, "claude-code", "success", &fixtureControl{codeToSubmit: code})
			if err == nil {
				t.Fatal("terminal control was accepted")
			}
			for _, event := range events {
				if event.Status == "connecting" || event.Status == "connected" {
					t.Fatal("terminal control reached native login input")
				}
			}
		})
	}
}

// Allowing arbitrary CLI output, an empty document or a credential symlink to
// become connected would expose unrelated secrets or promote invalid auth.
func TestNativeLoginRejectsUnsafeCompletion(t *testing.T) {
	for _, mode := range []string{"malicious-url", "empty-auth", "symlink-auth", "failure", "too-much-output"} {
		t.Run(mode, func(t *testing.T) {
			err, events, root := runFixture(t, "codex", mode, &fixtureControl{})
			if err == nil {
				t.Fatal("unsafe completion accepted")
			}
			if strings.Contains(err.Error(), "unrelated-secret") || strings.Contains(err.Error(), fixtureToken) {
				t.Fatal("CLI output leaked through error")
			}
			for _, e := range events {
				if e.Status == "connected" || strings.Contains(e.AuthorizationURL, "evil") {
					t.Fatalf("unsafe event: %#v", e)
				}
			}
			if len(events) == 0 || events[len(events)-1].Status != "failed" {
				t.Fatal("active failed login did not publish sanitized failure")
			}
			entries, readErr := os.ReadDir(root)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("unclean sandbox: %v %v", entries, readErr)
			}
		})
	}
}

// Removing control-plane polling would leave a native process alive after the
// person cancels or the attempt expires, and could promote its late result.
func TestNativeLoginStopsTerminalAttempts(t *testing.T) {
	for _, status := range []string{"cancelled", "expired"} {
		t.Run(status, func(t *testing.T) {
			started := time.Now()
			err, events, root := runFixture(t, "codex", "hang", &fixtureControl{stopOnWaiting: status})
			if err == nil || !strings.Contains(err.Error(), "attempt has ended") || time.Since(started) > 2*time.Second {
				t.Fatalf("terminal attempt did not stop promptly: %v", err)
			}
			for _, e := range events {
				if e.Status == "connected" {
					t.Fatal("terminal attempt promoted credentials")
				}
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatal("cancelled sandbox retained")
			}
		})
	}
}

func TestRunnerConfigRejectsUnsafeTransport(t *testing.T) {
	base := Config{BaseURL: "https://cloud.example", AttemptID: fixtureAttempt, Token: "attempt-only-token", DataDir: t.TempDir()}
	for _, raw := range []string{"http://cloud.example", "https://user:pass@cloud.example", "https://cloud.example?token=x", "https://cloud.example/#fragment", "https://cloud.example/api", "http://127.0.0.1:8000"} {
		config := base
		config.BaseURL = raw
		if _, err := newRunner(config, false); err == nil {
			t.Errorf("accepted unsafe base URL %s", raw)
		}
	}
	base.AttemptID = "../another-attempt"
	if _, err := newRunner(base, false); err == nil {
		t.Fatal("accepted path traversal in attempt id")
	}
}

// Following a redirect could disclose the attempt bearer to another endpoint;
// copying an upstream error body would disclose server credentials to logs.
func TestRunnerDoesNotFollowRedirectsOrExposeServerBodies(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			forwarded := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true; w.WriteHeader(204) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
				fmt.Fprint(w, "server-secret-body")
			}))
			defer server.Close()
			r, err := newRunner(Config{BaseURL: server.URL, AttemptID: fixtureAttempt, Token: "attempt-only-token", DataDir: t.TempDir()}, true)
			if err != nil {
				t.Fatal(err)
			}
			err = r.run(context.Background())
			if err == nil || strings.Contains(err.Error(), "server-secret-body") || forwarded {
				t.Fatalf("unsafe HTTP behavior: %v forwarded=%v", err, forwarded)
			}
		})
	}
}

func TestLoginParserRejectsUnsafeAuthorizationURLs(t *testing.T) {
	for _, raw := range []string{
		"https://auth.openai.com.evil.example/codex/device",
		"https://evil.example@auth.openai.com/codex/device",
		"https://auth.openai.com:8080/codex/device",
		"https://auth.openai.com/codex/device?token=secret",
		"https://auth.openai.com/codex/device#secret",
		"http://auth.openai.com/codex/device",
	} {
		p := outputParser{harness: "codex"}
		_ = p.add([]byte(raw + "\nABCD-12345\n"))
		if event, ok := p.waiting(); ok {
			t.Errorf("unsafe URL emitted: %#v", event)
		}
	}
	for _, raw := range []string{
		fixtureClaudeURL + "&token=secret",
		strings.Replace(fixtureClaudeURL, "platform.claude.com", "evil.example", 1),
		strings.Replace(fixtureClaudeURL, "code=true", "code=false", 1),
		strings.Replace(fixtureClaudeURL, "platform.claude.com", "localhost", 1),
	} {
		p := outputParser{harness: "claude-code"}
		_ = p.add([]byte(raw + "\n"))
		if event, ok := p.waiting(); ok {
			t.Errorf("unsafe Claude URL emitted: %#v", event)
		}
	}
}

// A valid-looking prefix at a chunk boundary must not publish a truncated
// device code; terminal hyperlinks and unfinished controls are not displayed text.
func TestLoginParserWaitsForCompleteVisibleCode(t *testing.T) {
	p := outputParser{harness: "codex"}
	_ = p.add([]byte("https://auth.openai.com/codex/device\nABCD-1234"))
	if _, ok := p.waiting(); ok {
		t.Fatal("partial device code was published")
	}
	_ = p.add([]byte("5\n"))
	if event, ok := p.waiting(); !ok || event.UserCode != "ABCD-12345" {
		t.Fatal("complete device code was not published")
	}
	for _, control := range []string{"\x1b]8;;https://auth.openai.com/codex/device\nABCD-12345\n", "\x1b]8;;https://auth.openai.com/codex/device\nABCD-12345\n\x07"} {
		p := outputParser{harness: "codex"}
		_ = p.add([]byte(control))
		if _, ok := p.waiting(); ok {
			t.Fatal("hidden terminal control was published")
		}
	}
}

// Killing only the immediate launcher leaves its native child running. A real
// subprocess writes its pid so this assertion observes group cancellation.
func TestNativeLoginCancelsProcessGroup(t *testing.T) {
	control := &fixtureControl{harness: "codex", status: "starting", expires: time.Now().Add(time.Minute), stopOnWaiting: "cancelled"}
	server := httptest.NewServer(http.HandlerFunc(control.serve))
	defer server.Close()
	root := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	r, err := newRunner(Config{BaseURL: server.URL, AttemptID: fixtureAttempt, Token: "attempt-only-token", DataDir: root}, true)
	if err != nil {
		t.Fatal(err)
	}
	r.pollInterval = 10 * time.Millisecond
	r.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoginFixtureProcess$", "--", "child-parent", pidFile)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = r.run(ctx); err == nil {
		t.Fatal("cancelled process succeeded")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err = fmt.Sscan(string(data), &pid); err != nil || pid < 1 {
		t.Fatal("missing child pid")
	}
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("native child survived login cancellation")
	}
}

// The provider may complete between polls. The final state check must still
// prevent a cancelled attempt from sending its credential for promotion.
func TestNativeCompletionAfterCancellationIsNotPromoted(t *testing.T) {
	err, events, _ := runFixture(t, "codex", "success", &fixtureControl{stopOnWaiting: "cancelled", pollInterval: time.Minute})
	if err == nil {
		t.Fatal("cancelled native success was accepted")
	}
	for _, event := range events {
		if event.Status == "connected" {
			t.Fatal("cancelled native success was promoted")
		}
	}
}

// A runner cannot rely only on polling: the original expiry bounds the native
// subprocess even when the next scheduled control-plane poll is much later.
func TestNativeLoginHonorsControlPlaneDeadline(t *testing.T) {
	control := &fixtureControl{harness: "codex", status: "starting", expires: time.Now().Add(100 * time.Millisecond)}
	server := httptest.NewServer(http.HandlerFunc(control.serve))
	defer server.Close()
	root := t.TempDir()
	r, err := newRunner(Config{BaseURL: server.URL, AttemptID: fixtureAttempt, Token: "attempt-only-token", DataDir: root}, true)
	if err != nil {
		t.Fatal(err)
	}
	r.pollInterval = time.Minute
	r.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestLoginFixtureProcess$", "--", "hang", name}, args...)...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err = r.run(ctx)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("native login ignored its expiry: %v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("expired login home retained")
	}
}

func runFixture(t *testing.T, harness, mode string, control *fixtureControl) (error, []progress, string) {
	t.Helper()
	control.harness, control.status, control.expires = harness, "starting", time.Now().Add(time.Minute)
	server := httptest.NewServer(http.HandlerFunc(control.serve))
	defer server.Close()
	root := t.TempDir()
	runner, err := newRunner(Config{BaseURL: server.URL, AttemptID: fixtureAttempt, Token: "attempt-only-token", DataDir: root}, true)
	if err != nil {
		t.Fatal(err)
	}
	runner.pollInterval = 10 * time.Millisecond
	if control.pollInterval > 0 {
		runner.pollInterval = control.pollInterval
	}
	runner.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestLoginFixtureProcess$", "--", mode, name}, args...)...)
		control.process = command
		return command
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = runner.run(ctx)
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.requestErr != "" {
		t.Fatal(control.requestErr)
	}
	return err, append([]progress(nil), control.events...), root
}

// Native login can finish before the CLI exits. Waiting for process exit loses
// valid credentials and forces the person to repeat a successful browser login.
func TestNativeLoginCompletesWhileCLIStaysAlive(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code"} {
		t.Run(harness, func(t *testing.T) {
			control := &fixtureControl{}
			start := time.Now()
			err, events, root := runFixture(t, harness, "linger", control)
			if err != nil {
				t.Fatalf("completed native login waited for CLI exit: %v", err)
			}
			if time.Since(start) > 2*time.Second || len(events) == 0 || events[len(events)-1].Status != "connected" {
				t.Fatal("ready credential did not promptly connect")
			}
			if control.process == nil || control.process.ProcessState == nil || !control.process.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
				t.Fatal("lingering native CLI was not killed and reaped")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("completed login retained its private home")
			}
		})
	}
}

func TestNativeLoginRejectsProcessFailureBeforeCredential(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code"} {
		t.Run(harness, func(t *testing.T) {
			err, events, _ := runFixture(t, harness, "failure", &fixtureControl{})
			if err == nil {
				t.Fatal("failed native process connected")
			}
			for _, event := range events {
				if event.Status == "connected" {
					t.Fatal("native failure promoted a credential")
				}
			}
		})
	}
}

// Readiness polling must not submit a truncated token at a PTY chunk boundary.
func TestClaudeReadinessWaitsForCompleteToken(t *testing.T) {
	p := outputParser{harness: "claude-code"}
	cut := len(fixtureToken) - 5
	_ = p.add([]byte("Your long-lived authentication token (valid for 1 year):\n" + fixtureToken[:cut]))
	if p.token() != "" {
		t.Fatal("incomplete setup token became ready")
	}
	_ = p.add([]byte(fixtureToken[cut:] + "\n"))
	if p.token() != fixtureToken {
		t.Fatal("complete setup token did not become ready")
	}
}

func TestCodexReadinessRetriesIncompleteCredentialDocument(t *testing.T) {
	err, events, _ := runFixture(t, "codex", "partial-linger", &fixtureControl{})
	if err != nil {
		t.Fatalf("partial document prevented valid login: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Status != "connected" || !strings.Contains(events[len(events)-1].Secret, "fixture-access-token") {
		t.Fatal("partial document was promoted")
	}
}

// Root.OpenFile normally follows in-root symlinks, even when the caller adds
// O_NOFOLLOW. The actual open boundary must reject every auth path component.
func TestCodexAuthOpenNeverFollowsSymlinksWithinPrivateRoot(t *testing.T) {
	for _, component := range []string{"auth.json", ".codex"} {
		t.Run(component, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Mkdir(filepath.Join(home, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "real", "auth.json"), []byte("private-other-file"), 0o600); err != nil {
				t.Fatal(err)
			}
			if component == ".codex" {
				if err := os.Symlink("real", filepath.Join(home, ".codex")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../real/auth.json", filepath.Join(home, ".codex", "auth.json")); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(home)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			file, err := openCodexAuthFile(root)
			if err == nil {
				file.Close()
				t.Fatal("credential open followed a private-home symlink")
			}
		})
	}
}

// Removing the native command, PTY, env isolation or real credential collection
// must prevent the authenticated terminal event these tests observe.
func TestNativeLoginFixtures(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code"} {
		t.Run(harness, func(t *testing.T) {
			t.Setenv("AO_AGENT_LOGIN_TOKEN", "must-not-reach-cli")
			t.Setenv("AO_WORKER_BOOTSTRAP_TOKEN", "must-not-reach-cli")
			t.Setenv("ANTHROPIC_API_KEY", "must-not-reach-cli")
			t.Setenv("OPENAI_API_KEY", "must-not-reach-cli")
			t.Setenv("NODE_OPTIONS", "must-not-reach-cli")
			control := &fixtureControl{}
			err, events, root := runFixture(t, harness, "success", control)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) < 2 || events[0].Status != "waiting" || events[len(events)-1].Status != "connected" {
				t.Fatalf("unexpected events: %#v", events)
			}
			last := events[len(events)-1]
			if harness == "codex" {
				if events[0].AuthorizationURL != "https://auth.openai.com/codex/device" || events[0].UserCode != "ABCD-12345" || events[0].RequiresCode || last.CredentialType != "auth_json" || !strings.Contains(last.Secret, "fixture-access-token") {
					t.Fatalf("wrong codex handoff: %#v", events)
				}
			} else {
				if events[0].AuthorizationURL != fixtureClaudeURL || !events[0].RequiresCode || last.CredentialType != "oauth_token" || last.Secret != fixtureToken {
					t.Fatalf("wrong claude handoff: %#v", events)
				}
			}
			for _, e := range events[:len(events)-1] {
				b, _ := json.Marshal(e)
				for _, secret := range []string{fixtureToken, "fixture-access-token", "unrelated-secret", "one-time-browser-code"} {
					if strings.Contains(string(b), secret) {
						t.Fatal("secret leaked in progress")
					}
				}
			}
			files, readErr := os.ReadDir(root)
			if readErr != nil || len(files) != 0 {
				t.Fatalf("auth sandbox not cleaned: %v %v", files, readErr)
			}
		})
	}
}

// This subprocess is a deterministic native CLI boundary: it requires a PTY,
// validates stock command flags and private HOME, and performs real stdin/file IO.
func TestLoginFixtureProcess(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	mode, name := args[0], args[1]
	if mode == "child" {
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "child-parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestLoginFixtureProcess$", "--", "child", "unused")
		if child.Start() != nil {
			os.Exit(48)
		}
		_ = os.WriteFile(name, []byte(fmt.Sprint(child.Process.Pid)), 0o600)
		fmt.Print("https://auth.openai.com/codex/device\nABCD-12345\n")
		_ = child.Wait()
		os.Exit(0)
	}
	if f, err := os.Stdin.Stat(); err != nil || f.Mode()&os.ModeCharDevice == 0 {
		os.Exit(41)
	}
	for _, key := range []string{"AO_AGENT_LOGIN_TOKEN", "AO_WORKER_BOOTSTRAP_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "NODE_OPTIONS"} {
		if os.Getenv(key) != "" {
			os.Exit(42)
		}
	}
	home := os.Getenv("HOME")
	if home == "" || os.Getenv("CODEX_HOME") != filepath.Join(home, ".codex") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") {
		os.Exit(43)
	}
	if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0o700 {
		os.Exit(44)
	}
	if name == "codex" {
		if strings.Join(args[2:], " ") != "-c cli_auth_credentials_store=\"file\" login --device-auth" {
			os.Exit(45)
		}
		fmt.Print("unrelated-secret https://evil.example/steal?secret=unrelated-secret\n")
		if mode != "malicious-url" {
			fmt.Print("\x1b[94mhttps://auth.openai.com/codex/device\x1b[0m\n\n2. Enter this one-time code (expires in 15 minutes)\n   \x1b[94mABCD-12345\x1b[0m\n")
		}
		if mode == "hang" {
			for {
				time.Sleep(time.Second)
			}
		}
		if mode == "failure" {
			fmt.Print(fixtureToken)
			os.Exit(2)
		}
		if mode == "too-much-output" {
			fmt.Print(strings.Repeat("x", 300000))
		}
		time.Sleep(35 * time.Millisecond)
		auth := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
		if mode == "partial-linger" {
			_ = os.WriteFile(auth, []byte(`{"tokens":{"access_token":`), 0o600)
			time.Sleep(60 * time.Millisecond)
		}
		if mode == "symlink-auth" {
			_ = os.Symlink("/dev/null", auth)
		} else {
			content := `{"tokens":{"access_token":"fixture-access-token","refresh_token":"fixture-refresh-token"}}`
			if mode == "empty-auth" {
				content = `{}`
			}
			_ = os.WriteFile(auth, []byte(content), 0o600)
		}
		if mode == "linger" || mode == "partial-linger" {
			for {
				time.Sleep(time.Second)
			}
		}
		fmt.Print("Successfully logged in\n")
	} else {
		if strings.Join(args[2:], " ") != "setup-token" {
			os.Exit(46)
		}
		fmt.Print("Browser didn't open? Use the url below to sign in (c to copy)\n" + fixtureClaudeURL + "\nPaste code here if prompted >")
		buf := make([]byte, 256)
		n, err := os.Stdin.Read(buf)
		if err != nil || strings.TrimSpace(string(buf[:n])) != "one-time-browser-code#state" {
			os.Exit(47)
		}
		if mode == "failure" {
			fmt.Print("unrelated-secret\n")
			os.Exit(2)
		}
		fmt.Print("\nYour long-lived authentication token (valid for 1 year):\n" + fixtureToken + "\nStore this token securely.\n")
		if mode == "linger" {
			for {
				time.Sleep(time.Second)
			}
		}
	}
	_ = mode
	os.Exit(0)
}
