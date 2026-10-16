package workerexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestSecretFileReference(t *testing.T) {
	blocked := map[string]string{
		"cat .env":                                       ".env",
		"head -1 ./.env":                                 ".env",
		`cat "$PWD/.env"`:                                ".env",
		"set -a; . ./.env; echo $SECRET":                 ".env",
		"source .env && env":                             ".env",
		`python3 -c "print(open('.env').read())"`:        ".env",
		"node -e \"require('fs').readFileSync('.env')\"": ".env",
		"cat ../other/.env.local":                        ".env.local",
		"grep KEY .env.production":                       ".env.production",
		"docker compose --env-file=.env up":              ".env",
		"cat .env.example .env":                          ".env",
		"cat .env.example.bak":                           ".env.example.bak",
	}
	for command, want := range blocked {
		if name, found := secretFileReference(command); !found || name != want {
			t.Errorf("%q: got %q, %v; want %q", command, name, found, want)
		}
	}
	allowed := []string{
		"make db-host",
		"cat .env.example",
		"cp .env.sample /tmp/x && cat .env.template",
		"direnv allow .envrc",
		"cat prod.env",
		"echo $ENVIRONMENT; ls .environment",
		"env | sort",
		"cat .env.",
		"",
	}
	for _, command := range allowed {
		if name, found := secretFileReference(command); found {
			t.Errorf("%q was blocked as %q", command, name)
		}
	}
}

