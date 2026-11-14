#!/usr/bin/env python3
"""Opt-in Strands native cancellation probe. Never runs from the default Go tests.

Provide an already prepared disposable native profile and authorized provider
credentials in the environment. This probe approves exactly one displayed shell
command in that disposable workspace. Output is terminal text, not screenshots.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import termios
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", required=True)
parser.add_argument("--home", required=True)
parser.add_argument("--report", required=True)
parser.add_argument("--interrupt", choices=["escape", "ctrl-c"], required=True)
args = parser.parse_args()
profile = Path(args.home).resolve()
report = Path(args.report).resolve()
if profile == Path.home().resolve() or not (profile / ".strands/cli/config.json").is_file():
    parser.error("--home must select a prepared disposable native profile, not the real home")
report.mkdir(parents=True, exist_ok=False)
workspace = report / "workspace"
workspace.mkdir()
binary = str(Path(args.binary).resolve())
version = subprocess.check_output([binary, "--version"], text=True).strip()
if version != "0.2.0":
    parser.error("this probe is pinned to Strands CLI 0.2.0")
command = "sleep 30; printf done > CANCEL_SHOULD_NOT_EXIST"
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 160, 0, 0))
env = dict(os.environ, HOME=str(profile), TERM="xterm-256color")
proc = subprocess.Popen(
    [binary, "--session", "on", "--session-id", "ao-cancel-probe",
     "--set", "session.dir=" + str(report / "sessions"),
     "--prompt", "Use the shell tool to execute exactly: " + command + ". Do not run any other tool."],
    cwd=workspace, env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True,
)
os.close(slave)


def children():
    rows = subprocess.check_output(["ps", "-eo", "pid,ppid,pgid,args"], text=True).splitlines()[1:]
    owned, result = {proc.pid}, []
    for _ in range(8):
        for row in rows:
            fields = row.split(None, 3)
            if int(fields[1]) in owned and int(fields[0]) not in owned:
                owned.add(int(fields[0]))
                result.append(row)
    return "\n".join(result)


raw, approved, interrupted, after_five = b"", 0, 0, False
started = time.monotonic()
try:
    while time.monotonic() - started < 90:
        if select.select([master], [], [], 0.2)[0]:
            try:
                raw += os.read(master, 65536)
            except OSError:
                break
            (report / "terminal.raw").write_bytes(raw)
        tail = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", raw[-40000:].decode(errors="replace"))
        if not approved and "Allow once" in tail and "Enter choose" in tail and command in tail:
            os.write(master, b"\r")
            approved = time.monotonic()
        if approved and not interrupted and time.monotonic() - approved > 2:
            (report / "active-processes.txt").write_text(children())
            os.write(master, b"\x1b" if args.interrupt == "escape" else b"\x03")
            interrupted = time.monotonic()
        if interrupted and not after_five and time.monotonic() - interrupted > 5:
            (report / "processes-after-five-seconds.txt").write_text(children())
            after_five = True
        if interrupted and time.monotonic() - interrupted > 35:
            break
        if proc.poll() is not None:
            break
finally:
    # The probe owns this terminal process. Record descendants before teardown;
    # Strands shell tools create separate process groups, so reap those too.
    remaining = children()
    (report / "remaining-processes.txt").write_text(remaining)
    for row in remaining.splitlines():
        pid = int(row.split()[0])
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    if proc.poll() is None:
        os.killpg(proc.pid, signal.SIGKILL)
    proc.wait()
    os.close(master)
    late_write = (workspace / "CANCEL_SHOULD_NOT_EXIST").exists()
    result = {
        "cliVersion": version,
        "executableSHA256": hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
        "interrupt": args.interrupt,
        "approvalOnceSent": bool(approved),
        "interruptSent": bool(interrupted),
        "lateSideEffect": late_write,
        "verdict": "FAIL" if late_write else "REVIEW_PROCESS_AND_TERMINAL_EVIDENCE",
        "qualification": "Native CLI evidence only. No AO integration or screenshots are claimed.",
    }
    (report / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))
