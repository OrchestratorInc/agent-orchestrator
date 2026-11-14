package pi

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
)

func TestPiExtensionOnlyEndsSessionOnQuit(t *testing.T) {
	source := piActivityExtensionSource(true)
	for _, reason := range []string{"reload", "new", "resume", "fork"} {
		if strings.Contains(source, `event.reason === "`+reason+`"`) {
			t.Fatalf("session shutdown reason %q must not end the AO session", reason)
		}
	}
	if !strings.Contains(source, `event.reason === "quit"`) {
		t.Fatal("extension must end the AO session only for quit")
	}
}

func TestParsePiVersion(t *testing.T) {
	for _, tc := range []struct {
		output string
		wantOK bool
		want   piVersion
	}{
		{output: "pi 0.80.6\n", wantOK: true, want: piVersion{0, 80, 6}},
		{output: "@earendil-works/pi-coding-agent 1.2.3", wantOK: true, want: piVersion{1, 2, 3}},
		{output: "dev", wantOK: false},
	} {
		got, ok := parsePiVersion(tc.output)
		if ok != tc.wantOK || got != tc.want {
			t.Fatalf("parsePiVersion(%q) = %v, %v; want %v, %v", tc.output, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestPiAgentSettledSupportedFromBinaryVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{version: "0.80.5", want: false},
		{version: "0.80.6", want: true},
		{version: "0.81.0", want: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			p := &Plugin{resolvedBinary: fakePiBinary(t, tc.version)}
			got, err := p.piAgentSettledSupported(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("settled support for %s = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestPiExtensionStopEventOrderingFollowsProbedSupport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ao executable fixture uses a Unix shebang")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute the Pi extension fixture")
	}

	for _, tc := range []struct {
		name             string
		settledSupported bool
		wantStopFrom     string
	}{
		{name: "legacy", settledSupported: false, wantStopFrom: "agent_end"},
		{name: "settled", settledSupported: true, wantStopFrom: "agent_settled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixtureDir := t.TempDir()
			modulePath := filepath.Join(fixtureDir, "ao-activity.mjs")
			source := strings.NewReplacer(
				`import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";`+"\n", "",
				`function callHookSync(hookName: string, payload: Record<string, unknown>)`, `function callHookSync(hookName, payload)`,
				`function sessionID(ctx: any): string`, `function sessionID(ctx)`,
				`function usagePayload(ctx: any, payload: Record<string, unknown> = {}): Record<string, unknown>`, `function usagePayload(ctx, payload = {})`,
				`export default function (pi: ExtensionAPI)`, `export default function (pi)`,
			).Replace(piActivityExtensionSource(tc.settledSupported))
			if err := os.WriteFile(modulePath, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			capturePath := filepath.Join(fixtureDir, "calls.jsonl")
			if err := os.WriteFile(filepath.Join(fixtureDir, "ao"), []byte(`#!/usr/bin/env node
const fs = require("node:fs");
let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => { input += chunk; });
process.stdin.on("end", () => {
  fs.appendFileSync(process.env.AO_TEST_CAPTURE, JSON.stringify({args: process.argv.slice(2), source: process.env.PI_EVENT_SOURCE, input}) + "\n");
});
`), 0o755); err != nil {
				t.Fatal(err)
			}
			harnessPath := filepath.Join(fixtureDir, "harness.mjs")
			if err := os.WriteFile(harnessPath, []byte(`import { pathToFileURL } from "node:url";
const handlers = new Map();
const loaded = await import(pathToFileURL(process.argv[2]).href);
loaded.default({ on(name, handler) { handlers.set(name, handler); } });
const ctx = { sessionManager: {
  getSessionId() { return "pi-session-1"; },
  getSessionFile() { return "/ao/data/pi/sessions/pi-session-1.jsonl"; }
} };
process.env.PI_EVENT_SOURCE = "session_start";
await handlers.get("session_start")({}, ctx);
process.env.PI_EVENT_SOURCE = "before_agent_start";
await handlers.get("before_agent_start")({ prompt: "fix bug" }, ctx);
process.env.PI_EVENT_SOURCE = "agent_end";
await handlers.get("agent_end")({ messages: [
  { role: "user", content: "fix bug" },
  { role: "assistant", content: [{ type: "thinking", text: "private reasoning" }, { type: "text", text: "Bug fixed." }] },
] }, ctx);
process.env.PI_EVENT_SOURCE = "agent_settled";
await handlers.get("agent_settled")({}, ctx);
process.env.PI_EVENT_SOURCE = "session_shutdown";
await handlers.get("session_shutdown")({ reason: "reload" }, ctx);
process.env.PI_EVENT_SOURCE = "session_shutdown";
await handlers.get("session_shutdown")({ reason: "quit" }, ctx);
`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(context.Background(), node, harnessPath, modulePath)
			cmd.Env = append(os.Environ(), "PATH="+fixtureDir+string(os.PathListSeparator)+os.Getenv("PATH"), "AO_TEST_CAPTURE="+capturePath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pi extension harness failed: %v\n%s", err, output)
			}
			data, err := os.ReadFile(capturePath)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if got := strings.Count(text, `["hooks","pi","stop"]`); got != 1 {
				t.Fatalf("stop hook count = %d, want 1:\n%s", got, text)
			}
			if !strings.Contains(text, `"source":"`+tc.wantStopFrom+`"`) {
				t.Fatalf("stop hook did not come from %s:\n%s", tc.wantStopFrom, text)
			}
			// The run's final assistant text rides on the stop hook, whichever
			// event fires it, and nothing but its text parts is taken.
			if !strings.Contains(text, `last_assistant_message\":\"Bug fixed.\"`) || strings.Contains(text, "private reasoning") {
				t.Fatalf("stop hook did not carry only the final assistant text:\n%s", text)
			}
			if !strings.Contains(text, `["hooks","pi","session-start"]`) || !strings.Contains(text, `["hooks","pi","user-prompt-submit"]`) || !strings.Contains(text, `["hooks","pi","session-end"]`) {
				t.Fatalf("expected lifecycle hook calls missing:\n%s", text)
			}
			if !strings.Contains(text, `\"transcript_path\":\"/ao/data/pi/sessions/pi-session-1.jsonl\"`) {
				t.Fatalf("Pi transcript path missing from hook payloads:\n%s", text)
			}
		})
	}
}

func fakePiBinary(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi")
	source := `#!/usr/bin/env sh
if [ "$1" = "--version" ]; then
  echo "pi ` + version + `"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPiExtensionSteersOnlyForAOChatAndNeverStartsATurn(t *testing.T) {
	source := piActivityExtensionSource(true)
	for _, want := range []string{
		// Only AO Chat sets the folder, so terminal sessions open no socket.
		`const STEER_DIR = process.env.AO_PI_STEER_DIR ?? "";`,
		`const steering = STEER_DIR && process.platform !== "win32" ? steerSessions() : undefined;`,
		// The socket name must match piacp.steerSocketPath.
		`join(STEER_DIR, "ao-steer.sock")`,
		`pi.on("session_shutdown", async (event, ctx) => {` + "\n\t    unregisterSteering();",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("extension source lacks %q", want)
		}
	}
	if strings.Contains(source, "sendUserMessage") {
		t.Error("extension uses sendUserMessage, which starts a turn when Pi is idle")
	}
}

// piExtensionForNode strips the generated extension's few TypeScript
// annotations so Node can load it, as the stop-ordering test does.
func piExtensionForNode(settledSupported bool) string {
	return strings.NewReplacer(
		`import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";`+"\n", "",
		`function callHookSync(hookName: string, payload: Record<string, unknown>)`, `function callHookSync(hookName, payload)`,
		`function sessionID(ctx: any): string`, `function sessionID(ctx)`,
		`function usagePayload(ctx: any, payload: Record<string, unknown> = {}): Record<string, unknown>`, `function usagePayload(ctx, payload = {})`,
		`export default function (pi: ExtensionAPI)`, `export default function (pi)`,
	).Replace(piActivityExtensionSource(settledSupported))
}

// piSteerHarness loads the generated extension into a fake Pi, the way Pi's
// ACP adapter does: no session_start, a context only once a turn starts. Each
// load imports a fresh copy of the module, like a second Pi session in the
// same process. It prints one JSON line of results.
const piSteerHarness = `import { pathToFileURL } from "node:url";
import { createConnection } from "node:net";
import { existsSync } from "node:fs";
import { join } from "node:path";
const socket = join(process.env.AO_PI_STEER_DIR, "ao-steer.sock");
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

// runPiSteerHarness runs piSteerHarness in Node with AO_PI_STEER_DIR set to
// dir and decodes its result line.
func runPiSteerHarness(t *testing.T, dir, mode string, result any) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute the Pi extension fixture")
	}
	fixtureDir := t.TempDir()
	modulePath := filepath.Join(fixtureDir, "ao-activity.mjs")
	if err := os.WriteFile(modulePath, []byte(piExtensionForNode(true)), 0o600); err != nil {
		t.Fatal(err)
	}
	harnessPath := filepath.Join(fixtureDir, "harness.mjs")
	if err := os.WriteFile(harnessPath, []byte(piSteerHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), node, harnessPath, modulePath, mode)
	// No ao on PATH: the activity hooks are best-effort and must not matter.
	cmd.Env = append(os.Environ(), "AO_PI_STEER_DIR="+dir, "PATH="+fixtureDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("extension harness failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), result); err != nil {
		t.Fatalf("decode harness output %q: %v", out, err)
	}
}

// shortSteerDir returns a folder short enough for a Unix socket path on macOS
// (104 bytes), which t.TempDir is not.
func shortSteerDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pisteer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestPiExtensionSteersThroughItsSocket runs the generated extension in Node
// against a fake Pi and talks to its steering socket the way piacp does.
func TestPiExtensionSteersThroughItsSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the extension does not steer on Windows")
	}
	dir := shortSteerDir(t)
	// A socket file left by a Pi process that died is replaced.
	stale, err := net.Listen("unix", filepath.Join(dir, "ao-steer.sock"))
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	var got struct {
		SocketAtLoad                          bool
		BeforeTurn, Busy, Second, Idle, Blank map[string]string
		AfterShutdown                         map[string]string
		SentA, SentB, SentAAfter              int
		Message                               struct {
			Message map[string]any `json:"message"`
			Options map[string]any `json:"options"`
		}
	}
	runPiSteerHarness(t, dir, "all", &got)
	if !got.SocketAtLoad {
		t.Fatal("the steering socket was not up once the extension loaded")
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
	if got.Idle["outcome"] != "promptRequired" || got.Blank["outcome"] != "error" {
		t.Fatalf("idle steer = %v, blank steer = %v, want promptRequired and a refusal", got.Idle, got.Blank)
	}
	if got.AfterShutdown["outcome"] != "promptRequired" || got.SentAAfter != 1 {
		t.Fatalf("steer after shutdown = %v with %d sent, want promptRequired and nothing sent", got.AfterShutdown, got.SentAAfter)
	}
	if _, err := os.Stat(filepath.Join(dir, "ao-steer.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the steering socket outlived its process: %v", err)
	}
}

// TestPiExtensionLeavesALiveSteeringSocketAlone covers a pi started from the
// session's own shell, which inherits AO_PI_STEER_DIR: it must not take the
// socket over from the session's Pi.
func TestPiExtensionLeavesALiveSteeringSocketAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the extension does not steer on Windows")
	}
	dir := shortSteerDir(t)
	path := filepath.Join(dir, "ao-steer.sock")
	owner, err := net.Listen("unix", path)
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
	runPiSteerHarness(t, dir, "owned", &got)
	if got.Owner["outcome"] != "owner" || got.Sent != 0 {
		t.Fatalf("steer = %v with %d sent, want the existing owner to answer", got.Owner, got.Sent)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the owner's socket was removed: %v", err)
	}
}
