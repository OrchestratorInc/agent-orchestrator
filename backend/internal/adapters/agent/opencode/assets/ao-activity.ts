// agent-orchestrator: managed opencode activity plugin (do not edit)
//
// It maps opencode's native lifecycle events onto AO's normalized
// activity events:
//   session.created                       -> `ao hooks opencode session-start`
//   message.updated / message.part.updated -> `ao hooks opencode user-prompt-submit`
//   tool.execute.before                  -> `ao hooks opencode pre-tool-use`
//   tool.execute.after                   -> `ao hooks opencode post-tool-use`
//   tool left open at the turn boundary  -> `ao hooks opencode post-tool-use-failure`
//                                             (opencode 1 reports no post for a
//                                              tool that threw, so the leftover
//                                              call ids are closed as failed)
//   child (subagent) tool activity       -> `ao hooks opencode active`
//                                             (folded onto the parent id, with no
//                                              call identity of its own)
//   session.status (status.type == idle)   -> `ao hooks opencode stop`
//                                             (carries the turn's final assistant text)
//   permission.asked / question.asked      -> `ao hooks opencode permission-blocked`
//                                             (carries the blocked `tool.callID`)
//   last permission/question dialog closed -> `ao hooks opencode permission-resolved`
//                                             (several dialogs can be open at once;
//                                              the report waits for the last one)
//
// Child (subagent) sessions share this event bus. They are tracked from
// `session.created` (`info.parentID`) and never reported as the session
// identity: their start/prompt/stop events are dropped, while their tool and
// permission traffic is attributed to the parent session so subagent work still
// reads as activity and a child dialog still surfaces. AO persists the reported
// id as the native restore handle, so forwarding a child id would make
// restoration resume the wrong conversation.
//
// The opencode-native session id (and prompt/model where known) is piped to the
// hook command as JSON on stdin, run with cwd set to the worktree so AO can
// correlate the opencode session to its AO session. Every invocation is
// best-effort and must never crash the user's opencode session: a missing `ao`
// binary is a guarded no-op (`Bun.which`), and spawn exceptions, non-zero
// exit codes, and malformed event payloads are caught and surfaced through
// opencode's structured logger (client.app.log) for diagnosis — never rethrown.
//
// `import type` is erased at runtime by Bun's transpiler, so this loads even
// before opencode has installed @opencode-ai/plugin into the config dir.
import type { Plugin } from "@opencode-ai/plugin"

