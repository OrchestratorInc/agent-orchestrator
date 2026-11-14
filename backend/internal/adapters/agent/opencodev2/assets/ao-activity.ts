// agent-orchestrator: managed opencode-v2 activity plugin (do not edit)
//
// Child (subagent) sessions share this event bus and carry their own ids. They
// are tracked from session.created(parentID) and never reported as the session
// identity: AO persists the reported id as the native restore handle, so a
// child id would make restoration resume the subagent conversation. Child tool /
// execution activity is folded onto the root session as plain `active` so
// subagent work still reads as activity, while child turn boundaries are
// dropped — a child finishing does not end the parent's turn. Roots report tool
// calls as pre-tool-use / post-tool-use with the upstream call id, and a root
// tool still open when the turn ends is reported as post-tool-use-failure, so
// AO's step timeline and in-flight correlation work.
import { spawn } from "node:child_process"

const HOOK_TIMEOUT_MS = 1_250

export default {
  id: "agent-orchestrator.activity.v2",
  async setup(context) {
    const launchID = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
    const seenSessions = new Set()
    const childSessions = new Set()
    // Root tool calls whose `execute.after` has not been seen, mapped to the
    // owning session and the tool name. A turn can end with a tool still open
    // (interrupted, or a failure the harness never reported), and AO closes a
    // step only on a matching post, so the leftovers are closed as failed at
    // that session's own turn boundary instead of showing "running" forever.
    // Ownership is part of the entry: OpenCode 2 runs sessions concurrently, so
    // a hook or boundary from one session must never close, report, or drop
    // another session's calls.
    const toolsInFlight = new Map()
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

    // Close the session's tool steps that never reported their post. Called at
    // that session's turn boundary, where an execution ends without its after
    // hook only if it failed or was interrupted — failed is the accurate
    // reading of the step. Only the boundary's own calls are closed and
    // dropped. The hook input carries no turn id, so a call from a turn that
    // has already begun can still be swept by this session's still-undelivered
    // previous boundary; that mislabels one timeline step, and the alternative
    // is leaving genuinely failed tools open forever.
    function failOpenTools(sessionID) {
      for (const [callID, call] of toolsInFlight) {
        if (call.sessionID !== sessionID) continue
        toolsInFlight.delete(callID)
        report("post-tool-use-failure", sessionID, { tool_name: call.toolName, tool_use_id: callID })
      }
    }

    // Adopt a session as the root AO may see. Root changes do not close
    // anything: sessions run concurrently, so the previous root's open calls
    // still belong to it and are closed by its own boundary, never by a hook
    // or boundary belonging to another session.
    function adoptRoot(sessionID) {
      rootSessionID = sessionID
    }

    // Fold a session id onto the root session AO may see. Children resolve to
    // the current root (null until one is known) and are never forwarded; a
    // non-child id becomes the current root, preserving OpenCode's supported
    // new-native-session flows and AO's checkpoint reset on identity change.
    function resolveSessionID(sessionID) {
      if (!sessionID) return null
      if (childSessions.has(sessionID)) return rootSessionID
      adoptRoot(sessionID)
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
          adoptRoot(sessionID)
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
          adoptRoot(sessionID)
          ensureSession(sessionID)
          // The execution is over, so any still-open tool step ended without its
          // after hook: close it as failed before the turn boundary.
          failOpenTools(sessionID)
          report("stop", sessionID)
          break
        case "session.status":
          if (!sessionID || isChildSession(sessionID)) break
          adoptRoot(sessionID)
          ensureSession(sessionID)
          if (data?.status?.type === "idle") {
            failOpenTools(sessionID)
            report("stop", sessionID)
          } else if (data?.status?.type === "busy") report("active", sessionID)
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
        // A child tool must never donate its call id to the root's bookkeeping.
        if (isChildSession(input.sessionID)) {
          report("active", root)
          return
        }
        const callID = input.id ?? ""
        if (!callID) {
          report("pre-tool-use", root, { tool_name: input.tool ?? "", tool_use_id: callID })
          return
        }
        // Nested Code Mode executions reuse the outer call's id and session, so
        // one id is one lifecycle: the first start opens it, later starts only
        // nest deeper, and the completion that empties the nesting closes it.
        // Otherwise parallel nested tools would overwrite the entry, the first
        // completion would close another running tool's step, and a turn
        // boundary after a nested completion would find no entry to sweep.
        const depth = (toolsInFlight.get(callID)?.depth ?? 0) + 1
        if (depth === 1) {
          toolsInFlight.set(callID, { sessionID: root, toolName: input.tool ?? "", depth })
          report("pre-tool-use", root, { tool_name: input.tool ?? "", tool_use_id: callID })
          return
        }
        toolsInFlight.get(callID).depth = depth
      }),
      context.tool.hook("execute.after", (input) => {
        const root = resolveSessionID(input.sessionID)
        ensureSession(root)
        if (isChildSession(input.sessionID)) {
          report("active", root)
          return
        }
        const callID = input.id ?? ""
        const entry = callID ? toolsInFlight.get(callID) : undefined
        if (entry && entry.depth > 1) {
          // A nested execution finished; the outer call is still running.
          entry.depth--
          return
        }
        if (callID) toolsInFlight.delete(callID)
        // `execute.after` reports the outcome in `status`, with the failure
        // detail in `error`; both are documented on the upstream ToolExecuteAfter
        // hook. Checking `error` too means a future payload that drops `status`
        // cannot silently downgrade a failed tool to a success.
        const failed = input.status === "error" || input.error != null
        report(failed ? "post-tool-use-failure" : "post-tool-use", root, {
          tool_name: input.tool ?? "",
          tool_use_id: callID,
        })
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
