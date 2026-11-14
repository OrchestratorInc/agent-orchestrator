---
name: ao-actions
description: >-
  Direct actions on one Orchestrator session or PR: stop or interrupt, resume, merge a PR,
  start a review, screenshot a session's browser, send a session's files or
  diff, rename, pin, switch model, retry a failed turn, resume after a usage
  limit, search all chats, keep a host awake, and recurring Orchestrator
  automations. Use for "stop <session>", "merge <PR>", "review <session>'s PR",
  "show me what <session> built", "diff of <session>", "rename <session>",
  "pin <session>", "switch <session> to <model>", "retry it", "resume after the
  limit", "find where we talked about <words>", "keep the Mac awake tonight",
  "every night, ...".
---
Follow the ao-rules skill. Find the session first (ao-rules "AO hosts": `<host>/<session id>`). Run each action on the owner host from its `via` computer. "Yes" means the user's yes for that exact action in this chat.

| Ask | Do | Yes needed | Hosts |
|---|---|---|---|
| Stop what it's doing now | `aoapi POST "sessions/<id>/conversation/interrupt"` | No | All |
| End the session | `aoapi POST "sessions/<id>/kill" '{}'` | Yes | All |
| Resume an exited agent | `aoapi POST "sessions/<id>/resume-agent" '{}'` | No | All |
| Restore an ended session | `aoapi POST "sessions/<id>/restore" '{}'` (an ended orchestrator can't come back while its project has another live one) | Yes | All |
| Merge a PR | see "Merge" | Yes, per PR | All |
| Start Orchestrator's reviewer on a session's PR | `ao review trigger <id>` | Yes | Local |
| Screenshot a session's browser | see "Screenshot" | No | Local |
| A session's files | see "Files" | No | Local |
| Keep the host awake | see "Keep awake" | Yes | Local |
| Recurring agent work | see "Automations" | Yes | Local |
| Start one worker directly | `aoapi POST sessions '<json>'` with `projectId`, `displayName`, `prompt` | Yes | All |
| Rename | `aoapi PATCH "sessions/<id>" "$(python3 -c 'import json,sys; print(json.dumps({"displayName":sys.argv[1]}))' '<name>')"` | No | All |
| Pin, unpin | `aoapi POST "sessions/<id>/pin"`, `aoapi DELETE "sessions/<id>/pin"` | No | All |
| Switch model or effort | see "Model" | Yes | All |
| Retry a failed turn | `aoapi POST "sessions/<id>/conversation/turns/<turn id>/retry"` | Yes | All |
| Resume when a usage limit resets | see "Usage limit" | Yes | All |
| Search every chat | see "Search" | No | All |
| A session's diff | see "Diff" | No | All |

For an action marked Local on a remote host, ask that host's orchestrator to do it (ao-orchestrator).

Start a worker directly only when the project has no orchestrator and the user doesn't want one (ao-rules "orchestrator first").

## Model
List what the session can use, then show the current pick and the choices as one card:
```sh
f(){ <aoapi helper>; aoapi GET "sessions/<id>/conversation/models" | python3 -c '
import json,sys
d=json.load(sys.stdin); print("now:", json.dumps(d.get("selected")))
for m in d.get("models") or []: print(json.dumps(m)[:200])'; }; f
```
An empty list means the session doesn't offer a choice (a terminal session, for example): say so. On the pick and a yes, send only what changes:
`aoapi PATCH "sessions/<id>/conversation/settings" "$(python3 -c 'import json,sys; print(json.dumps(json.loads(sys.argv[1])))' '{"model":"<model id>","reasoningEffort":"<effort>"}')"`
It applies from the next turn. A `409` means a turn is running: say it will apply after it, or offer to interrupt first.

## Usage limit
A turn that hit a usage limit has `usageLimit` in `aoapi GET "sessions/<id>/conversation"` (`turns`, the last one with `state` `failed`). With `resetsAt` ahead, on yes: `aoapi PATCH "sessions/<id>/conversation/turns/<turn id>/auto-resume" '{"enabled":true}'`, and say when it continues. `{"enabled":false}` cancels it. With no `resetsAt`, Orchestrator can't schedule it: offer to retry later.

## Search
```sh
f(){ <aoapi helper>; aoapi GET "conversations/search?query=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1]))' '<words>')" | python3 -c '
import json,sys
for r in json.load(sys.stdin)["results"][:10]: print(r["sessionId"], (r.get("title") or "")[:50], "|", r["text"].replace("\x02","**").replace("\x03","**")[:200])'; }; f
```
Run it on every host. Give each hit as `<host>/<session>`, its title and the passage.

## Diff
```sh
f(){ <aoapi helper>; S='<id>'; aoapi GET "sessions/$S/workspace/manifest" | python3 -c '
import json,sys
d=json.load(sys.stdin); s=d.get("summary") or {}; print("files=%s +%s -%s" % (s.get("files"),s.get("additions"),s.get("deletions")))
for f in d.get("files") or []: print(f["status"], "+%s -%s" % (f.get("additions"),f.get("deletions")), f["path"])'; }; f
```
That is the summary. For the patch of some files (at most 100 paths), write it to a file and send it:
```sh
f(){ <aoapi helper>; mkdir -p ~/.ao-grok/out; aoapi POST "sessions/<id>/workspace/diffs" "$(python3 -c 'import json,sys; print(json.dumps({"scope":"combined","paths":sys.argv[1:]}))' '<path>' '<path>')" | python3 -c '
import json,sys
print("".join(g.get("patch") or "" for g in json.load(sys.stdin)["groups"]))' > ~/.ao-grok/out/<id>.diff && wc -l ~/.ao-grok/out/<id>.diff; }; f
```
Then CopyToBox and SendToUser. For a PR's diff instead, `gh pr diff <url>` on a computer where `gh` is signed in.

## Merge
First show the PR and ask:
```sh
f(){ <aoapi helper>; aoapi GET "sessions/<session id>/pr" | python3 -c '
import json,sys
for p in json.load(sys.stdin).get("prs") or []:
    if str(p.get("number"))==sys.argv[1]: print(json.dumps({"url":p.get("url"),"headSha":p.get("headSha"),"state":p.get("state"),
        "ci":(p.get("ci") or {}).get("state"),"review":(p.get("review") or {}).get("decision"),"mergeability":(p.get("mergeability") or {}).get("state")}))' '<pr number>'; }; f
```
Tell the user the link, checks (`ci`), review decision and whether it can merge (`mergeability`). Never merge on failing checks or `conflicting` without the user saying so for that PR. On yes, merge exactly the commit you showed:
```sh
f(){ <aoapi helper>; aoapi POST "prs/<pr number>/merge" "$(python3 -c 'import json,sys; print(json.dumps({"prUrl":sys.argv[1],"expectedHeadSha":sys.argv[2]}))' '<url>' '<headSha>')"; }; f
```
If the PR got new commits since you showed it, Orchestrator refuses the merge. Show it again and ask again.

## Screenshot
The session must have a browser page open. `BROWSER_CAPABILITY_INVALID` means it has none: say so.
```sh
f(){ mkdir -p ~/.ao-grok/out && AO_SESSION_ID='<id>' ao browser screenshot ~/.ao-grok/out/<id>-$(date +%H%M%S).png; }; f
```
Then CopyToBox and SendToUser.

## Files
A session's deliverables are in its artifact folder:
```sh
f(){ ls -la "${AO_DATA_DIR:-$HOME/.ao/data}/artifacts/<id>/"; }; f
```
Send the file the user asks for with CopyToBox and SendToUser. HTML reports render best opened on the phone as a file.

## Keep awake
For a run that must last overnight, after a yes:
```sh
f(){ <aoapi helper>; aoapi POST awake/sessions "$(python3 -c 'import json,sys; print(json.dumps({"mode":"duration","durationSeconds":int(sys.argv[1]),"scope":"ao"}))' '<seconds>')"; }; f
```
Say when it ends. The user can stop it in Orchestrator. An `AWAKE` error that mentions a safety stop means Orchestrator stopped keep-awake on purpose (for example on battery): tell the user to resume it in Orchestrator. Don't resume it yourself.

## Automations
For recurring agent work that should run on the host even when the bot is off (for example "every night, update dependencies in <project>"), create an Orchestrator automation after a yes:
```sh
ao automation create --project '<project>' --name '<name>' --cron '<m h dom mon dow>' --timezone '<IANA zone>' --prompt '<task>'
```
`ao automation list` shows them. To remove one after a yes: `ao automation delete --yes <id>`. Use a Grok routine instead when the job is to tell the user something (ao-status "Digest", ao-answer waiting check).
