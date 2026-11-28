package piacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// steerHarness loads the extension into a fake Pi, the way pi-acp does: no
// session_start, a context only once a turn starts. Each load imports a fresh
// copy of the module, like a second Pi session in the same process. It prints
// one JSON line of results.
const steerHarness = `import { pathToFileURL } from "node:url";
import { execFileSync } from "node:child_process";
import { createConnection } from "node:net";
import { existsSync } from "node:fs";
import { join } from "node:path";
const socket = join(process.env.AO_PI_STEER_DIR, "ao-steer.sock");
// raw keeps its side open after the answer, so every part reaches the server.
const raw = (...parts) => new Promise((resolve, reject) => {
  const conn = createConnection({ path: socket, allowHalfOpen: true });
  let data = "";
  conn.on("data", (chunk) => { data += chunk; });
  conn.on("error", reject);
  const ended = new Promise((r) => conn.on("end", r));
  (async () => {
    for (const part of parts) {
      conn.write(part);
      await new Promise((r) => setTimeout(r, 30));
    }
    await ended;
    conn.end();
    resolve(data.trim().split("\n").map((line) => JSON.parse(line)));
  })().catch(reject);
});
const href = pathToFileURL(process.argv[2]).href;
const mode = process.argv[3];
let loads = 0;
const load = async (id) => {
  const handlers = new Map();
  const sent = [];
  const loaded = await import(href + "?load=" + ++loads);
  loaded.default({
    on(name, handler) { handlers.set(name, handler); },
    sendMessage(message, options) { sent.push({ message, options }); },
  });
  const session = { idle: true, sent };
  session.ctx = { sessionManager: { getSessionId() { return id; } }, isIdle() { return session.idle; } };
  session.emit = (name, event) => handlers.get(name)(event ?? {}, session.ctx);
  return session;
};
const steer = (sessionId, text) => new Promise((resolve, reject) => {
  const conn = createConnection(socket);
  let data = "";
  conn.on("data", (chunk) => { data += chunk; });
  conn.on("end", () => resolve(JSON.parse(data)));
  conn.on("error", reject);
  conn.write(JSON.stringify({ sessionId, text }) + "\n");
});
const settle = (ms) => new Promise((r) => setTimeout(r, ms));
const r = {};
const a = await load("pi-1");
r.envAfterLoad = process.env.AO_PI_STEER_DIR ?? null;
r.childEnv = JSON.parse(execFileSync(process.execPath, ["-e", "console.log(JSON.stringify(process.env.AO_PI_STEER_DIR ?? null))"], { encoding: "utf8" }));
// The socket answers from load, before any event.
const answers = () => new Promise((resolve) => {
  const conn = createConnection(socket);
  conn.on("connect", () => { conn.destroy(); resolve(true); });
  conn.on("error", () => resolve(false));
});
for (let i = 0; i < 50 && !(await answers()); i++) await settle(20);
r.socketAtLoad = await answers();
if (mode === "owned") {
  a.idle = false;
  await a.emit("agent_start");
  r.owner = await steer("pi-1", "use staging");
  r.sent = a.sent.length;
} else {
  r.beforeTurn = await steer("pi-1", "early");
  a.idle = false;
  await a.emit("agent_start");
  r.busy = await steer("pi-1", "use staging");
  r.message = a.sent[0];
  const b = await load("pi-2");
  b.idle = false;
  await b.emit("turn_start");
  r.second = await steer("pi-2", "for the second session");
  r.sentA = a.sent.length;
  r.sentB = b.sent.length;
  // One multi-byte character split across two chunks.
  const line = Buffer.from(JSON.stringify({ sessionId: "pi-2", text: "नमस्ते" }) + "\n");
  const cut = line.indexOf(0xe0) + 1;
  r.split = await raw(line.subarray(0, cut), line.subarray(cut));
  r.splitText = b.sent.at(-1).message.content;
  // Two requests on one connection: only the first is taken.
  const before = b.sent.length;
  const one = JSON.stringify({ sessionId: "pi-2", text: "once" }) + "\n";
  r.twice = await raw(one, one);
  r.twiceSent = b.sent.length - before;
  r.tooLong = await raw("x".repeat(9 * 1024 * 1024));
  a.idle = true;
  r.idle = await steer("pi-1", "too late");
  r.blank = await steer("pi-2", "  ");
  a.idle = false;
  await a.emit("session_shutdown", { reason: "reload" });
  r.afterShutdown = await steer("pi-1", "gone");
  r.sentAAfter = a.sent.length;
}
console.log(JSON.stringify(r));
process.exit(0);
`

