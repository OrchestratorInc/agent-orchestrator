// agent-orchestrator: managed kilocode activity plugin (do not edit)
//
// The Kilo Code CLI (binary "kilocode") is a fork of sst/opencode and loads the
// @opencode-ai/plugin runtime, so this plugin uses the same lifecycle surface.
// It maps Kilo's native lifecycle events onto AO's normalized activity events:
//   session.created                        -> `ao hooks kilocode session-start`
//   message.updated / message.part.updated  -> `ao hooks kilocode user-prompt-submit`
//   permission.ask hook                     -> `ao hooks kilocode permission-request`
//   session.status (status.type == idle)    -> `ao hooks kilocode stop`
//
// The native session id (and prompt/model where known) is piped to the hook
// command as JSON on stdin, run with cwd set to the worktree so AO can correlate
// the Kilo session to its AO session. Every invocation is best-effort and must
// never crash the user's Kilo session: a missing `ao` binary is a guarded no-op
// (`command -v ao`), and spawn exceptions, non-zero exit codes, and malformed
// event payloads are caught and surfaced through Kilo's structured logger
// (client.app.log) for diagnosis — never rethrown.
//
// `import type` is erased at runtime by Bun's transpiler, so this loads even
// before Kilo has installed @opencode-ai/plugin into the config dir.
import type { Plugin } from "@opencode-ai/plugin"

