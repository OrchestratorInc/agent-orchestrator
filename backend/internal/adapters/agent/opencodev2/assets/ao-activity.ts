// agent-orchestrator: managed opencode-v2 activity plugin (do not edit)
//
// Child (subagent) sessions share this event bus and carry their own ids. They
// are tracked from session.created(parentID) and never reported as the session
// identity: AO persists the reported id as the native restore handle, so a
// child id would make restoration resume the subagent conversation. Child tool /
// execution activity is folded onto the root session as plain `active` so
// subagent work still reads as activity, while child turn boundaries are
// dropped — a child finishing does not end the parent's turn.
import { spawn } from "node:child_process"

const HOOK_TIMEOUT_MS = 1_250

export default {
  id: "agent-orchestrator.activity.v2",
  async setup(context) {
    const launchID = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
    const seenSessions = new Set()
    const childSessions = new Set()
    let rootSessionID = null
    const controller = new AbortController()

    let queue = Promise.resolve()

    function send(event, sessionID, payload) {
      return new Promise((resolve) => {
        try {
          const child = spawn("ao", ["hooks", "opencode-v2", event], {
            cwd: context.location.directory,
            env: { ...process.env, AO_RUNTIME_LAUNCH_ID: launchID },
            stdio: ["pipe", "ignore", "ignore"],
          })
          const timer = setTimeout(() => child.kill(), HOOK_TIMEOUT_MS)
          const done = () => {
            clearTimeout(timer)
            resolve()
          }
          child.on("error", done)
          child.on("close", done)
          child.stdin.on("error", () => {})
          child.stdin.end(JSON.stringify({ ...payload, session_id: sessionID, launch_id: launchID }) + "\n")
        } catch {
          resolve()
        }
      })
    }

    // Activity is observational: report in order but asynchronously so it never blocks OpenCode.
    function report(event, sessionID, payload = {}) {
      if (!sessionID) return
      queue = queue.then(() => send(event, sessionID, payload))
    }

    function ensureSession(sessionID) {
      if (!sessionID || seenSessions.has(sessionID)) return
      seenSessions.add(sessionID)
      report("session-start", sessionID)
    }

    function isChildSession(sessionID) {
      return !!sessionID && childSessions.has(sessionID)
    }

    // Fold a session id onto the root session AO may see. Children resolve to
    // the current root (null until one is known) and are never forwarded; a
    // non-child id becomes the current root, preserving OpenCode's supported
    // new-native-session flows and AO's checkpoint reset on identity change.
    function resolveSessionID(sessionID) {
      if (!sessionID) return null
      if (childSessions.has(sessionID)) return rootSessionID
      rootSessionID = sessionID
      return sessionID
    }

    function handleEvent(event) {
      const data = event?.data
      const sessionID = data?.sessionID
      switch (event?.type) {
        case "session.created":
          if (!sessionID) break
          // Nested subagents carry their immediate parent's id; any session
          // with a parent is a child and must never become the identity.
          if (data?.parentID) {
            childSessions.add(sessionID)
            break
          }
          rootSessionID = sessionID
          ensureSession(sessionID)
          break
        case "session.execution.started": {
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          report("active", root)
          break
        }
        case "session.execution.succeeded":
        case "session.execution.failed":
        case "session.execution.interrupted":
          // A child's execution boundary says nothing about the parent turn.
          if (!sessionID || isChildSession(sessionID)) break
          rootSessionID = sessionID
          ensureSession(sessionID)
          report("stop", sessionID)
          break
        case "session.status":
          if (!sessionID || isChildSession(sessionID)) break
          rootSessionID = sessionID
          ensureSession(sessionID)
          if (data?.status?.type === "idle") report("stop", sessionID)
          else if (data?.status?.type === "busy") report("active", sessionID)
          break
        case "permission.asked": {
          // A child dialog still blocks the tree; surface it on the root id.
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          report("permission-blocked", root, { permission_id: data?.id ?? "" })
          break
        }
        case "permission.replied": {
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          report("permission-resolved", root, { permission_id: data?.requestID ?? "", reply: data?.reply ?? "" })
          break
        }
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
      context.tool.hook("execute.before", (input) => {
        const root = resolveSessionID(input.sessionID)
        ensureSession(root)
        if (isChildSession(input.sessionID)) report("active", root)
        else report("active", root, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
      context.tool.hook("execute.after", (input) => {
        const root = resolveSessionID(input.sessionID)
        ensureSession(root)
        if (isChildSession(input.sessionID)) report("active", root)
        else report("active", root, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
    ])

    return async () => {
      controller.abort()
      await queue
      await Promise.allSettled(registrations.map((registration) => registration.dispose()))
      await eventLoop
    }
  },
}
