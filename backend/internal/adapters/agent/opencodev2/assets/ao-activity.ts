// agent-orchestrator: managed opencode-v2 activity plugin (do not edit)
import { spawnSync } from "node:child_process"
import { Plugin } from "@opencode/plugin"

const HOOK_TIMEOUT_MS = 1_250

export default Plugin.define({
  id: "agent-orchestrator.activity.v2",
  async setup(context) {
    const launchID = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
    const seenSessions = new Set()
    const controller = new AbortController()

    function report(event, sessionID, payload = {}) {
      if (!sessionID) return
      try {
        spawnSync("ao", ["hooks", "opencode-v2", event], {
          cwd: context.location.directory,
          env: { ...process.env, AO_RUNTIME_LAUNCH_ID: launchID },
          input: JSON.stringify({ ...payload, session_id: sessionID, launch_id: launchID }) + "\n",
          timeout: HOOK_TIMEOUT_MS,
          stdio: ["pipe", "ignore", "ignore"],
        })
      } catch {
        // Activity is observational. It must never fail the OpenCode session.
      }
    }

    function ensureSession(sessionID) {
      if (!sessionID || seenSessions.has(sessionID)) return
      seenSessions.add(sessionID)
      report("session-start", sessionID)
    }

    function handleEvent(event) {
      const data = event?.data
      const sessionID = data?.sessionID
      switch (event?.type) {
        case "session.created":
          ensureSession(sessionID)
          break
        case "session.execution.started":
          ensureSession(sessionID)
          report("active", sessionID)
          break
        case "session.status":
          ensureSession(sessionID)
          if (data?.status?.type === "idle") report("stop", sessionID)
          else if (data?.status?.type === "busy") report("active", sessionID)
          break
        case "permission.asked":
          ensureSession(sessionID)
          report("permission-blocked", sessionID, { permission_id: data?.id ?? "" })
          break
        case "permission.replied":
          ensureSession(sessionID)
          report("permission-resolved", sessionID, { permission_id: data?.requestID ?? "", reply: data?.reply ?? "" })
          break
      }
    }

    const eventLoop = (async () => {
      try {
        for await (const event of context.event.subscribe({ signal: controller.signal })) handleEvent(event)
      } catch {
        // Event delivery and AO reporting are both best effort.
      }
    })()

    const registrations = await Promise.all([
      context.session.hook("prompt", (input) => {
        ensureSession(input.sessionID)
        report("user-prompt-submit", input.sessionID, { prompt: input.prompt?.text ?? "" })
      }),
      context.tool.hook("execute.before", (input) => {
        ensureSession(input.sessionID)
        report("active", input.sessionID, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
      context.tool.hook("execute.after", (input) => {
        ensureSession(input.sessionID)
        report("active", input.sessionID, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
    ])

    return async () => {
      controller.abort()
      await Promise.allSettled(registrations.map((registration) => registration.dispose()))
      await eventLoop
    }
  },
})
