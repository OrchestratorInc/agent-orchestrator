---
name: ao-status
description: >-
  What AO is doing on every host: which sessions are running, waiting on the
  user, idle or exited, which PRs are ready to merge or got a review, and a
  daily digest. Read-only. Use for "status", "what's running", "what's waiting
  on me", "what's ready to merge", "anything stuck", and the AO daily digest
  routine.
---
Follow the ao-rules skill. Read-only in AO: it never marks anything read there. It keeps its own seen list in `~/.ao-grok/` on each `via` computer. Run on every host in the **AO hosts** note, in parallel, each from its `via` computer with the `aoapi` helper (set `AO_REMOTE_BASE` and `AO_REMOTE_HEADER` for remote hosts). If memory has no **AO hosts** note, run ao-setup first. Don't save hosts without the user's yes.

## 1. Sessions
```sh
f(){ <aoapi helper>; aoapi GET 'sessions?active=true' | python3 -c '
import json,sys
for s in json.load(sys.stdin)["sessions"]:
    a=s.get("activity") or {}
    print("\t".join([s["id"], s.get("kind",""), a.get("state",""), str(len(s.get("prs") or [])), (s.get("displayName") or "")[:60]]))'; }; f
```
Columns: id, kind (`orchestrator` or `worker`), state, PR count, name. States:

| State | Say |
|---|---|
| `active` | running |
| `waiting_input`, `blocked` | waiting on you |
| `idle` | idle: finished its turn, or not started |
| `exited` | the agent exited; it can be resumed (ao-actions) |

For what's waiting on the user, run ao-answer "Find what's waiting": it finds sessions in `waiting_input` or `blocked`, and idle sessions whose last reply asked the user a question. Never use notifications for it. Group by host, then by project (the session id starts with the project id).

Keep the reply short enough for a phone, at most 6 lines: first what needs the user, then what's running, then one line per host with counts (for example "MacBook Pro: 25 idle, 9 exited"). Name a session only when it needs the user or is running. Leave out idle orchestrators, PR numbers and exited sessions unless asked. Report what you saw; don't guess why (a crash, a restart) unless the user asks.

## 2. PRs and reviews
```sh
f(){ <aoapi helper>
  aoapi GET 'notifications?status=unresolved' | python3 -c '
import json,sys
for n in json.load(sys.stdin)["notifications"]:
    if n.get("type")=="ready_to_merge": print("\t".join(["ready_to_merge", n.get("sessionId",""), n.get("prUrl",""), (n.get("title") or "")[:80]]))'
  SEEN="$HOME/.ao-grok/<host name>.seen-news"; mkdir -p ~/.ao-grok; touch "$SEEN"
  aoapi GET 'notifications?status=unread' | python3 -c '
import json,sys
seen=set(open(sys.argv[1]).read().split()); new=[]
for n in json.load(sys.stdin)["notifications"]:
    if n.get("type") in ("review_completed","review_changes_requested","pr_merged","pr_closed_unmerged") and not n.get("resolvedAt") and n.get("id") not in seen:
        new.append(n["id"]); print("\t".join([n["type"], n.get("sessionId",""), n.get("prUrl",""), (n.get("title") or "")[:80]]))
open(sys.argv[1],"a").writelines(i+"\n" for i in new)' "$SEEN"
  tail -n 500 "$SEEN" > "$SEEN.tmp" && mv "$SEEN.tmp" "$SEEN"; }; f
```
- `ready_to_merge` comes from the unresolved list, so a PR that was merged or changed since drops off. For "what's ready to merge", list these with the PR link, and offer the merge (ao-actions; needs the user's yes per PR).
- `review_completed`, `review_changes_requested`, `pr_merged`, `pr_closed_unmerged` are news. The command prints only news this bot hasn't reported before, and records it as reported, so each item appears once, in a status reply or in the digest, whichever comes first. Mention each in one line. For "what's new again" or "show all news", run it without the seen list.
- Never use `needs_input` notifications. They can outlive the wait (the session answered or exited); section 1 is the source for what's waiting.

## 3. One session in detail
For "what is <session> doing": find it (ask which host if the id is on two hosts), then read its last turn:
```sh
f(){ <aoapi helper>; aoapi GET "sessions/<id>/history?offset=0&limit=1" | python3 -c '
import json,sys
t=(json.load(sys.stdin).get("turns") or [{}])[0]
print("asked:", (t.get("userRequest") or "")[:500]); print("replied:", (t.get("agentResponse") or "")[-1500:])'; }; f
```
Summarise in two or three lines. For "catch me up on <project>", ask the project's orchestrator instead (ao-orchestrator). It has the workers' reports.

## Digest
The **AO daily digest** routine runs this skill. Per host: what finished since the last digest (idle sessions with `lastEventAt` since then), what's waiting on the user, what's ready to merge, and anything exited unexpectedly. Skip quiet projects. Send nothing if nothing happened and nothing is waiting. Write the time of this digest to `~/.ao-grok/<host name>.last-digest` on `via` and read it next time, instead of keeping it in memory.
