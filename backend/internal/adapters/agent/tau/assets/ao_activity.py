# agent-orchestrator: managed tau integration (do not edit)
"""Observe Tau's native lifecycle without replacing its prompt or tools."""
import asyncio
import json
import os
import subprocess


def _deliver(name, payload):
    try:
        subprocess.run(
            ["ao", "hooks", "tau", name],
            input=json.dumps(payload) + "\n",
            text=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=5,
            check=False,
            creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
        )
    except (OSError, subprocess.SubprocessError):
        pass


async def _report(name, context, **fields):
    if not os.environ.get("AO_SESSION_ID"):
        return
    await asyncio.to_thread(_deliver, name, {"session_id": context.session_id, **fields})


def setup(tau):
    async def started(event, context):
        await _report("session-start", context)

    async def active(event, context):
        await _report("agent-start", context)

    async def accepted(event, context):
        message = event.message
        if message.role != "user":
            return
        content = message.content
        text = content if isinstance(content, str) else "".join(
            part.text for part in content if getattr(part, "type", None) == "text"
        )
        await _report("user-prompt-submit", context, prompt=text)

    async def settled(event, context):
        await _report("stop", context)

    async def shutdown(event, context):
        if event.reason == "quit":
            await _report("session-end", context)

    tau.on("session_start", started)
    tau.on("agent_start", active)
    tau.on("message_end", accepted)
    # agent_end precedes persistence/retry reconciliation; only settled is idle.
    tau.on("agent_settled", settled)
    tau.on("session_shutdown", shutdown)
