#!/usr/bin/env python3
"""Bounded official-native slash-resume gate. Does not register or run AO."""
import json
from pathlib import Path
import re

from memcode_conformance import NATIVE_ID, ready


def validate_transcript(workspace, native_id):
    """Fail closed before invoking native prefix resolution or silent fallback."""
    if not re.fullmatch(r"sess_(?:[0-9a-f]{8}|[0-9a-f]{16})", native_id):
        raise ValueError("full native session id required")
    path = workspace / ".memcode" / "sessions" / native_id / "messages.json"
    try:
        data = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        raise ValueError("missing or corrupt native transcript") from error
    if not isinstance(data, dict) or data.get("session_id") != native_id:
        raise ValueError("native transcript identity mismatch")
    messages = data.get("messages")
    if not isinstance(messages, list) or not messages:
        raise ValueError("empty native transcript")
    if not all(isinstance(message, dict) and message.get("role") in
               ("user", "assistant", "system", "tool") and
               isinstance(message.get("content"), list) for message in messages):
        raise ValueError("invalid native wire history")
    return len(messages)


def restored(screen, history, witness, generation, sequence, messages):
    """A current-generation load witness and initialized composer are both required."""
    return (
        ready(screen)
        and witness.get("generation") == generation
        and witness.get("native_id") == NATIVE_ID
        and type(witness.get("sequence")) is int
        and witness["sequence"] > sequence
        and ("↩ resumed " + NATIVE_ID + " (" + str(messages) + " messages)") in history
    )


def write_report(path, report):
    """Persist first; a disconnected caller's stdout cannot erase evidence."""
    path.write_text(json.dumps(report, indent=2) + "\n")


import argparse
import shlex
import subprocess
import sys
import time
import uuid

from memcode_conformance import Probe, VERSION, digest, stamp

WITNESS_CODE = """import json, os, sys
from pathlib import Path
path, generation = Path(sys.argv[1]), sys.argv[2]
old = json.loads(path.read_text()) if path.exists() else {}
sequence = old.get("sequence", 0) + 1 if old.get("generation") == generation else 1
record = {"generation": generation, "sequence": sequence,
          "native_id": os.environ.get("MEMCODE_SESSION_ID", "")}
tmp = path.with_suffix(".tmp")
tmp.write_text(json.dumps(record) + "\\n")
tmp.replace(path)
with path.with_suffix(".jsonl").open("a") as log:
    log.write(json.dumps(record) + "\\n")
"""