// runSteerHarness runs steerHarness in Node with AO_PI_STEER_DIR set to dir
// and decodes its result line.
func runSteerHarness(t *testing.T, dir, mode string, result any) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the extension does not steer on Windows")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute the steering extension")
	}
	fixture := t.TempDir()
	// The extension is plain JavaScript, so Node runs it as it is.
	module := filepath.Join(fixture, "ao-steer.mjs")
	if err := os.WriteFile(module, steerExtension, 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(fixture, "harness.mjs")
	if err := os.WriteFile(harness, []byte(steerHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), node, harness, module, mode)
	cmd.Env = append(os.Environ(), "AO_PI_STEER_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("extension harness failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), result); err != nil {
		t.Fatalf("decode harness output %q: %v", out, err)
	}
}

func TestSteerExtensionSteersThroughItsSocket(t *testing.T) {
	dir := shortDir(t)
	// A socket file left by a Pi process that died is replaced.
	stale, err := net.Listen("unix", steerSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	var got struct {
		SocketAtLoad                          bool
		EnvAfterLoad, ChildEnv                *string
		BeforeTurn, Busy, Second, Idle, Blank map[string]string
		AfterShutdown                         map[string]string
		Split, Twice, TooLong                 []map[string]string
		SplitText                             string
		SentA, SentB, SentAAfter, TwiceSent   int
		Message                               struct {
			Message map[string]any `json:"message"`
			Options map[string]any `json:"options"`
		}
	}
	runSteerHarness(t, dir, "all", &got)
	if !got.SocketAtLoad {
		t.Fatal("the steering socket was not up once the extension loaded")
	}
	if got.EnvAfterLoad != nil || got.ChildEnv != nil {
		t.Fatal("a process started from Pi inherits AO_PI_STEER_DIR, so a nested pi could take over the session's socket")
	}
	if got.BeforeTurn["outcome"] != "promptRequired" {
		t.Fatalf("steer before the first turn = %v, want promptRequired", got.BeforeTurn)
	}
	if got.Busy["outcome"] != "injected" || got.Second["outcome"] != "injected" || got.SentA != 1 || got.SentB != 1 {
		t.Fatalf("busy steers = %v, %v with %d and %d sent, want each delivered to its own session", got.Busy, got.Second, got.SentA, got.SentB)
	}
	if got.Message.Options["deliverAs"] != "steer" || got.Message.Message["content"] != "use staging" || got.Message.Message["customType"] != "ao-steer" {
		t.Fatalf("sent message = %+v, want the text delivered as a steer", got.Message)
	}
	if got.SplitText != "नमस्ते" {
		t.Fatalf("a character split across chunks arrived as %q", got.SplitText)
	}
	if len(got.Twice) != 1 || got.TwiceSent != 1 {
		t.Fatalf("two requests on one connection: %d answers and %d steers, want one of each", len(got.Twice), got.TwiceSent)
	}
	if len(got.TooLong) != 1 || got.TooLong[0]["outcome"] != "error" {
		t.Fatalf("an endless request = %v, want a refusal", got.TooLong)
	}
	if got.Idle["outcome"] != "promptRequired" || got.Blank["outcome"] != "error" {
		t.Fatalf("idle steer = %v, blank steer = %v, want promptRequired and a refusal", got.Idle, got.Blank)
	}
	if got.AfterShutdown["outcome"] != "promptRequired" || got.SentAAfter != 1 {
		t.Fatalf("steer after shutdown = %v with %d sent, want promptRequired and nothing sent", got.AfterShutdown, got.SentAAfter)
	}
	if _, err := os.Stat(steerSocketPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the steering socket outlived its process: %v", err)
	}
	if strings.Contains(string(steerExtension), "sendUserMessage(") {
		t.Fatal("the extension uses sendUserMessage, which starts a turn when Pi is idle")
	}
}

// A Pi process AO didn't launch loads the file too, when it runs in the same
// workspace. It must report nothing and open nothing.
func TestSteerExtensionIsInertWithoutTheLaunchEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the extension does not steer on Windows")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute the steering extension")
	}
	fixture := t.TempDir()
	module := filepath.Join(fixture, "ao-steer.mjs")
	if err := os.WriteFile(module, steerExtension, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `import(process.argv[1]).then((m) => { const on = []; m.default({ on: (name) => on.push(name) }); console.log(JSON.stringify(on)); })`
	cmd := exec.CommandContext(context.Background(), node, "-e", script, module)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, steerDirEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "[]" {
		t.Fatalf("a pi AO didn't launch registered handlers %s, want none", got)
	}
}

