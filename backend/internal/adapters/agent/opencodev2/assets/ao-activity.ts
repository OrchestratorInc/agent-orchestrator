// agent-orchestrator: managed opencode-v2 activity plugin (do not edit)
//
// Child (subagent) sessions share this event bus and carry their own ids. They
// are tracked from session.created(parentID) and never reported as the session
// identity: AO persists the reported id as the native restore handle, so a
// child id would make restoration resume the subagent conversation. Child tool /
// execution activity is folded onto the root session as plain `active` so
// subagent work still reads as activity, while child turn boundaries are
// dropped — a child finishing does not end the parent's turn. Roots report tool
// calls as pre-tool-use / post-tool-use(-failure) with the upstream call id so
// AO's step timeline and in-flight correlation work.
import { spawn } from "node:child_process"

const HOOK_TIMEOUT_MS = 1_250

export default {
  id: "agent-orchestrator.activity.v2",
  async setup(context) {
    const launchID = (process.env.AO_RUNTIME_LAUNCH_ID ?? "").trim()
    const seenSessions = new Set()
    const childSessions = new Set()
    let rootSessionID = null
    // An inbox item can be re-delivered or reconciled, and a replayed prompt
    // does not clear AO's unsettled checkpoint. Emit each inbox item once.
    const seenInboxItems = new Set()
    // OpenCode 2 publishes a durable execution lifecycle per turn
    // (session.execution.started -> succeeded / failed / interrupted) and also
    // an ephemeral session.status that can report idle between the steps of a
    // still-running execution. Once a session reports execution events, the
    // execution terminal is its turn boundary and an idle status cannot end a
    // turn early.
    const executionSessions = new Set()
    // A turn can end with both a terminal and a trailing status idle, and a
    // coalesced drain can repeat a terminal. Report one stop per turn; only a
    // new user prompt opens the next one.
    const stoppedSessions = new Set()
    // Until the plugin has seen a prompt (for example when it subscribed
    // mid-turn), a new execution segment is the only evidence of a new turn.
    const promptedSessions = new Set()
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

    // A second stop in the same turn would mark the completed conversation
    // checkpoint unsettled, so the turn boundary is emitted at most once.
    function reportStop(sessionID) {
      if (!sessionID || stoppedSessions.has(sessionID)) return
      stoppedSessions.add(sessionID)
      report("stop", sessionID)
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
          if (!isChildSession(sessionID)) {
            executionSessions.add(root)
            // A segment boundary only reopens a turn the plugin never saw a
            // prompt for. Once a prompt is observed, a further segment belongs
            // to that turn: reopening here would let a completed checkpoint be
            // marked unsettled by the turn's own next boundary.
            if (!promptedSessions.has(root)) stoppedSessions.delete(root)
          }
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
          executionSessions.add(sessionID)
          reportStop(sessionID)
          break
        case "session.status":
          if (!sessionID || isChildSession(sessionID)) break
          rootSessionID = sessionID
          ensureSession(sessionID)
          if (data?.status?.type === "idle") {
            // A status-only session (no execution lifecycle on its bus) still
            // gets its boundary, but a session that reports executions is
            // stopped by their terminal so a mid-turn idle cannot end the turn.
            if (executionSessions.has(sessionID)) break
            reportStop(sessionID)
          } else if (data?.status?.type === "busy") {
            if (!promptedSessions.has(sessionID)) stoppedSessions.delete(sessionID)
            report("active", sessionID)
          }
          break
        case "session.inbox.enqueued": {
          // Only real user input is a prompt boundary. Synthetic, compaction,
          // and move items would otherwise mark the session active with no
          // prompt of their own.
          if (!sessionID || isChildSession(sessionID)) break
          const item = data?.item
          if (item?.type !== "user") break
          const inboxID = data?.inboxID || item?.id || ""
          if (inboxID) {
            if (seenInboxItems.has(inboxID)) break
            seenInboxItems.add(inboxID)
          }
          const root = resolveSessionID(sessionID)
          ensureSession(root)
          promptedSessions.add(root)
          stoppedSessions.delete(root)
          // Forward only the prompt text; files and metadata stay native.
          const text = typeof item.payload?.text === "string" ? item.payload.text : ""
          report("user-prompt-submit", root, { prompt: text })
          break
        }
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
        else report("pre-tool-use", root, { tool_name: input.tool ?? "", tool_use_id: input.id ?? "" })
      }),
      context.tool.hook("execute.after", (input) => {
        const root = resolveSessionID(input.sessionID)
        ensureSession(root)
        if (isChildSession(input.sessionID)) report("active", root)
        else
          report(input.status === "error" ? "post-tool-use-failure" : "post-tool-use", root, {
            tool_name: input.tool ?? "",
            tool_use_id: input.id ?? "",
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
