package pi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	piExtensionsDirName = "extensions"
	piExtensionFileName = "ao-activity.ts"
	piExtensionSentinel = "agent-orchestrator: managed pi activity extension"
	minPiSettledVersion = "0.80.6"
)

var piVersionPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

func piExtensionPath(workspacePath string) string {
	return filepath.Join(workspacePath, ".pi", piExtensionsDirName, piExtensionFileName)
}

// GetAgentHooks installs AO's project-local Pi extension. Launch and restore
// pass it explicitly with --extension, so activity reporting does not depend on
// Pi's trust-gated project auto-discovery.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("pi.GetAgentHooks: WorkspacePath is required")
	}

	path := piExtensionPath(cfg.WorkspacePath)
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // caller-owned workspace path
		if !strings.Contains(string(data), piExtensionSentinel) {
			return fmt.Errorf("pi.GetAgentHooks: refusing to overwrite non-AO file at %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pi.GetAgentHooks: read managed extension: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("pi.GetAgentHooks: create extension dir: %w", err)
	}
	settledSupported, err := p.piAgentSettledSupported(ctx)
	if err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(path, []byte(piActivityExtensionSource(settledSupported)), 0o600); err != nil {
		return fmt.Errorf("pi.GetAgentHooks: write extension: %w", err)
	}
	// The ignore file goes in .pi, not next to the extension: Pi skips any
	// extension a .gitignore in its extensions folder lists, so one there
	// hides AO's extension from Pi's discovery. A terminal session names the
	// file with --extension and still loaded it; Pi's ACP adapter (Chat) only
	// discovers.
	if err := removeManagedGitignore(filepath.Dir(path)); err != nil {
		return fmt.Errorf("pi.GetAgentHooks: gitignore: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Join(cfg.WorkspacePath, ".pi"), piExtensionsDirName+"/"+piExtensionFileName); err != nil {
		return fmt.Errorf("pi.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

// removeManagedGitignore deletes the .gitignore earlier AO versions wrote in
// dir. A .gitignore without AO's sentinel is the user's and stays.
func removeManagedGitignore(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path) //nolint:gosec // caller-owned workspace path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), hookutil.GitignoreSentinel) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func appendPiExtensionFlag(cmd *[]string, workspacePath string) {
	if strings.TrimSpace(workspacePath) == "" {
		return
	}
	*cmd = append(*cmd, "--extension", piExtensionPath(workspacePath))
}

func (p *Plugin) piAgentSettledSupported(ctx context.Context) (bool, error) {
	binary, err := p.piBinary(ctx)
	if err != nil {
		return false, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := aoprocess.CommandContext(probeCtx, binary, "--version").CombinedOutput() //nolint:gosec // binary is adapter-resolved, args are static.
	if err != nil {
		return false, fmt.Errorf("pi.GetAgentHooks: probe pi --version: %w", err)
	}
	version, ok := parsePiVersion(string(out))
	if !ok {
		return false, fmt.Errorf("pi.GetAgentHooks: parse pi --version output %q", strings.TrimSpace(string(out)))
	}
	minimum, _ := parsePiVersion(minPiSettledVersion)
	return !version.less(minimum), nil
}

type piVersion [3]int

func parsePiVersion(output string) (piVersion, bool) {
	match := piVersionPattern.FindStringSubmatch(output)
	if match == nil {
		return piVersion{}, false
	}
	var version piVersion
	for i := range version {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return piVersion{}, false
		}
		version[i] = n
	}
	return version, true
}

func (v piVersion) less(other piVersion) bool {
	for i := range v {
		if v[i] < other[i] {
			return true
		}
		if v[i] > other[i] {
			return false
		}
	}
	return false
}

func piActivityExtensionSource(settledSupported bool) string {
	return `// ` + piExtensionSentinel + ` (do not edit)
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { spawnSync } from "node:child_process";
import { rmSync } from "node:fs";
import { createConnection, createServer } from "node:net";
import { join } from "node:path";

const HOOK_TIMEOUT_MS = 5_000;
const AGENT_SETTLED_SUPPORTED = ` + strconv.FormatBool(settledSupported) + `;

function callHookSync(hookName: string, payload: Record<string, unknown>) {
  try {
    const result = spawnSync("ao", ["hooks", "pi", hookName], {
      input: JSON.stringify(payload) + "\n",
      encoding: "utf8",
      stdio: ["pipe", "ignore", "pipe"],
      timeout: HOOK_TIMEOUT_MS,
      windowsHide: true,
    });
    // AO's hook command records daemon delivery failures itself. A missing AO
    // executable or timeout is deliberately ignored so Pi remains usable.
    void result;
  } catch {
    // Activity reporting is best-effort and must never interrupt Pi.
  }
}

function sessionID(ctx: any): string {
  return ctx.sessionManager.getSessionId() ?? "";
}

// AO Chat sets AO_PI_STEER_DIR to a folder private to the AO session. Pi runs
// every session of that AO session in one process, so the process listens on
// one socket there and each loaded Pi session registers itself by its id,
// which is also the ACP session id AO steers.
const STEER_DIR = process.env.AO_PI_STEER_DIR ?? "";
const STEER_SESSIONS = Symbol.for("ao.pi.steer.sessions");

// steerSessions starts this process's steering socket on first use and
// returns its session registry. It runs when the extension loads, so the
// socket is up before the first prompt; Pi's ACP adapter never emits
// session_start.
function steerSessions() {
  if (globalThis[STEER_SESSIONS]) return globalThis[STEER_SESSIONS];
  const sessions = new Map();
  globalThis[STEER_SESSIONS] = sessions;
  const path = join(STEER_DIR, "ao-steer.sock");
  // A socket that still answers belongs to another live Pi process, such as
  // a pi started from this session's own shell; leave it alone. One that does
  // not is stale.
  const probe = createConnection(path);
  probe.on("connect", () => probe.destroy());
  probe.on("error", () => {
    try { rmSync(path, { force: true }); } catch {}
    const server = createServer((conn) => answerSteer(conn, sessions));
    server.on("error", () => {});
    server.listen(path, () => {
      process.once("exit", () => {
        try { rmSync(path, { force: true }); } catch {}
      });
    });
    server.unref();
  });
  return sessions;
}

// answerSteer reads one steer request and answers it. A steer reaches only a
// running turn, after its current tool calls and before its next model call.
// With no turn running AO is told so and nothing is started: sendMessage
// never starts a turn by itself.
function answerSteer(conn, sessions) {
  let buffer = "";
  conn.on("error", () => {});
  conn.on("data", (chunk) => {
    buffer += chunk.toString();
    const newline = buffer.indexOf("\n");
    if (newline < 0) return;
    let reply;
    try {
      const request = JSON.parse(buffer.slice(0, newline));
      const text = typeof request.text === "string" ? request.text.trim() : "";
      const session = sessions.get(request.sessionId);
      if (!text) {
        reply = { outcome: "error", reason: "the steer has no text" };
      } else if (!session || session.ctx.isIdle()) {
        reply = { outcome: "promptRequired" };
      } else {
        session.pi.sendMessage({ customType: "ao-steer", content: text, display: true }, { deliverAs: "steer" });
        reply = { outcome: "injected" };
      }
    } catch (error) {
      reply = { outcome: "error", reason: String(error) };
    }
    conn.end(JSON.stringify(reply) + "\n");
  });
}

` + hookutil.FinalAssistantTextJS + `
export default function (pi: ExtensionAPI) {
  // The final message of the current run. Stop may fire from a later event
  // than the one that carries the messages, so it is kept until then.
  let lastAssistantText = "";
  // Steering for AO Chat (see steerSessions): this Pi session registers once
  // it has a context, which the first turn brings.
  const steering = STEER_DIR && process.platform !== "win32" ? steerSessions() : undefined;
  let steerID = "";
  const registerSteering = (ctx) => {
    const id = steering ? sessionID(ctx) : "";
    if (!id) return;
    if (steerID && steerID !== id && steering.get(steerID)?.pi === pi) steering.delete(steerID);
    steerID = id;
    steering.set(id, { pi, ctx });
  };
  const unregisterSteering = () => {
    if (steerID && steering?.get(steerID)?.pi === pi) steering.delete(steerID);
    steerID = "";
  };
  pi.on("session_start", async (_event, ctx) => {
    registerSteering(ctx);
    callHookSync("session-start", { session_id: sessionID(ctx) });
  });
  pi.on("agent_start", async (_event, ctx) => {
    registerSteering(ctx);
  });
  pi.on("turn_start", async (_event, ctx) => {
    registerSteering(ctx);
  });
  pi.on("before_agent_start", async (event, ctx) => {
    lastAssistantText = "";
    callHookSync("user-prompt-submit", { session_id: sessionID(ctx), prompt: event.prompt });
  });
  // agent_end is the completion event in Pi 0.80.x. Newer releases may retry,
  // compact, or queue follow-up work after it; a subsequent start immediately
  // reactivates AO, while agent_settled below confirms the final idle state.
  pi.on("agent_end", async (event, ctx) => {
	    lastAssistantText = finalAssistantText(event) || lastAssistantText;
	    if (!AGENT_SETTLED_SUPPORTED) callHookSync("stop", { session_id: sessionID(ctx), last_assistant_message: lastAssistantText });
  });
  pi.on("agent_settled", async (_event, ctx) => {
	    if (AGENT_SETTLED_SUPPORTED) callHookSync("stop", { session_id: sessionID(ctx), last_assistant_message: lastAssistantText });
  });
  pi.on("session_shutdown", async (event, ctx) => {
	    unregisterSteering();
	    if (event.reason === "quit") {
	      callHookSync("session-end", { session_id: sessionID(ctx), reason: event.reason });
	    }
  });
}
`
}
