---
name: ao-checkup
description: >-
  Health check of Orchestrator and the computers it runs on: daemon checks,
  stuck or stale sessions, coding-agent versions, disk, memory and CPU. Use for
  "health", "checkup", "is Orchestrator ok", "is my Mac ok", "why is it slow",
  or the weekly health check routine.
---
Follow the ao-rules skill. Read-only. Run on every host, in parallel.

## 1. Orchestrator
```sh
f(){ <aoapi helper>; command -v ao >/dev/null && ao doctor --json | python3 -c '
import json,sys; d=json.load(sys.stdin); print("doctor ok" if d.get("ok") else "doctor failures=%s" % d.get("failures"))
for c in d.get("checks") or []:
    if c.get("level")=="FAIL": print("check", c.get("name"), (c.get("message") or "")[:160])'
  aoapi GET 'sessions?active=true' | python3 -c '
import json,sys,time,calendar
now=time.time(); age=lambda t: (now-calendar.timegm(time.strptime(t[:19],"%Y-%m-%dT%H:%M:%S")))/3600 if t else 0
c={"stuck":[],"waiting_1d":[],"exited":0,"failed_setup":[]}
for s in json.load(sys.stdin)["sessions"]:
    st=(s.get("activity") or {}).get("state"); h=age(s.get("lastEventAt"))
    if st=="active" and h>1: c["stuck"].append(s["id"])
    if st in ("waiting_input","blocked") and h>24: c["waiting_1d"].append(s["id"])
    if st=="exited": c["exited"]+=1
    if s.get("provisionState")=="failed": c["failed_setup"].append(s["id"])
print(json.dumps(c))'
  aoapi GET agents/readiness | python3 -c '
import json,sys
for a in json.load(sys.stdin).get("agents") or []:
    if (a.get("installation") or {}).get("state")=="installed": print(a["id"])' | while read -r a; do
    aoapi GET "agents/$a/update-advisory" | python3 -c '
import json,sys; d=json.load(sys.stdin)
if d.get("status")=="behind_latest": print("agent", d.get("agentId"), d.get("currentVersion"), "->", d.get("latestVersion"), "in use:", (d.get("inUse") or {}).get("level"))'; done; }; f
```
Remote host: `ao doctor` runs only on the host itself; skip that line.

## 2. The computer (local hosts)
```sh
f(){ df -h ~ | tail -1; du -sh ~/.ao 2>/dev/null; case "$(uname -s)" in
  Darwin) memory_pressure | tail -1; sysctl -n vm.loadavg hw.ncpu;;
  *) free -h | sed -n 2p; cut -d' ' -f1-3 /proc/loadavg; nproc;; esac; }; f
```

## 3. Report
One line per host: Orchestrator ok or the failing checks, stuck sessions (`active` with no event for over an hour), questions waiting over a day, sessions whose setup failed, agents behind their latest release, then disk free, `~/.ao` size, memory and load against the cores.

Warn when: disk free is under 10%, memory pressure is not normal, load is above the core count, `~/.ao` is over 20 GB, or a session has been stuck over an hour. Suggest fixes, apply none: a stuck session can be looked at (ao-status) or interrupted (ao-actions); old ended sessions can be cleaned up (ao-cleanup); an agent behind its release can be updated (ao-dev-update).

## Weekly health check
Only when the user asks for it: Mondays at 9:30, runs this and sends a message only when something needs attention.
