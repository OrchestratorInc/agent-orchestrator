---
name: ao-answer
description: >-
  Find AO sessions that wait on the user, show each question or approval as
  tap-to-answer options, and send the answer back to that session, following
  ao-rules "Decisions" (default, away mode, standing approvals). Use for
  "what's waiting on me", "answer <session>", "approve it", the AO waiting
  check routine, and away mode.
---
Follow the ao-rules skill. Its "Decisions" section says who may answer. Answers go straight to the worker, not through the orchestrator: the user's pick must arrive exactly as given.

## 1. Find what's waiting
On every host, in parallel:
```sh
f(){ <aoapi helper>
  aoapi GET 'sessions?active=true' | python3 -c '
import json,sys,time,calendar
now=time.time()
for s in json.load(sys.stdin)["sessions"]:
    st=(s.get("activity") or {}).get("state"); t=(s.get("lastEventAt") or "")[:19]
    if st in ("waiting_input","blocked"): print("W", s["id"])
    elif st=="idle" and t and now-calendar.timegm(time.strptime(t,"%Y-%m-%dT%H:%M:%S"))<86400: print("I", s["id"], s.get("lastEventAt"))' |
  while read -r k id at; do
    if [ "$k" = W ]; then
      aoapi GET "sessions/$id/conversation" | python3 -c '
import json,sys
d=json.load(sys.stdin); found=False
for a in d.get("activities") or []:
    if a.get("activityKind") in ("approval","user_input") and a.get("status")=="pending":
        found=True; print(json.dumps({"session":sys.argv[1],"kind":a["activityKind"],"requestId":a.get("requestId"),
            "summary":a.get("summary"),"decisions":a.get("decisions") or (a.get("detail") or {}).get("decisions"),"detail":a.get("detail")}))
if not found: print(json.dumps({"session":sys.argv[1],"kind":"reply"}))' "$id" 2>/dev/null
    else
      aoapi GET "sessions/$id/history?offset=0&limit=1" | python3 -c '
import json,sys,re
t=(json.load(sys.stdin).get("turns") or [{}])[0]; r=(t.get("agentResponse") or "").strip()
if re.search(r"\?[\s*_`)\]]*$", r): print(json.dumps({"session":sys.argv[1],"kind":"reply","lastEventAt":sys.argv[2],"asked":r[-300:]}))' "$id" "$at" 2>/dev/null
    fi
  done; }; f
```
Each line is one thing waiting:

| `kind` | What it is |
|---|---|
| `approval` | Permission to run a command or change files. `summary` and `detail.input` say what; `decisions` are the options `[{id, kind, label}]` |
| `user_input` | A question. `detail.message` asks it; `detail.schema.properties` are the fields, with choices in `enum` or `oneOf` |
| `reply` | The agent asked in plain text: a session that waits with no request (or a terminal session), or an idle session from the last 24 hours whose last reply ends with a question (`asked` holds its end). Read its last reply (ao-status "One session in detail") |

In the **AO waiting check** routine: keep what you already showed in `~/.ao-grok/shown.txt` on `via`, one line per item (`<host> <session> <requestId>`, or `<host> <session> reply <lastEventAt>` for a `reply`), and message only about new items. Send nothing when nothing new is waiting.

## 2. Show it
Per session, one short briefing first: `<host>/<session>` and its name, what it is doing, and what it asks, in one or two sentences. Don't recommend anything. Then:
- `approval`: the command or change verbatim, the risk you judge (ao-rules), and the `decisions` labels exactly as options in one card. Mark a decision whose `kind` is `allow_always` as "changes this agent's permissions for good".
- `user_input`: one card with every question in it, in order (the card pages through them). A field with `oneOf` or `enum` is a question: use the `oneOf` titles or `enum` values as its options, exactly. A field whose `_meta._askUserQuestionCustomAnswer.questionId` names another field is that question's "Other" box: show it as a typed-answer option on that question, not as its own question. Any other free-text field is a question with a typed answer. `detail.inputMode` `url` can't be answered from chat: tell the user to open AO.
- `reply`: the agent's question from its last reply, with options if it gave clear ones, else ask for a typed answer.

Several sessions at once: explain them together, then cards per session.

## 3. Answer
On the owner host, from its `via` computer:
- **Approval**:
  `aoapi POST "sessions/<id>/conversation/approvals/<requestId>/resolve" "$(python3 -c 'import json,sys; print(json.dumps({"decisionId":sys.argv[1]}))' '<decision id>')"`
- **Question**: `content` maps each field name to the pick. For a picked option use its `oneOf` `const` (or the `enum` value), not the title. For a typed "Other" answer, put the text under the "Other" field (for example `question_0_custom`) and leave its question out:
  `aoapi POST "sessions/<id>/conversation/inputs/<requestId>/resolve" "$(python3 -c 'import json,sys; print(json.dumps({"action":"accept","content":json.loads(sys.argv[1])}))' '{"<field>":"<pick>"}')"`
  The user says no or skip: `{"action":"decline"}`.
- **reply**: send the answer as a message (ao-rules "Messages to agents"):
  `aoapi POST "sessions/<id>/send" "$(python3 -c 'import json,sys; print(json.dumps({"message":"[via Grok Bot] "+sys.argv[1],"userAuthored":True}))' '<answer>')"`

Confirm in one line. An error that says the request is not pending or not found means it was already answered or closed: say so. Then run step 1 for that session again, and deal with anything still open.

## Away mode
When memory says away mode is on and its end time hasn't passed, answer as ao-rules "Decisions" allows instead of showing cards, and log each one. Tell the user afterwards in one line per answer: session, what it asked, what you picked, why. Anything you may not decide: show it as in step 2, and say away mode left it for them.
