#!/usr/bin/env python3
"""Bounded native memcode TUI restore gate. This does not test or register AO."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import time
from datetime import datetime, timezone

VERSION = "0.38.1"
NATIVE_ID = "sess_a0a0a0a0"
MODEL = "ao-native-restore-fixture"
PANIC = "panic: ui: AppendString called without primary screen support"


def stamp():
    return datetime.now(timezone.utc).isoformat()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def ready(screen):
    """Require current native chrome, not an old ready banner or hook identity."""
    lines = [line.strip() for line in screen.splitlines()]
    return (
        "○ idle" in lines
        and "→  Ask memcode…   ·   $ = shell" in lines
        and any(line.startswith("memcode · ") and
                line.endswith(MODEL + " · allow-all") for line in lines)
        and PANIC not in screen
    )


def fixture(workspace):
    native = workspace / ".memcode" / "sessions" / NATIVE_ID
    native.mkdir(parents=True)
    path = native / "messages.json"
    path.write_text(json.dumps({
        "session_id": NATIVE_ID, "saved_at": stamp(),
        "messages": [
            {"role": "user", "content": [{"type": "text", "text": "Record this fixture."}]},
            {"role": "assistant", "content": [{"type": "text", "text": "Fixture recorded."}]},
        ],
    }) + "\n")
    return path


def isolated_env(home):
    # Do not inherit credentials, user profiles, provider selection, or proxy secrets.
    return {
        "PATH": os.environ.get("PATH", os.defpath),
        "HOME": str(home), "XDG_CONFIG_HOME": str(home / "config"),
        "XDG_CACHE_HOME": str(home / "cache"), "XDG_DATA_HOME": str(home / "data"),
        "TMPDIR": str(home / "tmp"), "TERM": "xterm-256color", "LC_ALL": "C.UTF-8",
        "MEMCODE_AUTO_UPDATE": "off", "MEMCODE_REEXEC": "1",
        "MEMCODE_ENDPOINT_URL": "http://127.0.0.1:9/v1",
        "MEMCODE_API_URL": "http://127.0.0.1:9",
        "MEMCODE_ENDPOINT_MODEL": MODEL,
    }


class Probe:
    def __init__(self, binary, report, timeout):
        self.binary, self.out, self.timeout = binary, report, timeout
        self.home, self.workspace = report / "home", report / "workspace"
        for directory in (self.home / "tmp", self.home / ".memcode", self.workspace):
            directory.mkdir(parents=True)
        self.env = isolated_env(self.home)
        # Disable the passive update notice without changing the pinned executable.
        (self.home / ".memcode" / "update-check.json").write_text(json.dumps({
            "checked_at": stamp(), "latest": VERSION,
        }))
        self.socket = str(report / "tmux.sock")
        self.result = {
            "scope": "native-only; AO not registered or tested",
            "started_at": stamp(), "version": VERSION, "binary_sha256": digest(binary),
            "fixture": "synthetic two-message native transcript; no provider turns",
            "gates": {}, "screenshots": [],
        }

    def command(self, argv, **kwargs):
        return subprocess.run(argv, env=self.env, capture_output=True, text=True,
                              timeout=10, **kwargs)

    def tmux(self, *args, check=True):
        return self.command(["tmux", "-S", self.socket, *args], check=check)

    def write_report(self):
        (self.out / "report.json").write_text(json.dumps(self.result, indent=2) + "\n")

    def initialize(self):
        self.command(["git", "init", "-q", str(self.workspace)], check=True)
        (self.workspace / "README.md").write_text("Disposable native memcode restore fixture.\n")
        self.command(["git", "-C", str(self.workspace), "add", "."], check=True)
        self.command(["git", "-C", str(self.workspace), "-c", "user.name=AO fixture",
                      "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"],
                     check=True)
        config = self.workspace / ".memcode"
        config.mkdir()
        (config / "config.json").write_text(json.dumps({"theme": "dark", "models": {"coder": MODEL}}))
        hook = "printf '%s' \"$MEMCODE_SESSION_ID\" > " + shlex.quote(str(self.out / "native-id"))
        (config / "hooks.json").write_text(json.dumps({
            "hooks": {"session_start": [{"command": hook, "timeout": 5}]},
        }))
        self.transcript = fixture(self.workspace)
        self.result["transcript_sha256_before"] = digest(self.transcript)

    def capture(self, session, history=False):
        args = ["capture-pane", "-t", session, "-p"]
        if history:
            args += ["-S", "-2000"]
        text = self.tmux(*args).stdout
        (self.out / (session + (".history.txt" if history else ".screen.txt"))).write_text(text)
        return text

    def launch(self, name, resume=False):
        self.tmux("new-session", "-d", "-s", name, "-x", "160", "-y", "58",
                  "-c", str(self.workspace), "sleep 300")
        self.tmux("set-option", "-t", name, "remain-on-exit", "on")
        self.tmux("set-option", "-t", name, "status-left",
                  "NATIVE memcode | AO NOT REGISTERED / NOT TESTED ")
        self.tmux("set-option", "-t", name, "status-left-length", "90")
        argv = [str(self.binary), "run", "--allow-all"]
        if resume:
            argv += ["--resume", NATIVE_ID]
        self.tmux("respawn-pane", "-k", "-t", name, shlex.join(argv))
        end = time.monotonic() + self.timeout
        while time.monotonic() < end:
            screen = self.capture(name)
            dead = self.tmux("display-message", "-t", name, "-p", "#{pane_dead}").stdout.strip()
            if dead == "1":
                status = self.tmux("display-message", "-t", name, "-p",
                                   "#{pane_dead_status}").stdout.strip()
                history = self.capture(name, True)
                return {"status": "FAIL", "exit_code": status,
                        "reason": PANIC if PANIC in history else "native process exited"}
            history = self.capture(name, True)
            native = (self.out / "native-id").read_text() if (self.out / "native-id").exists() else ""
            if ready(screen) and (not resume or
                                  ("resumed " + NATIVE_ID in history and native == NATIVE_ID)):
                return {"status": "PASS", "native_id": native}
            time.sleep(0.1)
        return {"status": "FAIL", "reason": "native input readiness timed out"}

    def screenshot(self, session):
        """Capture the real retained terminal in Xvfb; never rasterize old log text."""
        for binary in ("Xvfb", "xterm", "scrot"):
            if not shutil.which(binary):
                self.result["screenshots"].append({"status": "unavailable",
                                                   "reason": binary + " not installed"})
                return
        display = next(n for n in range(110, 160)
                       if not Path(f"/tmp/.X{n}-lock").exists()
                       and not Path(f"/tmp/.X11-unix/X{n}").exists())
        env = dict(self.env, DISPLAY=f":{display}")
        xvfb = xterm = None
        with (self.out / "screenshot.log").open("wb") as log:
            try:
                xvfb = subprocess.Popen(
                    ["Xvfb", f":{display}", "-screen", "0", "1640x1240x24", "-nolisten", "tcp"],
                    env=env, stdout=log, stderr=log)
                for _ in range(60):
                    if Path(f"/tmp/.X11-unix/X{display}").exists():
                        break
                    if xvfb.poll() is not None:
                        raise RuntimeError("Xvfb exited")
                    time.sleep(0.05)
                xterm = subprocess.Popen([
                    "xterm", "-geometry", "160x59+0+0", "-fn", "10x20", "-bg", "#171717",
                    "-fg", "#dddddd", "-title", "Native memcode failure - AO NOT TESTED",
                    "-e", "tmux", "-S", self.socket, "attach-session", "-r", "-t", session,
                ], env=env, stdout=log, stderr=log)
                time.sleep(0.8)
                if xterm.poll() is not None:
                    raise RuntimeError("xterm exited")
                self.tmux("copy-mode", "-t", session)
                self.tmux("send-keys", "-t", session, "-X", "history-top")
                time.sleep(0.2)
                target = self.out / "native-resume-failure.png"
                started = stamp()
                subprocess.run(["scrot", str(target)], env=env, check=True, timeout=10,
                               stdout=log, stderr=log)
                self.result["screenshots"].append({
                    "status": "captured", "file": target.name, "sha256": digest(target),
                    "capture_started_at": started, "capture_finished_at": stamp(),
                    "surface": "real xterm attached read-only to this run's retained native tmux pane",
                    "classification": "new native-only failure reproduction; AO not registered/tested",
                    "native_process_state": ("exited; output retained in the original pane"
                                             if self.tmux("display-message", "-t", session, "-p",
                                                          "#{pane_dead}").stdout.strip() == "1"
                                             else "running at capture"),
                })
            except (OSError, subprocess.SubprocessError, RuntimeError) as error:
                self.result["screenshots"].append({"status": "unavailable", "reason": str(error)})
            finally:
                for process in (xterm, xvfb):
                    if process is not None:
                        process.terminate()
                        try:
                            process.wait(timeout=5)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait(timeout=5)

    def run(self, screenshots):
        try:
            self.initialize()
            self.result["gates"]["fresh_tui_readiness"] = self.launch("fresh")
            self.tmux("kill-session", "-t", "fresh")
            self.result["gates"]["exact_native_restore"] = self.launch("resume", True)
            self.result["transcript_sha256_after"] = digest(self.transcript)
            if self.result["transcript_sha256_before"] != self.result["transcript_sha256_after"]:
                self.result["gates"]["transcript_preserved"] = {"status": "FAIL"}
            else:
                self.result["gates"]["transcript_preserved"] = {"status": "PASS"}
            if screenshots and self.result["gates"]["exact_native_restore"]["status"] != "PASS":
                self.screenshot("resume")
        except (OSError, subprocess.SubprocessError, RuntimeError) as error:
            self.result["runner_error"] = str(error)
        finally:
            cleanup = self.tmux("kill-server", check=False)
            self.result["cleanup"] = "owned tmux server stopped" if cleanup.returncode == 0 else (
                "tmux server already stopped" if "no server running" in cleanup.stderr
                else "inspect screenshot.log and process state")
            self.result["finished_at"] = stamp()
            # A local fixture pass is only the native startup/restore gate, never full integration.
            passed = not self.result.get("runner_error") and len(self.result["gates"]) == 3 and all(
                gate["status"] == "PASS" for gate in self.result["gates"].values())
            self.result["verdict"] = "NATIVE_RESTORE_GATE_PASS" if passed else "NATIVE_RESTORE_GATE_FAIL"
            self.write_report()
        return 0 if passed else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--report", required=True, type=Path,
                        help="new evidence directory; existing attempts are never overwritten")
    parser.add_argument("--timeout", type=float, default=30, help="1-120 seconds per startup")
    parser.add_argument("--screenshots", action="store_true")
    args = parser.parse_args()
    if not 1 <= args.timeout <= 120:
        parser.error("--timeout must be between 1 and 120 seconds")
    binary = args.binary.expanduser().resolve(strict=True)
    report = args.report.expanduser().resolve()
    report.mkdir(parents=True, exist_ok=False)
    probe = Probe(binary, report, args.timeout)
    version = probe.command([str(binary), "--version"], check=True).stdout.strip()
    if version != "memcode version " + VERSION:
        raise SystemExit("Expected memcode " + VERSION + "; review the pinned native contract first")
    return probe.run(args.screenshots)


if __name__ == "__main__":
    raise SystemExit(main())