func TestRunSecretFileGuard(t *testing.T) {
	run := func(payload string) (int, string) {
		var stderr bytes.Buffer
		code := RunSecretFileGuard(strings.NewReader(payload), &stderr)
		return code, stderr.String()
	}
	// Claude Code's Bash payload; the description alone never blocks.
	code, message := run(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"python3 -c \"open('.env')\"","description":"Read .env"}}`)
	if code != 2 || !strings.Contains(message, "Blocked by AO") || !strings.Contains(message, `".env"`) {
		t.Fatalf("Claude read: code %d, %q", code, message)
	}
	if code, _ := run(`{"tool_name":"Bash","tool_input":{"command":"make db-host","description":"Load .env through make"}}`); code != 0 {
		t.Fatalf("make target blocked: %d", code)
	}
	// Codex may send argv.
	if code, _ := run(`{"tool_name":"Bash","tool_input":{"command":["bash","-lc","cat .env"]}}`); code != 2 {
		t.Fatalf("Codex argv read: %d", code)
	}
	if code, _ := run(`{"tool_name":"Bash","tool_input":{"cmd":"source .env"}}`); code != 2 {
		t.Fatalf("cmd field read: %d", code)
	}
	// Codex's apply_patch: only the files the patch touches count.
	if code, _ := run(`{"tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: .gitignore\n+.env\n*** Add File: app.py\n+load_dotenv(\".env\")\n*** End Patch\n"}}`); code != 0 {
		t.Fatalf("a patch mentioning .env was blocked: %d", code)
	}
	for _, header := range []string{"*** Add File: .env", "*** Update File: config/.env.local", "*** Delete File: .env", `*** Update File: a.txt\n*** Move to: .env`} {
		if code, _ := run(`{"tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n` + header + `\n+X=1\n*** End Patch\n"}}`); code != 2 {
			t.Fatalf("patch %q was allowed", header)
		}
	}
	for _, payload := range []string{
		"", "not json",
		`{"tool_name":"Read","tool_input":{"file_path":".env"}}`,
		`{"tool_name":"mcp__deploy__run","tool_input":{"command":"deploy with .env"}}`,
	} {
		if code, _ := run(payload); code != 0 {
			t.Fatalf("%q: %d", payload, code)
		}
	}
}

func TestApplySecretFileGuardHook(t *testing.T) {
	own := map[string]any{"matcher": "Edit", "hooks": []any{map[string]any{"type": "command", "command": "lint"}}}
	settings := map[string]any{"hooks": map[string]any{"PreToolUse": []any{own}}}
	guard := "/usr/local/bin/ao-worker " + SecretFileGuardArg
	applySecretFileGuardHook(settings, guard)
	applySecretFileGuardHook(settings, guard)
	groups := settings["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 2 || !isSecretFileGuardGroup(groups[1]) || groups[1].(map[string]any)["matcher"] != "Bash" {
		t.Fatalf("PreToolUse = %v", groups)
	}
	// A healed worker moves the binary; the old entry is replaced, not kept.
	applySecretFileGuardHook(settings, "/home/ubuntu/.ao/bin/ao-worker "+SecretFileGuardArg)
	groups = settings["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 2 || !strings.HasPrefix(groups[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string), "/home/ubuntu/") {
		t.Fatalf("PreToolUse after move = %v", groups)
	}
	applySecretFileGuardHook(settings, "")
	groups = settings["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 1 || isSecretFileGuardGroup(groups[0]) {
		t.Fatalf("PreToolUse after opting out = %v", groups)
	}
	empty := map[string]any{}
	applySecretFileGuardHook(empty, "")
	if len(empty) != 0 {
		t.Fatalf("an unprotected session gained settings: %v", empty)
	}
}

// Claude's terminal and Chat, and Codex's terminal, all run the guard on their
// shell tool when secret files are protected.
func TestSecretFileGuardReachesClaudeAndCodex(t *testing.T) {
	guardBinary := "/opt/ao/ao-worker " + SecretFileGuardArg
	guard := "env -u AO_CLOUD_PUBLIC_URL " + guardBinary
	claudeLaunch := worker.LaunchContext{SessionID: "session-1", Kind: "worker", Harness: "claude-code", Mode: "trusted"}
	claudeCredential := worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}

	terminal, err := HarnessBuilder{DataDir: t.TempDir(), Launch: claudeLaunch, ProtectSecretFiles: true, SecretFileGuard: guardBinary}.
		BuildInteractive(claudeLaunch, claudeCredential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := readClaudeGuard(t, terminal.Env["CLAUDE_CONFIG_DIR"]); got != guard {
		t.Fatalf("terminal guard = %q", got)
	}
	chat, err := HarnessBuilder{DataDir: t.TempDir(), Launch: claudeLaunch, ProtectSecretFiles: true, SecretFileGuard: guardBinary}.
		Build(context.Background(), worker.Turn{Harness: "claude-code", Mode: "trusted", Prompt: "hi"}, claudeCredential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := readClaudeGuard(t, chat.Env["CLAUDE_CONFIG_DIR"]); got != guard {
		t.Fatalf("chat guard = %q", got)
	}
	off, err := HarnessBuilder{DataDir: t.TempDir(), Launch: claudeLaunch, SecretFileGuard: guardBinary}.
		BuildInteractive(claudeLaunch, claudeCredential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := readClaudeGuard(t, off.Env["CLAUDE_CONFIG_DIR"]); got != "" {
		t.Fatalf("unprotected guard = %q", got)
	}

	codexLaunch := worker.LaunchContext{SessionID: "session-1", Kind: "worker", Harness: "codex", Mode: "trusted"}
	codexCredential := worker.CredentialResponse{Provider: "codex", CredentialType: "auth_json", Secret: "{}"}
	codex, err := HarnessBuilder{DataDir: t.TempDir(), Launch: codexLaunch, ProtectSecretFiles: true, SecretFileGuard: guardBinary}.
		BuildInteractive(codexLaunch, codexCredential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := `hooks.PreToolUse=[{hooks=[{type="command",command="` + guard + `",timeout=5}]}]`
	if !slices.Contains(codex.Args, want) || !slices.Contains(codex.Args, "--dangerously-bypass-hook-trust") {
		t.Fatalf("codex args = %v", codex.Args)
	}
	plainCodex, err := HarnessBuilder{DataDir: t.TempDir(), Launch: codexLaunch, SecretFileGuard: guardBinary}.
		BuildInteractive(codexLaunch, codexCredential, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(plainCodex.Args, func(arg string) bool { return strings.HasPrefix(arg, "hooks.PreToolUse") }) {
		t.Fatalf("unprotected codex args = %v", plainCodex.Args)
	}
}

func readClaudeGuard(t *testing.T, configDir string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatal(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks["PreToolUse"].([]any)
	for _, group := range groups {
		if isSecretFileGuardGroup(group) {
			return group.(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
		}
	}
	return ""
}

// AO's hook settings in the session's checkout stay out of git status, also
// when the checkout is a linked worktree whose info/exclude lives in the
// common git dir.
func TestAgentHookFilesStayOutOfGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return string(output)
	}
	repo := t.TempDir()
	gitIn(repo, "init", "-q")
	gitIn(repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "linked")
	gitIn(repo, "worktree", "add", "-q", worktree)

	for _, workspace := range []string{repo, worktree} {
		claudeLaunch := worker.LaunchContext{SessionID: "session-1", Kind: "worker", Harness: "claude-code", Mode: "trusted"}
		if _, err := (HarnessBuilder{DataDir: t.TempDir(), Launch: claudeLaunch}).BuildInteractive(
			claudeLaunch, worker.CredentialResponse{Provider: "claude-code", CredentialType: "api_key", Secret: "secret"}, workspace,
		); err != nil {
			t.Fatal(err)
		}
		if err := installCursorActivityHooks("/usr/local/bin/ao", workspace); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{".claude/settings.local.json", ".cursor/hooks.json"} {
			if _, err := os.Stat(filepath.Join(workspace, path)); err != nil {
				t.Fatalf("%s was not written: %v", path, err)
			}
		}
		if status := gitIn(workspace, "status", "--porcelain", "--untracked-files=all"); status != "" {
			t.Fatalf("%s: git status shows AO files:\n%s", workspace, status)
		}
		// A second launch adds nothing.
		if err := installCursorActivityHooks("/usr/local/bin/ao", workspace); err != nil {
			t.Fatal(err)
		}
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(exclude), "/.claude/settings.local.json\n"); count != 1 {
		t.Fatalf("exclude lists the Claude settings %d times:\n%s", count, exclude)
	}
	// A working directory below the checkout root excludes its own files.
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installCursorActivityHooks("/usr/local/bin/ao", sub); err != nil {
		t.Fatal(err)
	}
	if status := gitIn(repo, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("subdirectory hooks show in git status:\n%s", status)
	}
	// A user file in .claude still shows.
	if err := os.WriteFile(filepath.Join(repo, ".claude", "notes.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := gitIn(repo, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(status, ".claude/notes.md") {
		t.Fatalf("a user file was hidden:\n%s", status)
	}
}

// Codex Chat trusts the guard in its app-server, the way Codex's hook review
// does. Needs a real Codex binary (no login): AO_TEST_CODEX_BINARY=$(which codex).
func TestCodexAppServerTrustsSecretFileGuard(t *testing.T) {
	binary := os.Getenv("AO_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("AO_TEST_CODEX_BINARY is not set")
	}
	guard := "/opt/ao/ao-worker " + SecretFileGuardArg
	// Codex keeps writing under CODEX_HOME briefly after it is killed, which
	// t.TempDir's strict cleanup reports as a failure.
	cwd, err := os.MkdirTemp("", "ao-codex-guard-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })
	if err := os.MkdirAll(filepath.Join(cwd, ".codex-home"), 0o700); err != nil {
		t.Fatal(err)
	}
	listGuard := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		process := exec.CommandContext(ctx, binary, append(codexSecretFileGuardArgs(guard), "app-server")...)
		process.Dir = cwd
		process.Env = append(os.Environ(), "CODEX_HOME="+filepath.Join(cwd, ".codex-home"))
		stdin, _ := process.StdinPipe()
		stdout, _ := process.StdoutPipe()
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
		conn := newCodexRPC(stdin, stdout)
		if err := conn.request(ctx, "initialize", map[string]any{
			"clientInfo": map[string]string{"name": "ao-test", "title": "AO test", "version": "1"},
		}, nil); err != nil {
			t.Fatal(err)
		}
		if err := conn.notify("initialized", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		var listed struct {
			Data []struct {
				Hooks []struct {
					Command     string `json:"command"`
					TrustStatus string `json:"trustStatus"`
				} `json:"hooks"`
			} `json:"data"`
		}
		if err := conn.request(ctx, "hooks/list", map[string]any{"cwds": []string{cwd}}, &listed); err != nil {
			t.Fatal(err)
		}
		status := ""
		for _, entry := range listed.Data {
			for _, hook := range entry.Hooks {
				if hook.Command == guard {
					status = hook.TrustStatus
				}
			}
		}
		if status != "trusted" {
			if err := trustCodexSecretFileGuard(ctx, conn, cwd, guard); err != nil {
				t.Fatal(err)
			}
		}
		return status
	}
	if status := listGuard(); status != "untrusted" {
		t.Fatalf("first launch: guard is %q", status)
	}
	if status := listGuard(); status != "trusted" {
		t.Fatalf("after trusting: guard is %q", status)
	}
}

// fakeCodexAppServer answers each request line with respond(method).
func fakeCodexAppServer(t *testing.T, respond func(method string) string) *codexRPC {
	t.Helper()
	requestsReader, requestsWriter := io.Pipe()
	responsesReader, responsesWriter := io.Pipe()
	t.Cleanup(func() { _ = requestsWriter.Close(); _ = responsesWriter.Close() })
	go func() {
		scanner := bufio.NewScanner(requestsReader)
		for scanner.Scan() {
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(scanner.Bytes(), &request) != nil {
				continue
			}
			_, _ = fmt.Fprintf(responsesWriter, `{"id":%d,%s}`+"\n", request.ID, respond(request.Method))
		}
	}()
	return newCodexRPC(requestsWriter, responsesReader)
}

// A Codex without session hooks reports errCodexHooksUnsupported, which Chat
// logs and runs without the guard; other failures still fail the turn.
func TestTrustCodexSecretFileGuardOnACodexWithoutHooks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	guard := "env -u AO_CLOUD_PUBLIC_URL /opt/ao/ao-worker " + SecretFileGuardArg

	noMethod := fakeCodexAppServer(t, func(string) string {
		return `"error":{"code":-32601,"message":"method not found"}`
	})
	if err := trustCodexSecretFileGuard(ctx, noMethod, "/w", guard); !errors.Is(err, errCodexHooksUnsupported) {
		t.Fatalf("no hooks/list: err = %v", err)
	}

	notLoaded := fakeCodexAppServer(t, func(string) string { return `"result":{"data":[{"hooks":[]}]}` })
	if err := trustCodexSecretFileGuard(ctx, notLoaded, "/w", guard); !errors.Is(err, errCodexHooksUnsupported) {
		t.Fatalf("guard not loaded: err = %v", err)
	}

	broken := fakeCodexAppServer(t, func(string) string {
		return `"error":{"code":-32603,"message":"internal error"}`
	})
	if err := trustCodexSecretFileGuard(ctx, broken, "/w", guard); err == nil || errors.Is(err, errCodexHooksUnsupported) {
		t.Fatalf("internal error: err = %v", err)
	}
}
