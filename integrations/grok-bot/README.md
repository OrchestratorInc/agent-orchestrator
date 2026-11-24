# Orchestrator Bot for Grok Bot

Skills that let a Grok Bot run Orchestrator from chat: see what every session is doing, get questions, finished turns and failures the moment they happen, answer agents' questions, talk to each project's orchestrator, act on sessions and PRs, look after the computers, and update itself without losing the user's edits. There is nothing to install on the user's computers. The skills use the `ao` CLI and Orchestrator's local API, which every Orchestrator install already has. macOS and Linux only; Windows isn't supported yet.

| Skill | What it does |
|---|---|
| `ao-rules` | Shared rules: hosts, the `aoapi` helper, orchestrator-first routing, who decides, secrets |
| `ao-setup` | Finds Orchestrator on your computers, adds a remote host, turns on routines |
| `ao-status` | What's running, waiting, ready to merge; the daily digest |
| `ao-orchestrator` | Sends your request to a project's orchestrator and relays its reply |
| `ao-answer` | Shows waiting questions and approvals as cards and sends your pick back; away mode |
| `ao-actions` | Stop, resume, merge, review, screenshot, files, diff, rename, pin, switch model, retry, resume after a usage limit, search, keep awake, automations |
| `ao-live` | Sets up live events: the "AO events to chat" routine and Orchestrator's webhook on each host; per-project bots, event picks, quiet at desk |
| `ao-event` | Small, self-contained skill each live event reads when it needs more than a one-line report |
| `ao-watch` | Watches a session, PR, log or process; reports stalls and check changes |
| `ao-templates` | Saved prompts with `{vars}` for starting work |
| `ao-usage` | Tokens, estimated cost and plan limits, only when asked |
| `ao-report` | A full HTML briefing of one project |
| `ao-tracker` | A living tracker per project or launch |
| `ao-learnings` | Saves durable lessons into the project's AGENTS.md or CLAUDE.md |
| `ao-checkup` | Health of Orchestrator, its sessions and agents, and the computer |
| `ao-cleanup` | Disk audit: ended sessions' worktrees first, then the computer; deletes only picks |
| `ao-sync` | Copies the global dev setup from a main computer to the others |
| `ao-dev-update` | Updates coding agents and dev tools, with a check against running sessions and project pins |
| `ao-handoff` | Hands a session's work to a Cursor cloud agent |
| `ao-update` | Updates these skills: verifies sha256, keeps the user's edits (3-way merge), modes auto, notify or off |

## Computer layouts

| Layout | How the bot reaches Orchestrator |
|---|---|
| Grok Bot desktop app on every computer that runs Orchestrator | Each computer is a **local** host. The bot runs commands on each one and labels results by host |
| Grok Bot desktop app only on your laptop; Orchestrator runs on another Mac (the host) | The host is a **remote** host. The bot calls the host's Orchestrator API from the laptop, at the host's account address (`useao.dev`, any network) or its LAN address, with the host's Orchestrator password |
| Both: your laptop runs Orchestrator too | The laptop is a local host and also the bridge to the remote host |

Each host's Orchestrator keeps its own projects; a repo cloned on two Macs is two separate projects. Orchestrator names a project after its folder, so both can get the same id, and session ids such as `agent-orchestrator-1-12` can then exist on both. The bot names every session `<host>/<session id>`.

A remote host needs Orchestrator's network access turned on (Orchestrator remote access settings, or `ao remote-host enable` on the host). Its password is typed once into a Terminal window on the laptop and kept in `~/.ao-grok/<host>.header` (mode 600). It never passes through chat.

When a host's Grok Bot app is offline, the bot checks the host's address without a password (`/api/v1/identity`, the one route that needs none) and says whether Orchestrator there is still up, its tunnel is down (Cloudflare 530), or its address was released.

## Install

