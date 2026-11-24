---
name: ao-watch
description: >-
  Watch an Orchestrator session, its PR, a log or a process and message the
  user when it finishes, needs them, stalls, fails or its checks change. Use
  for "ping me when <session> finishes", "watch <session>", "tell me when CI
  passes on <PR>", "let me know when it's done", "monitor <log>", "keep an eye
  on it overnight".
---
Follow the ao-rules skill. A watch is a routine you create only when the user asks for one. It reports. Under away mode (ao-rules "Decisions") it may also answer, resume and retry, as ao-event says.

## Pick the check
With live events on for that host (memory, ao-live), questions, finished turns, failures and exits already reach chat the moment they happen. A watch is still useful for stalls, PR checks, logs and processes, and on hosts without live events.

- **A session** (`<host>/<id>`): state, a stall (`active` with no event for 30 minutes), its PRs' checks and review, and ended. It prints only what changed since the last run:
```sh
f(){ <aoapi helper>; S='<id>'; W="$HOME/.ao-grok/watch-<name>.json"; mkdir -p ~/.ao-grok
  { aoapi GET "sessions/$S"; echo; aoapi GET "sessions/$S/pr" 2>/dev/null || echo '{}'; } | python3 -c '
import json,sys,time,calendar,os
s,p=[json.loads(l) for l in sys.stdin.read().strip().split("\n",1)]; s=s.get("session",s)
a=(s.get("activity") or {}).get("state",""); t=(s.get("lastEventAt") or "")[:19]
age=time.time()-calendar.timegm(time.strptime(t,"%Y-%m-%dT%H:%M:%S")) if t else 0
now={"state":"ended" if s.get("isTerminated") else "stalled" if a=="active" and age>1800 else a,
     "prs":sorted("#%s %s ci=%s review=%s" % (x.get("number"),x.get("state"),(x.get("ci") or {}).get("state"),(x.get("review") or {}).get("decision")) for x in p.get("prs") or [])}
w=sys.argv[1]; old=json.load(open(w)) if os.path.exists(w) else None; json.dump(now,open(w,"w"))
if old is None: print("first", json.dumps(now))
elif old!=now: print("changed", json.dumps(old), "->", json.dumps(now))' "$W"; }; f
```
- **A log**: `tail -n 30 '<log>'`. **A process**: `pgrep -fl '<name>'`. Keep the last output in `~/.ao-grok/watch-<name>.txt` and report only a change.
- **A PR by URL**: `gh pr view '<url>' --json state,statusCheckRollup,reviewDecision` on a computer where `gh` is signed in.

## Start a watch
1. Find the target and run the check once now with the same state file. Tell the user the current state in one line.
2. Create a routine every 10 minutes (5 if they say "soon") with the prompt below filled in. Give each watch its own name and state file.
3. Reply in one line: what you watch, how often, and the state now.

A watch runs until the user stops it, unless they gave an end. When the work is done, say so and offer to stop it.

### Routine prompt
```
Watch <host>/<target> (<name>) from <via computer>. Follow ao-rules and ao-watch.
Mode: <report | away until <end>>.
Check: <the command from "Pick the check">
- No output: send nothing, write nothing to memory.
- first: send nothing.
- changed to idle: "<target> finished", two lines from its last reply (ao-status "One session in detail"), offer files or its diff (ao-actions) and to stop this watch. Skip it if a live event already reported this finish.
- changed to waiting_input or blocked: run ao-answer for that session.
- changed to stalled: one line with the last reply's end. Don't interrupt it.
- changed to exited or ended, or a PR's ci or review changed: one line each. ci=failing: offer to ask the session's orchestrator to fix it.
- Host unreachable: tell the user once, then check quietly until it is back.
```

## Away mode
Before the first check, save the scope and end in memory and confirm in one line what you'll still ask about. On a Mac host, offer keep-awake (ao-actions). Act as ao-event "Who decides" allows. Keep a short log of what you did and why, and send it when the user is back or the watch ends. Never nudge a stalled session more than twice: keep the count in memory ("watch <name>: nudges = n").

## Stop
"Stop watching <x>": delete that routine and its state file. "Stop all watches": list them first, then delete after a yes.
