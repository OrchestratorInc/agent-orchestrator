"""Pinned native wire conformance against a synthetic loopback endpoint; CI only.

This records no authenticated vendor traffic and is not live provider evidence.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import tempfile
import threading
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary")
    args = parser.parse_args()
    requests = []
    captured = threading.Event()

    class Endpoint(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_GET(self):
            body = json.dumps({"object": "list", "data": [
                {"id": model, "object": "model", "owned_by": "fixture"}
                for model in ("glm-4.7", "glm-5.3-flash")
            ]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if "messages" in body:
                requests.append(body)
                if any("PRIVATE_NATIVE_ROLE_6515" in json.dumps(message)
                       for message in body["messages"]):
                    captured.set()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for delta, finish in [({"role": "assistant", "content": "Fixture complete."}, None), ({}, "stop")]:
                chunk = {"id": "synthetic", "object": "chat.completion.chunk", "model": "glm-4.7",
                         "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
                self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()

    with tempfile.TemporaryDirectory(prefix="memcode-role-") as directory:
        root = Path(directory)
        workspace = root / "workspace"
        workspace.mkdir()
        subprocess.run(["git", "init", "-q", str(workspace)], check=True)
        (workspace / "MEMCODE.md").write_text("Preserve PROJECT_NATIVE_ROLE_6515 as a standing project rule.\n")
        native_config = workspace / ".memcode"
        native_config.mkdir()
        (native_config / "hooks.json").write_text(json.dumps({"hooks": {"session_start": [
            {"command": "printf '%s' 'PRIVATE_NATIVE_ROLE_6515 is private standing context.'"}
        ]}}))
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Endpoint)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        env = os.environ.copy()
        env.pop("MEMCODE_API_TOKEN", None)
        env.update({"XDG_CONFIG_HOME": str(root / "xdg"), "MEMCODE_AUTO_UPDATE": "off",
                    "MEMCODE_ENDPOINT_URL": f"http://127.0.0.1:{server.server_port}/v1",
                    "MEMCODE_ENDPOINT_MODEL": "glm-4.7", "MEMCODE_ENDPOINT_KEY": "synthetic-only",
                    "TERM": "xterm-256color"})
        master, slave = pty.openpty()
        process = subprocess.Popen([str(Path(args.binary).resolve()), "run", "--allow-all", "--",
                                    "-Ordinary role fixture task: answer briefly."], cwd=workspace, env=env,
                                   stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)
        try:
            deadline = time.monotonic() + 90
            while not captured.is_set() and time.monotonic() < deadline and process.poll() is None:
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        os.read(master, 65536)
                    except OSError:
                        break
            assert captured.is_set(), "released native never sent private hook context"
            request = next(request for request in requests if any(
                "PRIVATE_NATIVE_ROLE_6515" in json.dumps(message) for message in request["messages"]))
            messages = request["messages"]
            system = "\n".join(json.dumps(message.get("content")) for message in messages if message["role"] == "system")
            assert "PRIVATE_NATIVE_ROLE_6515" in system, "private hook was not sent as system role"
            assert "PROJECT_NATIVE_ROLE_6515" in system, "native project rules were replaced"
            assert "Core laws" in system and "Truth hierarchy" in system, "native default doctrine was replaced"
            assert not any("PRIVATE_NATIVE_ROLE_6515" in json.dumps(message)
                           for message in messages if message["role"] != "system")
            assert any(message["role"] == "user" and "-Ordinary role fixture task" in json.dumps(message)
                       for message in messages), "visible task lost its separate user role"
            print(json.dumps({"scope": "SYNTHETIC_RELEASED_NATIVE_WIRE_ONLY", "privateRole": "system",
                              "nativeDefaultsPresent": True, "nativeProjectRulesPresent": True,
                              "visibleTaskRole": "user", "authenticatedVendorWire": "NOT_CAPTURED"}))
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            os.close(master)
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
