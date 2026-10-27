# Orchestrator Bot for Grok Bot

Seven skills that let a Grok Bot run Orchestrator from chat: see what every session is doing, answer agents' questions, talk to each project's orchestrator, act on sessions and PRs, and update itself. There is nothing to install on the user's computers. The skills use the `ao` CLI and AO's local API, which every AO install already has. macOS and Linux only; Windows isn't supported yet.

| Skill | What it does |
|---|---|
| `ao-rules` | Shared rules: hosts, the `aoapi` helper, orchestrator-first routing, who decides, secrets |
| `ao-setup` | Finds AO on your computers, adds a remote host, turns on routines |
| `ao-status` | What's running, waiting, ready to merge; the daily digest |
| `ao-orchestrator` | Sends your request to a project's orchestrator and relays its reply |
| `ao-answer` | Shows waiting questions and approvals as cards and sends your pick back; away mode |
| `ao-actions` | Stop, resume, merge, review, screenshot, files, keep awake, automations |
| `ao-update` | Checks the published skills, verifies their sha256 and rewrites the changed ones |

## Computer layouts

| Layout | How the bot reaches AO |
|---|---|
| Grok Bot desktop app on every computer that runs AO | Each computer is a **local** host. The bot runs commands on each one and labels results by host |
| Grok Bot desktop app only on your laptop; AO runs on another Mac (the host) | The host is a **remote** host. The bot calls the host's AO API over your network from the laptop, with the host's AO password |
| Both: your laptop runs AO too | The laptop is a local host and also the bridge to the remote host |

Each host's AO keeps its own projects; a repo cloned on two Macs is two separate projects. AO names a project after its folder, so both can get the same id, and session ids such as `agent-orchestrator-1-12` can then exist on both. The bot names every session `<host>/<session id>`.

A remote host needs AO's network access turned on (AO remote access settings, or `ao remote-host enable` on the host). Its password is typed once into a Terminal window on the laptop and kept in `~/.ao-grok/<host>.header` (mode 600). It never passes through chat.

## Install

**Grok Bot:** open the [Orchestrator Bot template](https://x.ai/bot/1WTqU5Ql7Ysftsg4KpWjW) and choose **Add to Grok Bot**. Install the Grok Bot desktop app on the computers that run AO, then say "set up AO". Say "update Orchestrator Bot" later to get skill updates.

To build the bot yourself instead, create a bot and save each `skills/<id>/SKILL.md` as a skill (ask the bot to use UpdateSkill with the frontmatter name and description and the rest of the file as the body).

**Claude Code, Codex, Cursor:** copy the folders under `skills/` into the agent's skills folder (`~/.claude/skills`, `~/.agents/skills`, `~/.cursor/skills`). These agents run on one computer, so they see that computer's AO as a local host only.

## Publishing and updates

The skills are meant to live in the AO repository at `integrations/grok-bot/`. After changing any skill, run `python3 make-manifest.py <version>` to rewrite `manifest.json` (version plus sha256 per skill). Installed bots run `ao-update`: it reads the newest commit on `main`, downloads the manifest and the changed skills from that exact commit, checks every sha256, and rewrites the skills only if all match.

## Tested

Run on 2026-10-27 against a live AO daemon, in zsh and bash, with throwaway sessions in demo projects (all removed afterwards):

| Flow | Result |
|---|---|
| Question from a worker (AskUserQuestion), answered by option and by typed "Other" | Passed: the worker received both answers |
| Approval (Claude Code in `accept-edits`), answered Allow Once; answering twice | Passed; the second answer returns `409 CHAT_REQUEST_NOT_PENDING` |
| Plain-text question detection | Passed; it also found 3 real unanswered questions |
| Orchestrator: find, start with the request as its prompt, send, wait, steer | Passed (`ROUNDTRIP OK`, `START OK`, `STEER OK`); a real catch-up sent by Grok Bot was matched too |
| Send to a broken orchestrator | Passed: AO's error is shown, one resume and retry, no false "sent" |
| Interrupt, kill, restore (worker), resume | Passed |
| Merge | PR details read; request body accepted by AO's validation (fake PR). No real PR merged |
| Review trigger, screenshot, files | Passed (errors explained where there's no PR or browser page) |
| Automation create and delete | Passed (created disabled, then deleted) |
| Keep awake | Refused by AO's safety stop on this Mac; the skill explains it. Not forced |
| Remote host through the real password-checked LAN listener | Passed: finder, news, connection test; blocked routes return `ROUTE_NOT_FOUND` |
| Setup probe and password window (with a piped test password) | Passed: header file mode 600, window script deletes itself |
| `ao-update`: first install, no change, one change, tampered file, GitHub before publishing | Passed; tampering gives `HASH_MISMATCH` and writes nothing |

Not tested here: Grok Bot's own side (cards, routines, UpdateSkill) beyond what the user ran by hand, Linux, and a remote host on a second physical computer.

## AO bugs found while testing

| Bug | Effect |
|---|---|
| An orchestrator can't be created when an old folder is left at its worktree path that git no longer tracks (`todo-app` here) | `provisionState: failed`; every message to it fails. The skill reports it |
| `POST /sessions/<id>/send` returns a bare 500 when the agent controller is stopped | `steer-or-send` returns a clear `CHAT_CONTROLLER_NOT_READY` for the same case |
| `ao pr merge <n>` posts an empty body, but the route requires `prUrl` and `expectedHeadSha` | The CLI merge fails with `INVALID_PR`. The skill sends the full body |

## What AO could add to make this better

| Gap today | Effect | Fix in AO |
|---|---|---|
| No CLI command to list or answer pending requests | Skills read the conversation API with Python | `ao session pending`, `ao session answer` |
| The orchestrator isn't told when a worker waits on an approval | The bot, not the orchestrator, triages approvals | Send worker waits to the orchestrator as reports |
| AO can't push events to the bot | Questions reach chat on the 30-minute check, not instantly (AO's mobile app is instant) | A webhook sender in the daemon, with finished and failed events |
| `ao send` doesn't wait for a reply | The orchestrator skill polls history | `ao send --wait` |
| The `ao` CLI talks only to its own computer | Remote hosts need the REST helper | `ao --host <url>` with a stored credential |
| No risk level on approvals | The bot judges risk from the command text | A risk hint per approval |
| Review trigger, screenshots, file listing and keep-awake are local only | A remote host needs its orchestrator for these | REST routes reachable over remote access |