**Grok Bot:** open the [Orchestrator Bot template](https://x.ai/bot/1WTqU5Ql7Ysftsg4KpWjW) and choose **Add to Grok Bot**. Install the Grok Bot desktop app on the computers that run Orchestrator, then say "set up AO". No Orchestrator.inc yet? Setup offers to install it on a Mac and walks you through your first project. Setup checks for newer skills right away, so a bot made from an older template gets the current ones. Say "update Orchestrator Bot" later, or let the weekly update routine do it.

To build the bot yourself instead, create a bot and save each `skills/<id>/SKILL.md` as a skill (ask the bot to use UpdateSkill with the frontmatter name and description and the rest of the file as the body).

**Claude Code, Codex, Cursor:** copy the folders under `skills/` into the agent's skills folder (`~/.claude/skills`, `~/.agents/skills`, `~/.cursor/skills`). These agents run on one computer, so they see that computer's Orchestrator as a local host only.

## Live events

`ao webhook add main` on a host makes its Orchestrator daemon POST one JSON event per question, finished orchestrator turn, failed turn, agent exit and PR or review change to the bot's "AO events to chat" routine. The URL and key are pasted into a Terminal window on the host and kept in `~/.ao/data/webhooks.json` (mode 600); they never pass through chat, and the API only ever shows the URL's host. The routine's prompt reports plain events itself and reads the small `ao-event` skill only to act. More bots can each get their own projects and kinds (`ao webhook add <bot> --project <id> --events needs_input,failed`), and `--quiet-at-desk` skips events while the Orchestrator app is in front and in use. Bursts of finished turns, and turns that end while the webhook is down or rate limited, arrive as one `summary`. See `ao webhook --help` and the CLI docs.

## Publishing and updates

The skills are meant to live in the Orchestrator repository at `integrations/grok-bot/`. After changing any skill, run `python3 make-manifest.py <version>` to rewrite `manifest.json` (version plus sha256 per skill); a skill changed without it makes every update stop with `HASH_MISMATCH`. Installed bots run `ao-update`: it reads the newest commit on `main`, downloads the manifest and the skills from that exact commit, checks every sha256, and only then changes anything. It compares three copies of each skill (the one last installed, the live one, the new one), so a skill the user edited is kept, merged when the two changes touch different lines, or asked about. Bots updated before this kept no copies: the fetcher finds the installed version's copies in the repository history, or, with no version on record, recognises any published copy as unedited.

## Tested

Run on 2026-11-14:

| Flow | Result |
|---|---|
| `ao-update` with live skills saved in a different YAML style: unedited, edited where the release didn't change, edited on other lines, edited on the same lines, new skills | `changed`, `yours`, `both` that merges cleanly, `both` that doesn't, `new`; after apply and `done`, a second check is clean |
| `ao-update` for a bot made from the template (nothing on record) and for a box without the live skills folder | Unedited copies recognised; falls back to manifest hashes |
| `ao-update` against the real GitHub history | Finds the installed version's commit and its copies |
| Report export, checkup, search and diff snippets | Run read-only against a live daemon |
| Live events from a lab daemon with a real Claude agent: test event, auth header, restart, finished orchestrator turn, question then `input_resolved`, usage-limit `failed` (through a proxy that returns Claude's 5-hour limit), auto-resume, retry, `worker_finished` filter | Each event arrived once with the expected fields; `resetsAt` matched Claude's own reset time |
| Host check against five real saved hosts | Online host: 200 and its `hostId`; stopped tunnel: 530 `error code: 1033`; released address: `Could not resolve host`; gone LAN host: connection refused. The setup probe prints the account address and nothing else from `mobile/status` |

Run on 2026-10-27 against a live Orchestrator daemon, in zsh and bash, with throwaway sessions in demo projects (all removed afterwards):

| Flow | Result |
|---|---|
| Question from a worker (AskUserQuestion), answered by option and by typed "Other" | Passed: the worker received both answers |
| Approval (Claude Code in `accept-edits`), answered Allow Once; answering twice | Passed; the second answer returns `409 CHAT_REQUEST_NOT_PENDING` |
| Plain-text question detection | Passed; it also found 3 real unanswered questions |
| Orchestrator: find, start with the request as its prompt, send, wait, steer | Passed (`ROUNDTRIP OK`, `START OK`, `STEER OK`); a real catch-up sent by Grok Bot was matched too |
| Send to a broken orchestrator | Passed: Orchestrator's error is shown, one resume and retry, no false "sent" |
| Interrupt, kill, restore (worker), resume | Passed |
| Merge | PR details read; request body accepted by Orchestrator's validation (fake PR). No real PR merged |
| Review trigger, screenshot, files | Passed (errors explained where there's no PR or browser page) |
| Automation create and delete | Passed (created disabled, then deleted) |
| Keep awake | Refused by Orchestrator's safety stop on this Mac; the skill explains it. Not forced |
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

| Gap today | Effect | Fix in Orchestrator |
|---|---|---|
| No CLI command to list or answer pending requests | Skills read the conversation API with Python | `ao session pending`, `ao session answer` |
| The orchestrator isn't told when a worker waits on an approval | The bot, not the orchestrator, triages approvals | Send worker waits to the orchestrator as reports |
| `ao send` doesn't wait for a reply | The orchestrator skill polls history | `ao send --wait` |
| The `ao` CLI talks only to its own computer | Remote hosts need the REST helper | `ao --host <url>` with a stored credential |
| No risk level on approvals | The bot judges risk from the command text | A risk hint per approval |
| Review trigger, screenshots, file listing and keep-awake are local only | A remote host needs its orchestrator for these | REST routes reachable over remote access |