class SlashProbe(Probe):
    def __init__(self, binary, report, timeout, control, socket_dir):
        super().__init__(binary, report, timeout)
        self.control = control
        self.generation = uuid.uuid4().hex
        self.socket = str(socket_dir / ("mem-" + self.generation[:16] + ".sock"))
        self.witness = self.out / "native-witness.json"
        self.result.update({
            "control": control, "generation": self.generation,
            "scope": "native slash-resume safety control; no AO session or provider turn",
            "hook_contract": "local generation-scoped witness; no synchronous AO API callback",
        })

    def initialize(self):
        super().initialize()
        helper = self.out / "witness.py"
        helper.write_text(WITNESS_CODE)
        hook = shlex.join([sys.executable, "-I", str(helper), str(self.witness), self.generation])
        (self.workspace / ".memcode" / "hooks.json").write_text(json.dumps({
            "hooks": {"session_start": [{"command": hook, "timeout": 5}]},
        }))
        if self.control == "missing":
            self.transcript.unlink()
        elif self.control == "corrupt":
            self.transcript.write_text("{")
        elif self.control == "empty":
            self.transcript.write_text(json.dumps({"session_id": NATIVE_ID, "messages": []}))
        elif self.control == "mismatched":
            data = json.loads(self.transcript.read_text())
            data["session_id"] = "sess_b1b1b1b1"
            self.transcript.write_text(json.dumps(data))
        self.result["transcript_sha256_before"] = digest(self.transcript) if self.transcript.exists() else None

    def read_witness(self):
        try:
            return json.loads(self.witness.read_text())
        except (OSError, ValueError):
            return {}

    def slash_resume(self, fresh_sequence):
        # Only this native command is sent: no task/provider turn.
        argv = ["tmux", "-S", self.socket, "load-buffer", "-b", "resume", "-"]
        self.command(argv, input="/resume " + NATIVE_ID, check=True)
        self.tmux("paste-buffer", "-p", "-b", "resume", "-t", "fresh")
        self.tmux("send-keys", "-t", "fresh", "Enter")
        self.result["native_resume_command_sent"] = "/resume " + NATIVE_ID
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            screen = self.capture("fresh")
            history = self.capture("fresh", True)
            witness = self.read_witness()
            dead = self.tmux("display-message", "-t", "fresh", "-p", "#{pane_dead}").stdout.strip()
            if dead == "1":
                raise RuntimeError("native TUI exited during slash resume")
            # The frontend completion line happens after StartChat returns.
            # It is synchronization only, never identity evidence.
            finished = "↩ resumed " + NATIVE_ID + " — the conversation continues" in history
            missing_rejected = (
                self.control == "missing" and "no resumable sessions yet" in history
                and witness.get("sequence") == fresh_sequence
            )
            if ready(screen) and (missing_rejected or
                                  (finished and witness.get("sequence", 0) > fresh_sequence)):
                return screen, history, witness
            time.sleep(0.1)
        raise RuntimeError("bounded initialized slash-resume readiness timed out")

    def run_control(self):
        passed = False
        try:
            self.initialize()
            try:
                messages = validate_transcript(self.workspace, NATIVE_ID)
                preflight = True
                self.result["preflight"] = {"accepted": True, "message_count": messages}
            except ValueError as error:
                messages, preflight = 2, False
                self.result["preflight"] = {"accepted": False, "reason": str(error)}
            self.result["gates"]["preflight_control"] = (
                "PASS" if preflight == (self.control == "valid") else "FAIL")
            fresh = self.launch("fresh")
            first = self.read_witness()
            first_valid = (
                fresh["status"] == "PASS"
                and first.get("generation") == self.generation
                and first.get("sequence") == 1
                and re.fullmatch(r"sess_(?:[0-9a-f]{8}|[0-9a-f]{16})",
                                 first.get("native_id", "")) is not None
                and first["native_id"] != NATIVE_ID
            )
            self.result["fresh_readiness"] = fresh
            self.result["fresh_witness"] = first
            self.result["gates"]["fresh_initialized_identity"] = "PASS" if first_valid else "FAIL"
            if not first_valid:
                raise RuntimeError("fresh initialized TUI or current-generation identity not proven")
            (self.out / "fresh-before-resume.screen.txt").write_text(self.capture("fresh"))
            # Negative controls deliberately bypass preflight to expose native fallback.
            # A production adapter MUST stop on the rejected preflight above.
            self.result["diagnostic_preflight_bypass"] = not preflight
            screen, history, witness = self.slash_resume(first["sequence"])
            (self.out / "resumed.screen.txt").write_text(screen)
            (self.out / "resumed.history.txt").write_text(history)
            native_loaded = restored(screen, history, witness, self.generation, first["sequence"], messages)
            self.result["resumed_witness"] = witness
            self.result["native_ready_with_exact_identity"] = native_loaded
            self.result["guarded_restore_accepted"] = preflight and native_loaded
            self.result["frontend_claimed_requested_id"] = (
                "↩ resumed " + NATIVE_ID + " — the conversation continues" in history)
            if self.control == "valid":
                control_ok = native_loaded
            elif self.control == "missing":
                control_ok = (not native_loaded and witness == first
                              and "no resumable sessions yet" in history)
            elif self.control in ("corrupt", "empty"):
                control_ok = (not native_loaded and witness.get("native_id") != NATIVE_ID
                              and "could not resume " + NATIVE_ID in history)
            else:
                # Native ignores transcript.session_id; only preflight closes this hole.
                control_ok = not preflight and native_loaded
            self.result["gates"]["observed_native_control"] = "PASS" if control_ok else "FAIL"
            rejected_or_valid = self.result["guarded_restore_accepted"] == (self.control == "valid")
            self.result["gates"]["guarded_identity_decision"] = "PASS" if rejected_or_valid else "FAIL"
            self.result["transcript_sha256_after"] = digest(self.transcript) if self.transcript.exists() else None
            preserved = self.result["transcript_sha256_after"] == self.result["transcript_sha256_before"]
            self.result["gates"]["seeded_transcript_unchanged"] = "PASS" if preserved else "FAIL"
            passed = all(value == "PASS" for value in self.result["gates"].values())
        except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
            self.result["runner_error"] = str(error)
        finally:
            stopped = self.tmux("kill-server", check=False)
            self.result["cleanup"] = "owned tmux server stopped" if stopped.returncode == 0 else (
                "owned tmux server already stopped" if "no server running" in stopped.stderr
                else "cleanup failed: " + stopped.stderr.strip())
            if "cleanup failed" in self.result["cleanup"]:
                passed = False
            self.result["finished_at"] = stamp()
            self.result["verdict"] = "PASS" if passed else "FAIL"
            write_report(self.out / "report.json", self.result)
        return passed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--timeout", type=float, default=30)
    parser.add_argument("--control", action="append",
                        choices=("valid", "corrupt", "empty", "mismatched", "missing"),
                        help="run only selected native controls (default: all)")
    parser.add_argument("--socket-dir", type=Path,
                        default=Path.home() / ".ao" / "audits" / "memcode-sockets")
    args = parser.parse_args()
    if not 1 <= args.timeout <= 60:
        parser.error("--timeout must be between 1 and 60 seconds")
    binary = args.binary.expanduser().resolve(strict=True)
    expected = "89cd4375fce660d6c365163745ef83d78e76ad36e803fe4cebb952644fe4ddff"
    if digest(binary) != expected:
        raise RuntimeError("expected the qualified official Linux x86-64 v0.38.1 executable")
    socket_dir = args.socket_dir.expanduser().resolve()
    if len(str(socket_dir / ("mem-" + "0" * 16 + ".sock")).encode()) >= 104:
        parser.error("--socket-dir is too long for portable Unix sockets")
    socket_dir.mkdir(parents=True, exist_ok=True)
    out = args.report.expanduser().resolve()
    out.mkdir(parents=True, exist_ok=False)
    overall = {
        "scope": "credential-free native controls; no AO registration or provider turn",
        "started_at": stamp(), "binary_sha256": digest(binary), "cases": {},
        "probe_sha256": digest(Path(__file__)),
        "historical_evidence": "prior native/slash controls remain unchanged",
    }
    controls = args.control or ("valid", "corrupt", "empty", "mismatched", "missing")
    for control in controls:
        report = out / control
        report.mkdir()
        probe = SlashProbe(binary, report, args.timeout, control, socket_dir)
        version = probe.command([str(binary), "--version"], check=True).stdout.strip()
        if version != "memcode version " + VERSION:
            raise RuntimeError("expected official pinned memcode " + VERSION)
        passed = probe.run_control()
        overall["cases"][control] = "PASS" if passed else "FAIL"
        overall["finished_at"] = stamp()
        overall["verdict"] = "PASS" if len(overall["cases"]) == len(controls) and all(
            value == "PASS" for value in overall["cases"].values()) else "FAIL"
        write_report(out / "report.json", overall)
        if not passed:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