export const aoActivity: Plugin = async ({ directory, client }) => {
  // ao hooks must never be able to hang Kilo: cap each invocation, matching
  // the 30s timeout the claude-code and codex hook entries use.
  const HOOK_TIMEOUT_MS = 30_000
  // A user message is reported at most twice (see reportUserPrompt): an optional
  // early empty report, then an upgrade carrying the prompt text. Maps a message
  // id to whether the report we already sent included the prompt text.
  const promptReports = new Map<string, boolean>()
  // message.* events don't carry the session id, so track it from events that do.
  let currentSessionID: string | null = null
  // The model of the most recent assistant message, forwarded for context.
  let currentModel: string | null = null
  const messageStore = new Map<string, any>()
  // This turn's assistant message ids and the assistant text part updated last: the turn's final message.
  const assistantMessages = new Set<string>()
  let lastAssistantText = ""
  // Bound messageStore so it can't grow unbounded within a session: `kilo run`
  // flows that never deliver a text message.part.updated leave the user message
  // entry undeleted, so without a cap the map would accumulate across many turns.
  // Map preserves insertion order, so the first key is the oldest entry.
  const MESSAGE_STORE_MAX = 256
  function rememberMessage(id: string, msg: any) {
    messageStore.set(id, msg)
    while (messageStore.size > MESSAGE_STORE_MAX) {
      const oldest = messageStore.keys().next().value
      if (oldest === undefined) break
      messageStore.delete(oldest)
    }
  }

  // Wrap in `sh -c` with a guard so a missing `ao` binary is a silent no-op
  // (exit 0) rather than a per-event error in the user's session.
  function hookCmd(hookName: string): string[] {
    return ["sh", "-c", `if ! command -v ao >/dev/null 2>&1; then exit 0; fi; exec ao hooks kilocode ${hookName}`]
  }

  // Report a hook failure through Kilo's structured logger. Best-effort: the
  // log call must itself never throw or reject back into Kilo, hence the
  // optional chaining + swallowed rejection.
  function logHookFailure(hookName: string, detail: string) {
    try {
      void client?.app
        ?.log?.({ body: { service: "ao-activity", level: "error", message: `hook ${hookName} failed: ${detail}` } })
        ?.catch?.(() => {})
    } catch {
      // The logger itself is unavailable — nothing more we can safely do.
    }
  }

  // All hooks are dispatched synchronously (Bun.spawnSync), for two reasons:
  //   1. Ordering. An async hook yields the event loop; if Kilo does not await
  //      the handler's promise, a later event (e.g. message.updated ->
  //      user-prompt-submit) could complete before an in-flight async
  //      session-start, so AO would see the prompt before the session is
  //      registered. spawnSync blocks Kilo's single-threaded loop until the hook
  //      returns, so events are reported strictly in dispatch order.
  //   2. `kilo run` exits on the idle event, so an async stop hook would be
  //      killed before completing.
  //
  // A non-zero exit (the guard makes a missing `ao` exit 0, so this is a real
  // `ao hooks` failure) or a spawn exception is logged with its stderr and never
  // rethrown, so reporting failures are diagnosable without crashing Kilo.
  function callHookSync(hookName: string, payload: Record<string, unknown>) {
    try {
      const result = Bun.spawnSync(hookCmd(hookName), {
        cwd: directory,
        stdin: new TextEncoder().encode(JSON.stringify(payload) + "\n"),
        stdout: "ignore",
        stderr: "pipe",
        timeout: HOOK_TIMEOUT_MS,
      })
      if (!result.success) {
        const stderr = result.stderr ? new TextDecoder().decode(result.stderr).trim() : ""
        logHookFailure(hookName, `exited ${result.exitCode}${stderr ? `: ${stderr}` : ""}`)
      }
    } catch (err) {
      // The spawn itself failed (e.g. no `sh` on PATH). Never propagate.
      logHookFailure(hookName, err instanceof Error ? err.message : String(err))
    }
  }

  function switchedSession(sessionID: string): boolean {
    if (currentSessionID === sessionID) return false
    promptReports.clear()
    messageStore.clear()
    assistantMessages.clear()
    lastAssistantText = ""
    currentModel = null
    currentSessionID = sessionID
    return true
  }

  function readSessionID(value: any): string | null {
    const id = value?.sessionID ?? value?.sessionId ?? value?.session_id
    return typeof id === "string" && id.trim().length > 0 ? id.trim() : null
  }

  function readCreatedSessionID(value: any): string | null {
    const id = readSessionID(value) ?? value?.id
    return typeof id === "string" && id.trim().length > 0 ? id.trim() : null
  }

  // Report a user prompt, preferring the one that carries the prompt text.
  // message.updated can arrive before message.part.updated with no text, so an
  // early empty report must NOT dedup away the later text report — otherwise the
  // prompt never reaches AO and title-from-prompt metadata breaks. Therefore: an
  // empty report fires at most once (so run-mode flows that omit the text part
  // still mark the session active), and a text report fires once and is terminal.
  function reportUserPrompt(sessionID: string, messageID: string, prompt: string) {
    const hasText = prompt.length > 0
    const reportedWithText = promptReports.get(messageID)
    if (reportedWithText) return // already reported with text — terminal
    if (reportedWithText === false && !hasText) return // already reported empty; no new info
    promptReports.set(messageID, hasText)
    callHookSync("user-prompt-submit", { session_id: sessionID, prompt, model: currentModel ?? "" })
  }

  return {
    // permission.ask fires when Kilo needs the user to approve a tool call. AO
    // maps it to a sticky waiting_input state. The plugin only observes the
    // request (it does not alter `output.status`), so Kilo's own approval flow
    // is untouched.
    "permission.ask": async (input: any) => {
      try {
        const sessionID = readSessionID(input) ?? currentSessionID
        if (!sessionID) return
        callHookSync("permission-request", { session_id: sessionID, model: currentModel ?? "" })
      } catch (err) {
        logHookFailure("permission-request", err instanceof Error ? err.message : String(err))
      }
    },

    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const session = (event as any).properties?.info
            const sessionID = readCreatedSessionID(session)
            if (!sessionID) break
            if (switchedSession(sessionID)) {
              callHookSync("session-start", { session_id: sessionID })
            }
            break
          }

          case "message.updated": {
            const msg = (event as any).properties?.info
            if (!msg) break
            const msgSessionID = readSessionID(msg)
            if (msgSessionID && switchedSession(msgSessionID)) {
              callHookSync("session-start", { session_id: msgSessionID })
            }
            if (msg.role === "assistant" && msg.modelID) currentModel = msg.modelID
            if (msg.role === "assistant" && msg.id) assistantMessages.add(msg.id)
            // Fallback: some `kilo run` flows never deliver message.part.updated
            // for the prompt, so start the turn from the user message itself.
            if (msg.role === "user") {
              rememberMessage(msg.id, msg)
              const sessionID = msgSessionID ?? currentSessionID
              if (sessionID) reportUserPrompt(sessionID, msg.id, "")
            }
            break
          }

          case "message.part.updated": {
            const part = (event as any).properties?.part
            if (!part?.messageID) break
            const msg = messageStore.get(part.messageID)
            if (msg?.role === "user" && part.type === "text") {
              const sessionID = readSessionID(msg) ?? currentSessionID
              const prompt = part.text ?? ""
              if (sessionID) reportUserPrompt(sessionID, msg.id, prompt)
              if (prompt.length > 0) messageStore.delete(part.messageID)
            } else if (assistantMessages.has(part.messageID) && part.type === "text" && !part.synthetic && !part.ignored &&
              typeof part.text === "string" && part.text.trim().length > 0) {
              lastAssistantText = part.text
            }
            break
          }

          case "session.status": {
            // session.status fires in both TUI and `kilo run`; session.idle is
            // deprecated and not reliably emitted in run mode.
            // AO's "stop" hook means "the current turn is idle/finished", not
            // "the whole native session has terminated", so multi-turn TUI
            // sessions intentionally emit one stop per idle transition.
            const props = (event as any).properties
            if (props?.status?.type !== "idle") break
            const sessionID = readSessionID(props) ?? currentSessionID
            if (!sessionID) break
            callHookSync("stop", { session_id: sessionID, model: currentModel ?? "", last_assistant_message: lastAssistantText })
            assistantMessages.clear()
            lastAssistantText = ""
            break
          }
        }
      } catch (err) {
        // A malformed/unexpected event payload must never crash Kilo; log it
        // (tagged with the event type) for diagnosis and move on.
        logHookFailure(`event:${(event as any)?.type ?? "unknown"}`, err instanceof Error ? err.message : String(err))
      }
    },
  }
}
