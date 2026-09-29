// agent-orchestrator: managed opencode v2 activity plugin (do not edit)
//
// OpenCode v2 (@opencode/cli, plugin SDK @opencode/plugin) replaced v1's
// factory-returning-hooks with a domain-based plugin: `define({ id, setup(ctx) })`.
// Events arrive from `ctx.event.subscribe()` (an AsyncIterable) and tool activity
// from `ctx.tool.hook("execute.before"|"execute.after")`. This plugin maps
// opencode v2's native lifecycle onto AO's five normalized activity events:
//   session.created                                   -> `ao hooks opencode session-start`
//   prompt.submit                                     -> `ao hooks opencode user-prompt-submit`
//   tool execute.before/after, session.tool.called,
//     permission.replied                              -> `ao hooks opencode active`
//   session.execution.{succeeded,failed,interrupted},
//     session.idle                                    -> `ao hooks opencode stop`
//   permission.asked                                  -> `ao hooks opencode permission-blocked`
//
// The opencode-native session id (plus prompt/model where known) is piped to the
// hook command as JSON on stdin, cwd = the worktree, so AO can correlate the
// opencode session to its AO session. Every invocation is best-effort and must
// never crash the user's opencode session: a missing `ao` binary is a guarded
// no-op, and spawn exceptions / non-zero exits / malformed payloads are caught
// and surfaced through opencode's structured logger, never rethrown.
import { define } from "@opencode/plugin"

// AO fences each provider generation with AO_RUNTIME_LAUNCH_ID; carry it in the
// payload so hooks work even when child-process env inheritance is trimmed.
const launchID: string = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
// Match the 30s hook timeout the claude-code/codex entries use.
const HOOK_TIMEOUT_MS = 30_000

export default define({
  id: "ao-activity",
  setup: (ctx) => {
    const directory = ctx.location?.directory ?? process.cwd()
    // A user message is reported at most once with text (terminal) and at most
    // once empty; see reportUserPrompt.
    const promptReports = new Map<string, boolean>()
    let currentModel: string | null = null

    function hookCmd(hookName: string): string[] | null {
      const ao = Bun.which("ao")
      return ao ? [ao, "hooks", "opencode", hookName] : null
    }

    function logHookFailure(hookName: string, detail: string) {
      try {
        void ctx?.app
          ?.log?.({ body: { service: "ao-activity", level: "error", message: `hook ${hookName} failed: ${detail}` } })
          ?.catch?.(() => {})
      } catch {
        // logger unavailable — nothing safe left to do
      }
    }

    // Synchronous dispatch preserves event ordering (opencode's loop blocks until
    // the hook returns) and survives `opencode run` exiting on the stop event.
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
        logHookFailure(hookName, err instanceof Error ? err.message : String(err))
      }
    }

    function reportUserPrompt(sessionID: string, messageID: string, prompt: string) {
      const hasText = prompt.length > 0
      const reportedWithText = promptReports.get(messageID)
      if (reportedWithText) return
      if (reportedWithText === false && !hasText) return
      promptReports.set(messageID, hasText)
      callHookSync("user-prompt-submit", { session_id: sessionID, prompt, model: currentModel ?? "" })
    }

    // Payload shapes are read defensively (v2 event `properties` shapes are read
    // via `any`, matching v1's tolerance): a renamed nested field degrades to a
    // logged no-op, never a crash. Confirm exact shapes with a live v2 run.
    function handleEvent(event: any) {
      try {
        const props = event?.properties ?? {}
        switch (event?.type) {
          case "session.created": {
            const id = props.info?.id ?? props.sessionID
            if (id) callHookSync("session-start", { session_id: id })
            break
          }
          case "prompt.submit": {
            const sessionID = props.sessionID ?? props.info?.sessionID
            const messageID = props.messageID ?? props.message?.id ?? `${sessionID}:${props.seq ?? ""}`
            const prompt = props.text ?? props.prompt ?? ""
            if (sessionID) reportUserPrompt(sessionID, messageID, prompt)
            break
          }
          case "session.model.selected": {
            const model = props.modelID ?? props.model?.id ?? props.model
            if (model) currentModel = String(model)
            break
          }
          case "session.tool.called":
          case "session.step.started":
          case "session.execution.started": {
            const sessionID = props.sessionID
            if (sessionID) callHookSync("active", { session_id: sessionID, model: currentModel ?? "" })
            break
          }
          case "session.execution.succeeded":
          case "session.execution.failed":
          case "session.execution.interrupted":
          case "session.idle": {
            const sessionID = props.sessionID
            if (sessionID) callHookSync("stop", { session_id: sessionID, model: currentModel ?? "" })
            break
          }
          case "permission.asked": {
            const sessionID = props.sessionID
            if (sessionID) callHookSync("permission-blocked", { session_id: sessionID, model: currentModel ?? "" })
            break
          }
          case "permission.replied": {
            const sessionID = props.sessionID
            if (sessionID) callHookSync("active", { session_id: sessionID, model: currentModel ?? "" })
            break
          }
        }
      } catch (err) {
        logHookFailure(`event:${event?.type ?? "unknown"}`, err instanceof Error ? err.message : String(err))
      }
    }

    const controller = new AbortController()
    // Drain the event stream for the life of the plugin. Failures in the stream
    // are logged, not fatal.
    void (async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal } as any)) {
          handleEvent(event)
        }
      } catch (err) {
        if (!controller.signal.aborted) {
          logHookFailure("event-stream", err instanceof Error ? err.message : String(err))
        }
      }
    })()

    // Tool execution is direct activity; register both edges.
    const registrations: Array<Promise<{ dispose: () => Promise<void> }>> = [
      ctx.tool.hook("execute.before", (input: any) => {
        if (input?.sessionID) callHookSync("active", { session_id: input.sessionID, model: currentModel ?? "" })
      }),
      ctx.tool.hook("execute.after", (input: any) => {
        if (input?.sessionID) callHookSync("active", { session_id: input.sessionID, model: currentModel ?? "" })
      }),
    ]

    return async () => {
      controller.abort()
      for (const reg of registrations) {
        try {
          await (await reg).dispose()
        } catch {
          // best-effort teardown
        }
      }
    }
  },
})
