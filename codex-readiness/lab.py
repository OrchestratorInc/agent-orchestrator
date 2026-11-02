#!/usr/bin/env python3
"""Codex readiness at turn start, before/after lab for PR #7544.

A real daemon and a real Codex chat with isolated AO data. Two turns per build:
  control: a turn with no pending login demand
  demand:  a turn sent after the lab records a refused-login demand
During each turn the lab samples Codex readiness (GET /agents/readiness), the
Codex account stream (SSE /agents/codex/accounts/events) and the chat's
account_json, and reports whether the turn start made Codex readiness stale.

  lab.py up NAME BUILD PORT     # BUILD = before | after
  lab.py shapes NAME            # dump raw readiness / codex account JSON
  lab.py turn NAME PHASE MSG    # PHASE = control | demand
  lab.py down NAME
"""
import json
import os
import signal
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

LAB = Path.home() / ".ao/dev/codex-recheck-lab"
PROJECT = "codexlab"
PROMPT = ("This is an automated test session in a throwaway test repository. Reply with the single word "
          "ready. Only touch files inside this repository; never change anything under ~/.codex or any "
          "other global configuration.")


def log(msg):
    print(time.strftime("[%H:%M:%S] ") + msg, flush=True)


def sh(args, cwd=None, env=None, check=True):
    r = subprocess.run([str(a) for a in args], cwd=cwd, env=env, text=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and r.returncode != 0:
        raise RuntimeError(f"{' '.join(map(str, args))} failed ({r.returncode}): {r.stderr.strip()[:500]}")
    return r.stdout.strip()


def utcnow():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


class Run:
    def __init__(self, name):
        self.name = name
        self.dir = LAB / "runs" / name
        self.state_path = self.dir / "state.json"
        self.state = json.loads(self.state_path.read_text()) if self.state_path.exists() else {}

    def save(self):
        self.state_path.write_text(json.dumps(self.state, indent=2))

    def env(self):
        env = {k: v for k, v in os.environ.items() if not k.startswith(("AO_", "CLAUDE", "CODEX_", "TMUX"))}
        env.update(AO_DATA_DIR=str(self.dir / "data"), AO_RUN_FILE=str(self.dir / "running.json"),
                   AO_PORT=str(self.state["port"]), AO_TELEMETRY_REMOTE="off",
                   GH_CONFIG_DIR=str(self.dir / "data/gh-config"))
        return env

    def ao(self, *args, check=True):
        return sh([self.state["bin"], *args], env=self.env(), check=check)

    def db(self, query, *params, write=False):
        uri = f"file:{self.dir / 'data/ao.db'}" + ("" if write else "?mode=ro")
        con = sqlite3.connect(uri, uri=True, timeout=10)
        try:
            rows = con.execute(query, params).fetchall()
            if write:
                con.commit()
            return rows
        finally:
            con.close()

    def api(self, method, path, body=None, timeout=60):
        req = urllib.request.Request(f"http://127.0.0.1:{self.state['port']}/api/v1{path}", method=method,
                                     data=json.dumps(body).encode() if body is not None else None,
                                     headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return json.loads(resp.read() or b"null")
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"{method} {path}: {e.code} {e.read()[:400]!r}")

    def wait(self, what, fn, timeout, every=1.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                got = fn()
            except (sqlite3.Error, RuntimeError, OSError):
                got = None
            if got:
                log(f"{what}: {got}")
                return got
            time.sleep(every)
        raise RuntimeError(f"timed out waiting for {what}")

    def turns(self):
        return self.db("select t.state, t.started_at, t.completed_at from conversation_turns t join conversations c "
                       "on c.id=t.conversation_id where c.session_id=? order by t.requested_at", self.state["session"])

    def account(self):
        rows = self.db("select account_json from conversations where session_id=?", self.state["session"])
        return json.loads(rows[0][0]) if rows and rows[0][0] else {}


def make_repo(run):
    origin, repo = run.dir / "origin.git", run.dir / "repo"
    sh(["git", "init", "-q", "--bare", "-b", "main", origin])
    sh(["git", "init", "-q", "-b", "main", repo])
    (repo / "README.md").write_text("# Codex lab\n\nThrowaway repository for an AO lab.\n")
    sh(["git", "add", "."], cwd=repo)
    sh(["git", "-c", "user.name=AO Lab", "-c", "user.email=lab@example.com", "commit", "-q", "-m", "init"], cwd=repo)
    sh(["git", "remote", "add", "origin", origin], cwd=repo)
    sh(["git", "push", "-q", "-u", "origin", "main"], cwd=repo)
    sh(["git", "remote", "set-head", "origin", "main"], cwd=repo)
    return repo


def cmd_up(name, build, port):
    run = Run(name)
    run.dir.mkdir(parents=True, exist_ok=False)
    run.state.update(name=name, build=build, bin=str(LAB / "bin" / build / "ao"), port=int(port),
                     builds=json.loads((LAB / "bin/builds.json").read_text()))
    run.save()
    repo = make_repo(run)
    daemon = subprocess.Popen([run.state["bin"], "daemon"], env=run.env(), stdout=open(run.dir / "daemon.log", "w"),
                              stderr=subprocess.STDOUT, start_new_session=True)
    run.state["daemon_pid"] = daemon.pid
    run.save()

    def healthy():
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=2)
            return True
        except Exception:
            return False
    run.wait("daemon healthy", healthy, 120)
    run.ao("project", "add", "--path", repo, "--id", PROJECT, "--name", "Codex Lab")
    body = {"projectId": PROJECT, "harness": "codex", "mode": "chat", "approvalMode": "bypass-permissions",
            "branch": f"codexlab/{name}/root", "prompt": PROMPT, "displayName": "Codex readiness lab"}
    out = run.api("POST", "/sessions", body, timeout=120)
    sid = (out.get("session") or out)["id"]
    run.state["session"] = sid
    run.save()
    log(f"spawned {sid}")
    run.wait("first turn done", lambda: [t for t in run.turns() if t[0] in ("completed", "failed")], 300, every=2)


def readiness_codex(run):
    data = run.api("GET", "/agents/readiness", timeout=10)
    for agent in data.get("agents", data if isinstance(data, list) else []):
        if agent.get("agentId", agent.get("id")) == "codex":
            return agent
    return None


def cmd_shapes(name):
    run = Run(name)
    print("readiness codex:", json.dumps(readiness_codex(run), indent=1)[:3000])
    print("codex accounts:", json.dumps(run.api("GET", "/agents/codex/accounts"), indent=1)[:3000])


def auth_view(agent):
    a = (agent or {}).get("authentication") or {}
    return {k: a.get(k) for k in ("state", "freshness", "checkedAt", "attemptedAt") if k in a}


def sse_reader(run, sink, stop):
    req = urllib.request.Request(f"http://127.0.0.1:{run.state['port']}/api/v1/agents/codex/accounts/events",
                                 headers={"Accept": "text/event-stream"})
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            event, data = "", []
            for raw in resp:
                if stop.is_set():
                    return
                line = raw.decode().rstrip("\n")
                if line.startswith("event:"):
                    event = line[6:].strip()
                elif line.startswith("data:"):
                    data.append(line[5:].strip())
                elif line == "" and data:
                    payload = "\n".join(data)
                    try:
                        body = json.loads(payload)
                    except ValueError:
                        body = payload
                    sink.append({"t": time.time(), "event": event, "accounts": summarize_accounts(body)})
                    event, data = "", []
    except Exception as e:  # stream closes when the daemon stops
        sink.append({"t": time.time(), "event": "stream-error", "accounts": str(e)[:200]})


def summarize_accounts(body):
    if not isinstance(body, dict):
        return body
    accounts = body.get("accounts") or (body.get("snapshot") or {}).get("accounts") or []
    out = []
    for a in accounts:
        auth = a.get("authentication") or {}
        out.append({"id": a.get("id"), "active": a.get("active"), "freshness": auth.get("freshness"),
                    "status": auth.get("status"), "checkedAt": auth.get("checkedAt")})
    return out or {k: body[k] for k in list(body)[:6]}


def cmd_turn(name, phase, message):
    run = Run(name)
    if phase.startswith("demand"):
        reason = "Not logged in"
        account = run.account()
        account.update(authenticationState="required", reauthRequiredAt=utcnow(), reauthReason=reason,
                       lastAuthFailureAt=utcnow(), lastAuthFailureReason=reason,
                       authFailureId=f"lab-{name}-{int(time.time())}")
        account.pop("authVerifiedAt", None)
        run.db("update conversations set account_json=? where session_id=?", json.dumps(account),
               run.state["session"], write=True)
        log("demand recorded")
    # Settle: readiness fresh before the turn, so a flip at turn start is visible.
    run.api("POST", "/agents/readiness/ensure", {"agentIds": ["codex"], "purpose": "display"}, timeout=120)
    time.sleep(3)
    samples, events, stop = [], [], threading.Event()
    reader = threading.Thread(target=sse_reader, args=(run, events, stop), daemon=True)
    reader.start()
    time.sleep(1)
    before = len(run.turns())
    t0 = time.time()
    run.api("POST", f"/sessions/{run.state['session']}/send", {"message": message})
    started = done = None
    while time.time() - t0 < 240:
        turns = run.turns()
        latest = turns[-1] if len(turns) > before else None
        now = time.time()
        if latest and latest[1] and not started:
            started = now
        if latest and latest[0] in ("completed", "failed") and not done:
            done = now
        try:
            ready = auth_view(readiness_codex(run))
        except Exception as e:
            ready = {"error": str(e)[:80]}
        acct = run.account()
        samples.append({"t": now, "readiness": ready, "banner": acct.get("authenticationState"),
                        "turn": latest[0] if latest else None})
        if done and now - done > 4:
            break
        time.sleep(0.25)
    stop.set()
    stale = [s for s in samples if s["readiness"].get("freshness") == "stale"]
    stale_events = [e for e in events if isinstance(e["accounts"], list) and
                    any(a.get("freshness") == "stale" for a in e["accounts"])]
    first = samples[0]["readiness"].get("attemptedAt") if samples else None
    probes = sorted({x["readiness"].get("attemptedAt") for x in samples} - {first, None})
    acct_probes = sorted({a.get("checkedAt") for e in events if isinstance(e["accounts"], list)
                          for a in e["accounts"]} - {None})
    cleared = next((s["t"] for s in samples if s["banner"] != "required" and phase.startswith("demand")), None)
    result = {
        "build": run.state["build"], "phase": phase,
        "turnSeconds": round(done - started, 1) if done and started else None,
        "readinessStaleSamples": len(stale), "samples": len(samples),
        "firstStaleAfterTurnStart": round(stale[0]["t"] - started, 2) if stale and started else None,
        "newCodexAuthProbes": len(probes), "probeTimes": probes,
        "accountCheckTimes": acct_probes,
        "accountEvents": len(events), "accountEventsWithStaleAuth": len(stale_events),
        "bannerFinal": samples[-1]["banner"] if samples else None,
        "bannerClearedAfterTurnStart": round(cleared - started, 2) if cleared and started else None,
    }
    log(json.dumps(result))
    out = run.dir / f"{phase}.json"
    out.write_text(json.dumps({"result": result, "t0": t0, "started": started, "done": done,
                               "samples": samples, "events": events}, indent=1, default=str))


def cmd_down(name):
    run = Run(name)
    run.ao("stop", check=False)
    pid = run.state.get("daemon_pid")
    for _ in range(20):
        if not pid or subprocess.run(["kill", "-0", str(pid)], capture_output=True).returncode != 0:
            break
        time.sleep(1)
    else:
        os.kill(pid, signal.SIGKILL)
    # Chat hosts and their agents outlive the daemon; they carry the lab data dir.
    sh(["pkill", "-f", str(run.dir / "data")], check=False)
    log("down")


if __name__ == "__main__":
    cmd, *rest = sys.argv[1:]
    {"up": cmd_up, "shapes": cmd_shapes, "turn": cmd_turn, "down": cmd_down}[cmd](*rest)
