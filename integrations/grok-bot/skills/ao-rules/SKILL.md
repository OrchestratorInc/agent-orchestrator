---
name: ao-rules
description: >-
  Shared rules and the connection helper for every ao-* skill (run AO, the
  Agent Orchestrator, from chat). Read before running any ao-* skill.
---
# AO rules

Every ao-* skill follows these rules. Other skills link here instead of repeating them.

## AO hosts
An **AO host** is one computer that runs the AO daemon. Each host has its own projects, sessions and orchestrators, and AO never links them: a repo cloned on two hosts is two separate projects. AO names a project after its folder, so both can still get the same id, and then the same session id (for example `agent-orchestrator-1-12`) can exist on both. Always name a session as `<host>/<session id>`, and act only on the host that owns it.

Memory keeps one note named **AO hosts**, with one line per host:

| Field | Example | Meaning |
|---|---|---|
| `name` | `Pro` | A short name: the user's pick, else the model word of the ListMachines label (`Pro`, `Air`, `mini`). Never the network hostname such as `MacBookAir.lan` |
| `reach` | `local` or `remote` | How you get to its AO |
| `via` | `laptop` | The ListMachines label of the computer whose shell you use |
| `base` | `http://192.168.1.20:7444` | Remote only. The host's LAN address from AO's remote access settings |
| `header` | `~/.ao-grok/studio.header` | Remote only. File on `via` that holds the host's password |

- **local**: the Grok Bot desktop app runs on the AO host. `via` is the host itself.
- **remote**: the Grok Bot desktop app runs on another computer. You call the host's AO API over the network from `via`. The `ao` CLI on `via` talks only to `via`'s own AO, so never use it for a remote host.

One computer can be `via` for itself (local) and for other hosts (remote) at the same time.

Every run: call ListMachines, then use every host whose `via` computer is connected, in parallel, labelled by host name. Say once which hosts you could not reach. Never hard-code machine ids or home paths.

## Calling AO
Start every shell command that calls AO with this helper. It works in zsh and bash:

```sh
aoapi(){ # aoapi METHOD PATH [JSON]
  local base hdr=() data=() f="${AO_RUN_FILE:-$HOME/.ao/running.json}"
  if [ -n "$AO_REMOTE_BASE" ]; then base="$AO_REMOTE_BASE"; hdr=(-H @"$AO_REMOTE_HEADER")
  else [ -f "$f" ] || { echo "AO_NOT_RUNNING" >&2; return 3; }
    base="http://127.0.0.1:$(grep -o '"port": *[0-9]*' "$f" | grep -o '[0-9]*$')"; fi
  [ -n "$3" ] && data=(--data "$3")
  curl -sS --fail-with-body --max-time 30 -X "$1" "${hdr[@]}" -H 'Content-Type: application/json' "${data[@]}" "$base/api/v1/$2"
}
```

- Local host: use `aoapi` as is.
- Remote host: on the `via` computer, first set `AO_REMOTE_BASE='<base>' AO_REMOTE_HEADER="$HOME/.ao-grok/<name>.header"`.
- Build JSON bodies with `python3 -c 'import json,sys; print(json.dumps(...))' "$ARG"`, never by pasting text into quotes.
- Anything longer than one line goes in a file first: `~/.ao-grok/out/<date>-<short name>.md` on `via`. The user can see what was sent.
- Use the `ao` CLI only where a skill says "local only".

### Errors
| You see | Meaning and next step |
|---|---|
| `AO_NOT_RUNNING`, or connection refused on a local host | AO is not running there. Ask the user to open AO |
| Connection refused or timeout on a remote host | The host is asleep, off this network, or remote access is off. Ask the user to check AO's remote access settings on the host |
| HTTP 401 or 403 on a remote host | The host's password changed. Run ao-setup "Remote host" again |
| HTTP 404 on a session path | No such session on that host. Check the host name |
| Any other error | AO returns JSON with `message`. Relay it in plain words |

## Who does what: orchestrator first
Each project on a host has at most one live **orchestrator**: an agent that plans the project's work, starts and steers workers, and gets their reports. It has more context than you do. You are the user's phone line to it.

