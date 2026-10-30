---
name: ao-orchestrator
description: >-
  Talk to a project's AO orchestrator: pass the user's request to it, wait for
  its reply and relay it. The default route for starting or changing work,
  "catch me up", "why", "what next" and plans. Use for "in <project>: <ask>",
  "tell the orchestrator ...", "catch me up on <project>", "start work on ...".
---
Follow the ao-rules skill ("Who does what: orchestrator first"). Sending a message is a write: do it only for what the user asked in this turn.

## 1. Pick the host and project
Match the user's words against project ids on every host (`aoapi GET projects`). One match: use it. The same project id on two hosts (two separate projects that share a folder name): ask which host, unless the user named one or memory has a default for that project. No match: list the projects and ask.

## 2. Find the orchestrator
```sh
f(){ <aoapi helper>; aoapi GET "sessions?orchestratorOnly=true&active=true&project=<project>" | python3 -c '
import json,sys
for s in json.load(sys.stdin)["sessions"]: print(s["id"], (s.get("activity") or {}).get("state",""), s.get("provisionState",""))'; }; f
```
- One line: that is the orchestrator. Its state tells you if it is busy (`active`).
- `exited`: resume it first with `aoapi POST "sessions/<id>/resume-agent" '{}'`, then go on.
- Provision state `failed`: AO could not create its workspace, and messages to it fail. Read the reason with `aoapi GET "sessions/<id>"` (field `provisionError`), tell the user in plain words, and ask them to fix it in AO. Stop here.
- No line: ask "There's no orchestrator in <project> on <host>. Start one with your request?" On yes, start it with the request as its first message (step 3's file and tag), then go to step 4. Its reply is its first turn:

### Start an orchestrator
```sh
f(){ <aoapi helper>; P='<project>'; TAG="[via Grok Bot #$(date +%H%M%S)]"; F=~/.ao-grok/out/$(date +%F)-<short name>.md
  mkdir -p ~/.ao-grok/out; { printf '%s ' "$TAG"; cat <<'EOF'
<message>
EOF
  } > "$F"
  H=$(aoapi GET "projects/$P" | python3 -c 'import json,sys; d=json.load(sys.stdin); c=(d.get("project",d).get("config") or {}); print((c.get("orchestrator") or {}).get("agent") or (c.get("worker") or {}).get("agent") or "")')
  aoapi POST sessions "$(python3 -c 'import json,sys; b={"projectId":sys.argv[1],"kind":"orchestrator","mode":"chat","displayName":"Orchestrator","prompt":open(sys.argv[2]).read()}; b.update({"harness":sys.argv[3]} if sys.argv[3] else {}); print(json.dumps(b))' "$P" "$F" "$H")" | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("session",d); print("started", d["id"])' && echo "TAG=$TAG"; }; f
```
After about 10 seconds, check `provisionState` with `aoapi GET "sessions/<new id>"`. If it is `failed`, handle it as above.

## 3. Send
Write the message to a file on `via`, then send it. The message is the user's request in their words, plus only the context from this chat the orchestrator needs. Don't add your own design choices. Start it with a tag so you can find the reply:
```sh
f(){ <aoapi helper>; O='<orchestrator id>'; TAG="[via Grok Bot #$(date +%H%M%S)]"; F=~/.ao-grok/out/$(date +%F)-<short name>.md
  mkdir -p ~/.ao-grok/out; { printf '%s ' "$TAG"; cat <<'EOF'
<message>
EOF
  } > "$F"
  B=$(python3 -c 'import json,sys; print(json.dumps({"message":open(sys.argv[1]).read(),"userAuthored":True}))' "$F")
  if aoapi POST "sessions/$O/send" "$B" >/dev/null; then echo "sent TAG=$TAG F=$F"; return 0; fi
  echo "SEND_FAILED: resuming the agent and trying once more"
  aoapi POST "sessions/$O/resume-agent" '{}' >/dev/null; sleep 5
  aoapi POST "sessions/$O/send" "$B" >/dev/null && echo "sent TAG=$TAG F=$F"; }; f
```
A failed send prints AO's error. If it still fails after the retry, tell the user the orchestrator isn't accepting messages and to open it in AO.

If the orchestrator was `active` in step 2, the message waits and runs as its own turn after the current one. Tell the user right away, in one line: "The orchestrator is busy with <what its last turn is about>. Your question is queued and runs next." Then go on to step 4.

If the user wants to change what it is doing right now ("stop that", "use X instead"), steer instead. Write the file and tag as above, then:
```sh
f(){ <aoapi helper>; O='<orchestrator id>'; F='<message file>'
  aoapi POST "sessions/$O/conversation/steer-or-send" "$(python3 -c 'import json,sys,uuid; print(json.dumps({"text":open(sys.argv[1]).read(),"clientMessageId":str(uuid.uuid4())}))' "$F")"; }; f
```
`"outcome":"sent"` means it arrived (in the running turn, or as a new turn if the orchestrator was idle). Then go to step 4.

## 4. Wait for the reply
Check every 10 seconds for up to 5 minutes. Done when the orchestrator is no longer `active` and a turn that starts with your tag has a reply:
```sh
f(){ <aoapi helper>; O='<orchestrator id>'; TAG='<tag>'
  for i in $(seq 30); do sleep 10
    st=$(aoapi GET "sessions/$O" | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("session",d); print((d.get("activity") or {}).get("state",""))')
    [ "$st" = active ] && continue
    aoapi GET "sessions/$O/history?offset=0&limit=5" | python3 -c '
import json,sys
for t in json.load(sys.stdin).get("turns") or []:
    if (t.get("userRequest") or "").startswith(sys.argv[1]) and t.get("agentResponse"):
        print(t["agentResponse"]); raise SystemExit(0)
raise SystemExit(1)' "$TAG" && return 0
  done; echo STILL_WORKING; }; f
```
- A reply: relay it. Keep it faithful. Shorten only for a phone, and send a long reply as a file.
- `STILL_WORKING`: tell the user the orchestrator is still on it, and offer: "I'll message you when it replies." On yes, create a routine that runs step 4 once every 5 minutes for that tag, reports the reply, then deletes itself.
- The orchestrator asks the user something back: show it as a card if it gives clear options, and send the pick as a new message (step 3).

## 5. After
If the orchestrator started workers, list them in one line (ao-status "Sessions" for that project). Don't watch them unless the user asks. Their questions reach the user through ao-answer.
