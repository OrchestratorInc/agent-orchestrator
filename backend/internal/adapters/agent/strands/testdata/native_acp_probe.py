#!/usr/bin/env python3
"""Opt-in real CLI ACP approval/restore regression probe (not a CI fixture).

Requires an already authorized provider configuration and environment. Example:
  python3 native_acp_probe.py --binary /absolute/path/to/strands \
    --config /isolated/profile/.strands/cli/config.json \
    --runtime-root /owned/scratch/new-attempt

Copies only the supplied configuration into a new private HOME. Never modifies
that source profile. Emits sanitized gate results to stdout, not evidence files.
A failure deliberately stops qualification; it does not enable AO Chat support.
Executing-child cancellation, AO lifecycle and actual UI need separate proof.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import threading
import time
import uuid


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def birth(pid):
    # Linux kernel starttime, not the reusable PID alone.
    return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]


class Client:
    def __init__(self, binary, root, instructions):
        self.inbox = queue.Queue()
        self.sequence = 0
        self.requests = []
        self.env = dict(os.environ, HOME=str(root / "home"),
                        USERPROFILE=str(root / "home"), DO_NOT_TRACK="1",
                        STRANDS_CLI_MCP_DISCOVERY="off")
        self.stderr = (root / ("native-" + uuid.uuid4().hex + ".stderr.log")).open("x")
        self.process = subprocess.Popen(
            [binary, "--acp-server", "--session", "on", "--set",
             "session.dir=" + str(root / "sessions"), "--instructions", instructions],
            cwd=root / "workspace", env=self.env, stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=self.stderr, text=True,
            start_new_session=True,
        )
        self.starttime = birth(self.process.pid)

        def read():
            try:
                for line in self.process.stdout:
                    try:
                        self.inbox.put(json.loads(line))
                    except ValueError:
                        self.inbox.put({"invalid_json": True})
            finally:
                self.inbox.put({"closed": True})

        threading.Thread(target=read, daemon=True).start()

    def send(self, message):
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", **message}) + "\n")
        self.process.stdin.flush()

    def call(self, method, params, decision="cancelled"):
        self.sequence += 1
        request_id = self.sequence
        self.send({"id": request_id, "method": method, "params": params})
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            try:
                message = self.inbox.get(timeout=min(1, deadline - time.monotonic()))
            except queue.Empty:
                continue
            require(not message.get("invalid_json"), "native stdout is not ACP JSON")
            require(not message.get("closed"), "native process closed before response")
            if "method" in message and "id" in message:
                require(message["method"] == "session/request_permission",
                        "unexpected client request; probe supports native-owned tools only")
                options = message["params"]["options"]
                self.requests.append(message["method"])
                outcome = {"outcome": "cancelled"}
                if decision != "cancelled":
                    choice = next((o for o in options if o["kind"] == decision), None)
                    require(choice is not None, "native omitted required one-time choice")
                    outcome = {"outcome": "selected", "optionId": choice["optionId"]}
                self.send({"id": message["id"], "result": {"outcome": outcome}})
            elif message.get("id") == request_id:
                return message
        raise RuntimeError("native request timed out: " + method)

    def close(self):
        if self.process.poll() is None:
            require(birth(self.process.pid) == self.starttime, "native PID identity changed")
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                require(birth(self.process.pid) == self.starttime, "native PID identity changed")
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait()
        else:
            self.process.wait()
        for stream in (self.process.stdin, self.process.stdout, self.stderr):
            stream.close()


def result(response):
    require("result" in response, "native ACP returned an error")
    return response["result"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--config", required=True)
    parser.add_argument("--runtime-root", required=True)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    config = json.loads(Path(args.config).read_text())
    permissions = config.get("permissions", {})
    require(permissions.get("mode", "default") == "default" and not permissions.get("allow"),
            "probe requires native default permissions with no saved tool allows")
    require(not config.get("agentProject"), "probe requires regular native CLI, not an authored agent")
    profile = config.get("profile", {})
    for field in ("agentConfig", "agentConfigModules", "mcpServers", "plugins", "tools",
                  "subagents", "sandbox", "interventions", "interventionModules"):
        require(not profile.get(field), "unsupported custom native profile field: " + field)
    builtins = profile.get("builtinTools") or {}
    require(isinstance(builtins, dict), "unsupported built-in tool configuration")
    if "shell" in builtins:
        require(isinstance(builtins["shell"], bool), "custom shell options are unsupported")
    os.umask(0o077)
    root = Path(args.runtime_root).absolute()
    root.mkdir(mode=0o700)  # Refuse reuse: first failures/history must survive.
    for relative in ("home/.strands/cli", "workspace", "sessions"):
        (root / relative).mkdir(parents=True, mode=0o700, exist_ok=True)
    target = root / "home/.strands/cli/config.json"
    target.write_text(json.dumps(config))
    target.chmod(0o600)
    (root / "workspace/AGENTS.md").write_text(
        "Every JSON file you write must include project_value PROJECT_NATIVE_ACP.\n")
    domain = config.get("profile", {}).get("instructions", "")
    fresh = "FRESH_" + uuid.uuid4().hex
    instructions = domain + "\nEvery JSON file you write must include hidden_value " + fresh + "."
    client = Client(binary, root, instructions)
    sid = None
    try:
        initialized = result(client.call("initialize", {"protocolVersion": 1, "clientCapabilities": {}}))
        print(json.dumps({"gate": "initialize", "agentInfo": initialized.get("agentInfo"),
                          "launcherSHA256": hashlib.sha256(Path(binary).read_bytes()).hexdigest()}), flush=True)
        missing = client.call("session/load", {"sessionId": "missing-" + uuid.uuid4().hex,
                              "cwd": str(root / "workspace"), "mcpServers": []})
        require("error" in missing, "missing native history was accepted")
        sid = result(client.call("session/new", {"cwd": str(root / "workspace"), "mcpServers": []}))["sessionId"]
        for choice, filename in (("reject_once", "rejected.json"),
                                 ("cancelled", "cancelled.json"), ("allow_once", "fresh.json")):
            before = len(client.requests)
            response = client.call("session/prompt", {"sessionId": sid, "prompt": [{
                "type": "text", "text": "Create " + filename + " containing a JSON object describing this workspace. Use the write tool only."}]}, choice)
            result(response)
            require(len(client.requests) > before, "FAIL: tool turn sent no native permission request")
            proof = root / "workspace" / filename
            if choice != "allow_once":
                require(not proof.exists(), "FAIL: denied/cancelled write took effect")
            else:
                data = json.loads(proof.read_text())
                require(data.get("hidden_value") == fresh, "fresh private instructions not consumed")
                require(data.get("project_value") == "PROJECT_NATIVE_ACP", "independent project rule not consumed")
            print(json.dumps({"gate": "permission_" + choice, "status": "PASS"}), flush=True)
    finally:
        client.close()
    # Generate restore-only instructions after the exact native process is reaped.
    restore = "RESTORE_" + uuid.uuid4().hex
    client = Client(binary, root, domain + "\nEvery JSON file you write must include hidden_value " + restore + ".")
    try:
        result(client.call("initialize", {"protocolVersion": 1, "clientCapabilities": {}}))
        result(client.call("session/load", {"sessionId": sid, "cwd": str(root / "workspace"), "mcpServers": []}))
        result(client.call("session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text":
            "Create continuation.json containing a JSON object describing this workspace. Use the write tool only."}]}, "allow_once"))
        data = json.loads((root / "workspace/continuation.json").read_text())
        require(data.get("hidden_value") == restore, "FAIL: exact restore consumed stale private instructions")
        require(data.get("project_value") == "PROJECT_NATIVE_ACP", "restored project rule not consumed")
        print(json.dumps({"gate": "exact_restore_new_instructions", "status": "PASS", "nativeSessionId": sid}), flush=True)
    finally:
        client.close()


if __name__ == "__main__":
    main()