export const aoActivity: Plugin = async ({ directory, client }) => {
  // ao hooks must never be able to hang opencode: cap each invocation, matching
  // the 30s timeout the claude-code and codex hook entries use.
  const HOOK_TIMEOUT_MS = 30_000
  // A user message is reported at most twice (see reportUserPrompt): an optional
  // early empty report, then an upgrade carrying the prompt text. Maps a message
  // id to whether the report we already sent included the prompt text.
  const promptReports = new Map<string, boolean>()
  // message.* events don't carry the session id, so track it from events that do.
  let currentSessionID: string | null = null
  // OpenCode can hold several permission/question dialogs at once (two bash
  // calls in one step, for example). Track them by request id: a reply closes
  // only its own dialog, so the turn-boundary report must wait until the last
  // one is answered. Reporting the first reply would send AO back to active
  // while a dialog is still on screen, and `ao send` keystrokes would type
  // into that dialog instead of reaching the agent.
  const openAsks = new Set<string>()
  let askCounter = 0
  // Subagent sessions are children of the session the user started. They are
  // never adopted as currentSessionID: AO treats the reported id as the native
  // session to restore, and a child id would resume the subagent conversation.
  const childSessions = new Set<string>()
  // Root tool calls whose `tool.execute.after` has not arrived yet, mapped to
  // the owning session and the tool name. OpenCode 1 fires no hook when a tool
  // throws, so a failed tool reports no post and its step would stay "running"
  // forever — the lifecycle closes a step only on a matching post, and `stop`
  // only clears flights. The leftovers are closed as failures at the turn
  // boundary. The owning session is kept with each call: a late idle event from
  // a replaced session must never close or re-attribute another session's calls.
  const toolsInFlight = new Map<string, { sessionID: string; toolName: string }>()
  // The model of the most recent assistant message, forwarded for context.
  let currentModel: string | null = null
  // AO fences each provider generation with AO_RUNTIME_LAUNCH_ID; carry it in
  // the payload so hooks work even when child-process env inheritance is
  // trimmed by the host runtime.
  const launchID: string = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
  const messageStore = new Map<string, any>()
  // This turn's assistant message ids and the assistant text part updated last: the turn's final message.
  const assistantMessages = new Set<string>()
  let lastAssistantText = ""

  // Resolve AO through Bun so the same command works on Unix and Windows. A
  // missing binary remains a silent no-op rather than a per-event error.
  function hookCmd(hookName: string): string[] | null {
    const ao = Bun.which("ao")
    return ao ? [ao, "hooks", "opencode", hookName] : null
  }

  // Report a hook failure through opencode's structured logger. Best-effort: the
  // log call must itself never throw or reject back into opencode, hence the
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
  //   1. Ordering. An async hook yields the event loop; if opencode does not
  //      await the handler's promise, a later event (e.g. message.updated ->
  //      user-prompt-submit) could complete before an in-flight async
  //      session-start, so AO would see the prompt before the session is
  //      registered. spawnSync blocks opencode's single-threaded loop until the
  //      hook returns, so events are reported strictly in dispatch order.
  //   2. `opencode run` exits on the idle event, so an async stop hook would be
  //      killed before completing.
  //
  // A non-zero exit (the guard makes a missing `ao` exit 0, so this is a real
  // `ao hooks` failure) or a spawn exception is logged with its stderr and never
  // rethrown, so reporting failures are diagnosable without crashing opencode.
  function callHookSync(hookName: string, payload: Record<string, unknown>) {
    try {
      const command = hookCmd(hookName)
      if (!command) return
      const result = Bun.spawnSync(command, {
        cwd: directory,
        env: { ...process.env, AO_RUNTIME_LAUNCH_ID: launchID },
        stdin: new TextEncoder().encode(JSON.stringify({ ...payload, launch_id: launchID }) + "\n"),
        stdout: "ignore",
        stderr: "pipe",
        timeout: HOOK_TIMEOUT_MS,
      })
      if (!result.success) {
        const stderr = result.stderr ? new TextDecoder().decode(result.stderr).trim() : ""
        logHookFailure(hookName, `exited ${result.exitCode}${stderr ? `: ${stderr}` : ""}`)
      }
    } catch (err) {
      // The spawn itself failed. Never propagate.
      logHookFailure(hookName, err instanceof Error ? err.message : String(err))
    }
  }

  // Close the session's tool steps that never reported their post as failed.
  // OpenCode 1 exposes no failure hook, so an after-hook that never fired means
  // the tool threw (or the turn was interrupted): marking the step failed is
  // accurate, and leaving it open is not — the lifecycle's step ring only closes
  // on a matching post, so it would show "running" for the rest of the session.
  // Only this session's calls are closed, and only they are dropped, so a stale
  // event from a replaced session can neither close nor re-attribute another
  // session's in-flight calls.
  function failOpenTools(sessionID: string) {
    for (const [callID, call] of toolsInFlight) {
      if (call.sessionID !== sessionID) continue
      toolsInFlight.delete(callID)
      callHookSync("post-tool-use-failure", {
        session_id: sessionID,
        model: currentModel ?? "",
        tool_name: call.toolName,
        tool_use_id: callID,
      })
    }
  }

  function switchedSession(sessionID: string): boolean {
    if (currentSessionID === sessionID) return false
    // Close the replaced identity's open steps under the id they were reported
    // with, so they can never be attributed to the incoming session.
    if (currentSessionID) failOpenTools(currentSessionID)
    promptReports.clear()
    messageStore.clear()
    assistantMessages.clear()
    lastAssistantText = ""
    currentModel = null
    // Dialogs of the replaced session are no longer ours to track; the new
    // identity starts with none open.
    openAsks.clear()
    currentSessionID = sessionID
    return true
  }

  // The id a dialog is opened and answered under. permission.* carries
  // permissionID on ask and reply; question.* has been seen as id or requestID
  // depending on the opencode release. An ask with no id gets a synthetic one.
  function askID(props: any): string | null {
    const id = props?.permissionID ?? props?.requestID ?? props?.id
    return typeof id === "string" && id.length > 0 ? id : null
  }

  function isChildSession(sessionID: string | null | undefined): boolean {
    return !!sessionID && childSessions.has(sessionID)
  }

  // Resolve the session id AO may see. Child (subagent) traffic is folded onto
  // the parent id so it still counts as activity without leaking the child's
  // native id; it returns null when no parent is known yet, in which case the
  // child event has no safe target. Non-child ids pass through unchanged so
  // AO's supported new-native-session flows keep working.
  function reportSessionID(sessionID: string | null | undefined): string | null {
    if (!sessionID) return null
    if (!childSessions.has(sessionID)) return sessionID
    if (!currentSessionID || childSessions.has(currentSessionID)) return null
    return currentSessionID
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
    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const session = (event as any).properties?.info
            if (!session?.id) break
            // Nested subagents carry their immediate parent's id. Any session
            // with a parent is a child and must never become the identity.
            if (session.parentID) {
              childSessions.add(session.id)
              break
            }
            if (switchedSession(session.id)) {
              callHookSync("session-start", { session_id: session.id })
            }
            break
          }

          case "message.updated": {
            const msg = (event as any).properties?.info
            if (!msg) break
            // Child prompt/assistant messages are subagent traffic: never adopt
            // the child id and never report its prompt as the parent's.
            if (isChildSession(msg.sessionID)) break
            if (msg.sessionID && switchedSession(msg.sessionID)) {
              callHookSync("session-start", { session_id: msg.sessionID })
            }
            if (msg.role === "assistant" && msg.modelID) currentModel = msg.modelID
            if (msg.role === "assistant" && msg.id) assistantMessages.add(msg.id)
            // Fallback: some `opencode run` flows never deliver message.part.updated
            // for the prompt, so start the turn from the user message itself.
            if (msg.role === "user") {
              messageStore.set(msg.id, msg)
              const sessionID = msg.sessionID ?? currentSessionID
              if (sessionID) reportUserPrompt(sessionID, msg.id, "")
            }
            break
          }

          case "message.part.updated": {
            const part = (event as any).properties?.part
            if (!part?.messageID) break
            const msg = messageStore.get(part.messageID)
            if (msg && isChildSession(msg.sessionID)) break
            if (msg?.role === "user" && part.type === "text") {
              const sessionID = msg.sessionID ?? currentSessionID
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
            // session.status fires in both TUI and `opencode run`; session.idle
            // is deprecated and not reliably emitted in run mode.
            // AO's "stop" hook means "the current turn is idle/finished", not
            // "the whole native session has terminated", so multi-turn TUI
            // sessions intentionally emit one stop per idle transition.
            const props = (event as any).properties
            if (props?.status?.type !== "idle") break
            const sessionID = props?.sessionID ?? currentSessionID
            // A child finishing says nothing about the parent's turn — never
            // stop the parent on subagent completion.
            if (!sessionID || isChildSession(sessionID)) break
            // The turn boundary is where a failed tool's step ends: OpenCode 1
            // reports no post for a tool that threw. Close the leftovers first
            // so the timeline is settled before the turn goes idle.
            failOpenTools(sessionID)
            callHookSync("stop", { session_id: sessionID, model: currentModel ?? "", last_assistant_message: lastAssistantText })
            assistantMessages.clear()
            lastAssistantText = ""
            // The turn ended; any dialog state still tracked is stale and must
            // not suppress the next turn's resolve.
            openAsks.clear()
            break
          }

          case "permission.asked":
          case "question.asked": {
            const props = (event as any).properties
            const sessionID = reportSessionID(props?.sessionID ?? currentSessionID)
            if (!sessionID) break
            const ask = askID(props) ?? `ask-${++askCounter}`
            openAsks.add(ask)
            // A permission ask names the blocked call in `properties.tool`, which
            // carries only `messageID` and `callID`. Forwarding that id lets the
            // lifecycle tie the dialog to the tool it is holding up and clear the
            // block when that exact tool finishes, instead of waiting for the turn
            // boundary. There is no tool name in the payload to forward with it.
            callHookSync("permission-blocked", {
              session_id: sessionID,
              model: currentModel ?? "",
              tool_use_id: props?.tool?.callID ?? "",
            })
            break
          }

          case "permission.replied":
          case "question.replied":
          case "question.rejected": {
            // A reply closes one dialog; generic `active` is suppressed while
            // blocked, so the turn-boundary event AO already understands is
            // reserved for the moment the LAST dialog closes (matching the
            // OpenCode 2 plugin). While a dialog remains open there is no
            // report: the session stays blocked and AO refuses sends, whose
            // keystrokes would otherwise type into the still-open dialog.
            const sessionID = reportSessionID((event as any).properties?.sessionID ?? currentSessionID)
            if (!sessionID) break
            if (openAsks.size > 0) {
              const ask = askID((event as any).properties)
              if (ask !== null && openAsks.has(ask)) openAsks.delete(ask)
              else {
                // No matching id (short payload, or the plugin restarted and
                // never saw the ask): close the oldest tracked dialog. This
                // stays strictly stricter than reporting every reply.
                const oldest = openAsks.values().next().value
                if (oldest !== undefined) openAsks.delete(oldest)
              }
              if (openAsks.size > 0) break
            }
            callHookSync("permission-resolved", { session_id: sessionID, model: currentModel ?? "" })
            break
          }

        }
      } catch (err) {
        // A malformed/unexpected event payload must never crash opencode; log
        // it (tagged with the event type) for diagnosis and move on.
        logHookFailure(`event:${(event as any)?.type ?? "unknown"}`, err instanceof Error ? err.message : String(err))
      }
    },
    "tool.execute.before": async (input) => {
      const sessionID = reportSessionID(input.sessionID)
      if (!sessionID) return
      // Subagent (child) tool traffic is folded to plain activity: its call id
      // must never enter the parent's in-flight tool bookkeeping.
      if (isChildSession(input.sessionID)) {
        callHookSync("active", { session_id: sessionID, model: currentModel ?? "" })
        return
      }
      const toolUseID = input.callID ?? ""
      if (toolUseID) toolsInFlight.set(toolUseID, { sessionID, toolName: input.tool ?? "" })
      // Never forward the tool arguments; only the identity AO correlates on.
      callHookSync("pre-tool-use", {
        session_id: sessionID,
        model: currentModel ?? "",
        tool_name: input.tool ?? "",
        tool_use_id: toolUseID,
      })
    },
    "tool.execute.after": async (input, output) => {
      // OpenCode 1 fires this only after a successful execution and exposes no
      // failure hook, so a tool that threw reports no post at all; failOpenTools
      // closes it as failed at the turn boundary.
      const sessionID = reportSessionID(input.sessionID)
      if (!sessionID) return
      if (isChildSession(input.sessionID)) {
        callHookSync("active", { session_id: sessionID, model: currentModel ?? "" })
        return
      }
      // A thrown subtask still fires this hook, with no output. Keep its call in
      // flight and report nothing: the turn-boundary sweep closes it as failed.
      if (output === undefined) return
      const toolUseID = input.callID ?? ""
      if (toolUseID) toolsInFlight.delete(toolUseID)
      callHookSync("post-tool-use", {
        session_id: sessionID,
        model: currentModel ?? "",
        tool_name: input.tool ?? "",
        tool_use_id: toolUseID,
      })
    },
  }
}