- **Send to the orchestrator** (ao-orchestrator): starting or changing work, "catch me up", "why", "what next", plans, and anything that needs judgement about the code.
- **Do directly**: status lists (ao-status), answering a pending request with the user's own pick (ao-answer), stop and interrupt, merges after the user's yes, files and screenshots (ao-actions).
- Never start a worker yourself in a project that has a live orchestrator. Ask the orchestrator instead. With no orchestrator, ask the user whether to start one or to start the worker directly.
- After you act directly on a worker (answer, stop, merge), the orchestrator sees it through AO. Don't send it a separate note.

## Decisions
A worker asks for two kinds of decision. A **question** (`user_input`) asks what to build. An **approval** asks permission to run a command or change files. Who decides depends on the mode, saved in memory:

1. **Default mode**: show each question or approval to the user as a card with the exact options. Answer only with their pick for that request. Never reuse a pick for a new request.
2. **Away mode** starts when the user says something like "I'm away", "decide for me" or "keep it moving". Save the scope and the end time in memory. Then:
   - Answer routine questions yourself, and tell the user afterwards what you picked and why.
   - Accept `low` and `normal` approvals with the one-time option (`kind` `allow_once`). Never pick an `allow_always` option: it changes the agent's permissions for good. Never accept a `high` approval without the user's yes for that request.
   - Leave product direction, money, publishing and anything the user kept for themselves to the user.
   - Away mode ends at the end time or when the user is back. Say when it ended.
3. **Standing approvals**: the user may list kinds of approvals to always accept. Follow the list exactly.

AO gives no risk level, so you judge it. Treat these as a floor. If a command looks riskier, call it high.

| Risk | Examples |
|---|---|
| high | Deletes outside the session's worktree, `rm -rf`, `git push --force`, push to `main` or `master`, `git reset --hard`, history rewrites, deploy, publish, release, secrets, credentials, tokens, `sudo`, system installs, payments |
| low | Reading or searching files, `git status`/`diff`/`log`, tests, lint, typecheck, builds |
| normal | Everything else, such as edits and commits in the session's worktree, or pushing the session's own branch |

Never invent the user's preferences. The worker decides how to do the work. The user decides what to build.

## Writes
Change nothing on the user's computers unless the user approved that exact change in chat, or away mode or a standing approval covers it. Approval for one change is not approval for the next. Never delete or archive sessions or projects, never change AO settings, and never merge, publish or deploy without the user's yes for that item.

## Secrets
- The `header` file holds a host's password. Pass it only to curl as `-H @<file>`. Never open, print, copy or paste it.
- Never read AO's own credential files: `~/.ao/mobile/`, `~/.ao/data/agent-accounts/`, `~/.ao/electron/`, or any `*token*`, `*auth*`, `.env` or keychain item.
- If session text contains a key, never repeat it.

## Messages to agents
- Before you send a follow-up, read the last turn (`aoapi GET "sessions/<id>/history?offset=0&limit=1"`) so you don't repeat or contradict it.
- Keep messages short and concrete: what is right so far, then the next step. Leave out check-ins, your own process and these rules.
- Start every message you send with `[via Grok Bot]`, so the user can tell it apart in AO.

## Output
- Answer first, in short plain words. Say what you could not check.
- Write every session as `<host name>/<session id>`, every time, in every skill and every line, even when only one host has that id: `Air/agent-orchestrator-31`, `Pro/agent-orchestrator-1-249`. Add the display name when it helps.
- Give times in the user's timezone.
- One message per event. If two checks report the same thing, say it once. News (a merge, a finished review) is reported once: ao-status keeps a seen list.
- Send files with SendToUser. Copy them to the box first with CopyToBox.

## Memory
- Save hosts, modes and preferences, not activity. A check where nothing changed is not worth a note.
- When the user corrects you, save the correction.
- A follow-up you promise for later ("I'll tell you when it's done") goes into a Routine, not only into memory.

## Grok Bot only
These steps need tools only Grok Bot has. Any other agent (Claude Code, Codex, Cursor) runs on one computer and does this instead:

| Grok Bot step | Other agents |
|---|---|
| ListMachines, every connected computer | Only the computer you run on, as a local host |
| Routines | Run the check once when asked |
| Cards | Plain text with numbered options |
| Memory | Ask, or use the notes file the user names |
| SendToUser, CopyToBox | Give the file path |