// A second live Pi process with the same folder must not take the socket over.
func TestSteerExtensionLeavesALiveSocketAlone(t *testing.T) {
	dir := shortDir(t)
	owner, err := net.Listen("unix", steerSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	go func() {
		for {
			conn, err := owner.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_, _ = conn.Write([]byte(`{"outcome":"owner"}` + "\n"))
			_ = conn.Close()
		}
	}()
	var got struct {
		SocketAtLoad bool
		Owner        map[string]string
		Sent         int
	}
	runSteerHarness(t, dir, "owned", &got)
	if got.Owner["outcome"] != "owner" || got.Sent != 0 {
		t.Fatalf("steer = %v with %d sent, want the existing owner to answer", got.Owner, got.Sent)
	}
	if _, err := os.Stat(steerSocketPath(dir)); err != nil {
		t.Fatalf("the owner's socket was removed: %v", err)
	}
}

func gitStatus(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "git", "-C", dir, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return string(out)
}

// The extension never shows as a change, whatever ignore files the repository
// keeps under .pi, and those files are left as they are.
func TestInstallSteerExtensionKeepsTheWorktreeClean(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no .pi folder":                   {"README.md": "x"},
		"committed .pi/.gitignore":        {".pi/.gitignore": "npm/\n"},
		"committed .pi that un-ignores":   {".pi/.gitignore": "!extensions/**\n", ".gitignore": "!.pi/**\n"},
		"committed extensions .gitignore": {".pi/extensions/.gitignore": "*.local.ts\n", ".pi/extensions/own.ts": "export default () => {}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			for path, body := range files {
				full := filepath.Join(repo, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x"}} {
				if out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			// Installing twice, as a resume does, changes nothing.
			for range 2 {
				if err := installSteerExtension(repo); err != nil {
					t.Fatal(err)
				}
			}
			if data, err := os.ReadFile(filepath.Join(steerExtensionDir(repo), "index.ts")); err != nil || string(data) != string(steerExtension) {
				t.Fatalf("installed extension = %d bytes, %v", len(data), err)
			}
			if got := gitStatus(t, repo); got != "" {
				t.Fatalf("the worktree is dirty after the install:\n%s", got)
			}
		})
	}
}

func TestInstallSteerExtensionLeavesOtherFilesAlone(t *testing.T) {
	for _, name := range []string{"index.ts", ".gitignore"} {
		workspace := t.TempDir()
		path := filepath.Join(steerExtensionDir(workspace), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := installSteerExtension(workspace); err == nil {
			t.Fatalf("installed over a user's %s", name)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "mine\n" {
			t.Fatalf("user's %s = %q, %v", name, data, err)
		}
	}
}

// Chat launches get the folder to steer through; a launch whose extension
// could not be installed doesn't, and still starts.
func TestLaunchOffersTheSteerFolderOnlyWithTheExtensionInstalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Pi does not steer on Windows")
	}
	binary := filepath.Join(t.TempDir(), "pi-acp")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := buildConfig(nil, func(context.Context) (string, error) { return binary, nil }, nil)
	cfg := acpdriver.LaunchConfig{SessionID: "proj-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir()}
	launch, err := config.Launch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := launch.Env[steerDirEnv]; got != sessionRunDir(cfg) {
		t.Fatalf("steer folder = %q, want %q", got, sessionRunDir(cfg))
	}
	// The activity extension stays hidden from Pi's discovery, as before.
	if _, err := os.Stat(filepath.Join(cfg.WorkspacePath, ".pi", ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the launch wrote .pi/.gitignore (err=%v)", err)
	}

	blocked := acpdriver.LaunchConfig{SessionID: "proj-2", DataDir: t.TempDir(), WorkspacePath: t.TempDir()}
	path := filepath.Join(steerExtensionDir(blocked.WorkspacePath), "index.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launch, err = config.Launch(context.Background(), blocked)
	if err != nil {
		t.Fatalf("launch with a user's extension in the way: %v", err)
	}
	if got, ok := launch.Env[steerDirEnv]; ok {
		t.Fatalf("steer folder = %q without the extension, want none", got)
	}
}

func TestSteerExtensionGitignoreIsMarkedAsOrchestrators(t *testing.T) {
	workspace := t.TempDir()
	if err := installSteerExtension(workspace); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(steerExtensionDir(workspace), ".gitignore"))
	if err != nil || !strings.Contains(string(data), hookutil.GitignoreSentinel) || !strings.Contains(string(data), "/index.ts") {
		t.Fatalf(".gitignore = %q, %v", data, err)
	}
}
