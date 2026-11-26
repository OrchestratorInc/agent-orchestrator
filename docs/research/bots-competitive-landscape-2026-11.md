# Bots: competitive landscape and AO implementation plan

Research snapshot: 2026-11-26. Sources are vendor docs and changelogs, plus press where the vendor had no page. Claims that only third parties make are marked as such.

## Verdict

"Bots" are long-lived orchestrators that you reach from where work happens and that act on their own initiative. Every product converges on five parts:

1. **Persistent identity.** A name, a purpose, and memory kept in editable files.
2. **Ingress.** You @-mention it in Slack, Linear, or GitHub, or it reacts to events (CI failed, PR opened, webhook) and schedules.
3. **Delegation.** It splits the goal and starts worker sessions in worktrees or VMs. Their PRs report back to it.
4. **A "Needs you" inbox.** It surfaces only decisions, reviews, and blockers, usually on mobile too.
5. **A permission model.** Who can ask it, whose credentials it uses, and what needs approval.

A demo is easy. Getting it right is hard, and the hard parts are the trust model and the quality of the inbox, not the agent itself.

The market has split in two. Vendors that sell cloud sandboxes shipped cloud-only bots. Open-source desktop tools shipped local-first bots. **No product found offers a true hybrid:** one bot whose home can be a laptop, a self-hosted remote host, or the vendor's cloud, and which can move between them. AO already has all three substrates behind one control plane, so this gap is our clearest differentiator.

## The wave, in order

| Date | Product | Kind |
| --- | --- | --- |
| 2026-02-10 | Warp Oz | Cloud agents with a Slack bot |
| 2026-03-05 | Cursor Automations (expanded 2026-06-18) | Cloud, triggered |
| 2026-04-14 | Claude Code routines | Cloud, triggered |
| 2026-09-29 | OpenAI dots | General-purpose always-on agent, cloud VM |
| 2026-10 | Capy Captains | Coding orchestrator, cloud by default |
| 2026-10-21 | Claude Duet (research preview) | General-purpose always-on agent, cloud VM |
| 2026-11-17 | Devin Bots | Coding bot managing teams of Devins, cloud |
| 2026-11-17 | Kiro Crew (PR #38240 merged that day; launch date not stated) | Self-hosted crew with memory |
| ~2026-11-19/20 | Orca Bots beta (v1.4.265) | Coding bot, local-first with cloud relay |

The coding bots arrived within weeks of OpenAI's and Anthropic's general always-on agents, and press frames them as a response to those.

## Where bots run: cloud vs local

| Product | Where it runs | Persistent bot with memory? | Triggers |
| --- | --- | --- | --- |
| Devin Bots | Cloud only. Each child session gets its own VM | Yes | Slack, Git provider events, webhooks, schedules, iMessage, web |
| Capy Captains | Cloud by default; a paired device is optional | Yes (files in a Drive) | Chat, Slack `@Capy captain=`, automations, API |
| Cursor Automations | Cloud only ("spins up a cloud sandbox"). Can be created from a local session | Memory yes, no named identity | Schedules, Slack (including emoji), Linear, GitHub, PagerDuty, webhooks |
| Claude Code routines | Cloud only (Anthropic infrastructure) | No. One-shot runs | Schedules, API, GitHub events. Slack is output only |
| Warp Oz | Cloud; docs say it can also run on your own infrastructure | No | Slack, GitHub, schedules, system events |
| Conductor | Slack feature is cloud-only. It is an MCP connection, not a bot | No | None; a user prompts an agent |
| **Orca Bots** | **Local-first**: your machine or a paired server. Orca Cloud only relays ingress | Yes (`SOUL.md`, `MEMORY.md`) | Chat, Slack, Linear, GitHub, Gmail/Calendar, webhooks, schedules |
| Paradev (formerly Superset) Automations | Local ("the Mac you picked"). Cloud-workspace support unconfirmed | No. Each run is a new workspace | GitHub, Slack, Linear, Sentry, Notion, Teams, webhooks, schedules |
| Kiro Crew | Self-hosted: Mac, local container, or a remote machine you control | Yes (Crew Members with private memory) | Slack, Discord, other messaging apps, authenticated webhooks, schedules, heartbeats |
| T3 Code | Local server; no bots | No | Scheduled tasks only |

Persistent identity plus memory is still rare. Only Orca, Kiro Crew, Devin, and Capy have it. The rest are triggered one-shot runs.

Both camps hit the same walls:

- **Local-first bots stop when the laptop sleeps.** Orca: "The computer has to be on," and queued requests expire after about a day. Paradev: a missed run "is missed and isn't retried."
- **Cloud-only bots take code and credentials off your machine.** They bill sandbox minutes on top of model usage, and they can't use your local tooling.

## Competitor teardowns

### Orca Bots (orca.dev, MIT)

The most important competitor. Sources: [orca.dev/bots](https://orca.dev/bots), [docs/bots](https://orca.dev/docs/bots), [connectors](https://orca.dev/docs/bots/connectors), [security](https://orca.dev/docs/bots/security), [use cases](https://orca.dev/docs/bots/use-cases).

- **Identity.** A bot "has a name, a purpose, a memory and automations, and lives in a folder on one computer." Soul and memory are plain Markdown files you can edit. Chat history lives in Orca's local storage, outside the folder. A creation wizard asks for name, picture, job description, computer, and agent.
- **Harness.** The bot uses your own Claude Code, Codex, or another supported agent, on your sign-in. Its usage counts against that plan ("No new model bill, no markup").
- **Execution.** The bot runs on your machine or a paired Orca server. It starts workers "each in its own Git worktree on your machine," and their PRs come back to the bot. PRs open under your git login. A bot can propose another bot, but nothing is created until you click.
- **Ingress.**
  - Slack: each bot gets its own Slack app, created through a one-time App Configuration Token stored encrypted in Orca Cloud.
  - GitHub: an Orca GitHub App. Triggers include issues, PRs, pushes, comments, reviews, CI pass/fail, merge conflicts, merges, deploys, and security alerts. Your own actions don't trigger the bot, except a PR-opened rule.
  - Linear: an Orca Linear app. Triggers are mentions, a `bot:<handle>` label, assignment, new issues, and moved issues.
  - Google: connects directly from your computer, polled about once a minute.
  - Webhooks: go through Orca Cloud. Bodies are kept for 7 days. A signing secret is optional.
  - Schedules: hourly, daily, weekly, cron, or RRULE. They only run while the computer is awake.
  - Transport: "Your computer connects out to Orca Cloud and picks up new requests; nothing has to reach in."
- **Reporting.** Replies go to the thread, PR, or issue the request came from. Mobile shows a "Needs you" list with one-tap approvals. The bot's Home view has Needs you, In progress, Planned, Closed, and Automations.
- **Memory.** After a day with new chats, the bot tidies its memory after 3 AM; this is configurable. The tidy-up has no tools and can't run commands. Changes are reviewable, and Undo is available. A separate read-only "Look around" scan of Slack, Linear, GitHub, and projects runs every 4 hours on weekdays and writes suggestions. You can import bots from OpenClaw or Hermes.
- **Security.**
  - "Who can ask" is enforced in Orca Cloud before a request reaches your machine. Teammates' requests "use your access, not their own."
  - Permission modes: Ask for approval, Accept edits, Approve for me, Full access. **The default is Full access.** Some agents have no approval modes, so their bots always run in Full access.
  - Most guardrails are only prompt rules: merge only when asked, treat outside content as information. Orca says "None of these settings is a guarantee" and "A model can still be fooled."
  - Secrets go in the OS keychain. Remote MCP headers are injected by Orca, so the bot never holds them.
- **24 templates.** Bug investigation, PR review, CI failures, meeting prep, daily standup, release notes, docs upkeep, deploy watch, bug report intake, issue triage, support triage, feedback digest, backlog cleanup, dependency updates, security check, release readiness, incident notes, on-call handoff, replies you owe, email digest, project briefing, spec drafting, unreviewed agent work, workspace cleanup. Template schedules start turned off.
- **Weaknesses.** The computer must be on; closing a Mac lid sleeps it. Guardrails are soft. There are no cost controls. Slack, GitHub, Linear, and webhooks require an Orca account.

Why it shipped fast: Orca already had worktrees, orchestration, mobile, and a cloud account. The bot is mostly packaging plus ingress.

### Devin Bots (Cognition)

Sources: [devin.ai/bots](https://devin.ai/bots), [docs](https://docs.devin.ai/work-with-devin/bots), [Fortune](https://fortune.com/2026/11/17/exclusive-cognition-bets-on-devin-bots-coding-agents-in-challenge-to-anthropics-duet-and-openais-dots/).

- A Bot "plans the work, starts a Devin session for each piece, follows up on those sessions, and comes back to you when something needs you." It has one chat for everything it owns, and each piece of work gets a child session on its own VM.
- Its scope is an outcome ("own checkout"), not a task. Lifecycle actions are Recycle (a fresh session), Archive, and Delete.
- Its "Needs you" list holds Decision, Review PR, Merge PR, Permission request, and Report/FYI items.
- **Credential model.** The bot "works with its own access, not the access of whoever messages it." Shared bots pool sign-ins, secrets, and memory: "Anyone you add can read everything the Bot has said and remembered so far." Sharing levels are Can use, Can configure, and Can share. Anyone who can message the bot can have it run commands on its Outposts without approval.
- **Billing.** Usage counts toward the plan like any session. Teams have an optional on-demand cap; Enterprise can set budgets. Admins get a page to set limits on every bot.
- **Claims.** One beta tester went from about 50 to 1,900 PRs a week, and Cognition says testers merge "4.5x more PRs per message." Third parties note these count merges, not quality.
- **Fortune-only claims, not in the docs:** auto-merge rules for low-risk edits, budgets "measured against results," and mixing models from several labs.

### Capy Captains

Sources: [docs.capy.ai/captains](https://docs.capy.ai/captains), [features page](https://capy.ai/features/captains), [Captain vs Build blog](https://capy.ai/blog/captain-vs-build).

- "A long-lived agent that runs other threads for you," with one Captain per project or area. It runs in the cloud by default, and "its machine sleeps between turns." A paired device is an option.
- You create one with New Captain, by promoting an existing thread (which keeps its history, PRs, and Slack thread), or by asking any thread.
- **Crew threads** each get their own machine, branch, and PR. A thread belongs to one crew at a time. Captains can read and message each other.
- **State is files in the Captain's Drive.** `AGENTS.md` holds the Captain's own rules. `CREW.md` holds rules every crew thread reads each turn. `NOTES.md` holds the plan. `DIGEST.md` has Needs you, What's new, and In progress sections. There are also `skills/`, `reports/`, and `handoffs/` folders.
- **The only brakes are rules written in prose**, for example "Nothing merges without my word." A Captain "always acts as its owner, whoever messages it."
- **Billing.** Model usage goes to the owner's subscriptions. Cloud machine time comes out of plan minutes and credits, even when the model runs on your own subscription.
- **History.** The February 2026 blog describes a read-only "Captain" planner plus a "Build" executor. The current Captain is a different concept that reuses the name. A search snippet said Build was later retired, but the primary pages don't confirm that.

### OpenAI dots and Claude Duet (general always-on agents)

- **OpenAI dots** (2026-09-29, DevDay). Each dot has its own cloud computer, uses ChatGPT memory, and works in the background. You can message dots from Slack or Teams. Available on Pro, Business Premium, and some Enterprise plans. Microsoft is bringing specialist dots under Agent 365 governance. ([TechCrunch](https://techcrunch.com/2026/09/29/openai-launches-dots-its-bubbly-agentic-avatar/), [9to5Google](https://9to5google.com/2026/09/29/openai-dots-agent/))
- **Claude Duet** (2026-10-21, research preview). Runs in a browser-equipped cloud VM with a daily morning brief and more than 6,000 connectors. It asks before sending messages, making payments, signing, or deleting. US Pro and Max only, by waitlist. Its usage counts against plan limits. ([MacRumors](https://www.macrumors.com/2026/10/21/anthropic-claude-duet/), [AA](https://aa.com.tr/en/americas/anthropic-launches-claude-duet-ai-agent-to-rival-metas-muse-openais-dots/4096538))

These matter to AO as channels as well as competitors. The Grok Bot skill pack (`integrations/grok-bot`) already lets a general platform bot drive AO, and the same approach would work for Duet or dots.

### Other Orca competitors

- **Cursor Automations.** "Always-on agents that run based on triggers and instructions you define." Each run gets a cloud sandbox, and a memory tool learns from past runs. The 2026-06-18 update added a `/automate` skill (create from a local session), five more GitHub triggers, Slack emoji triggers, and computer use on by default. ([Mar 5](https://cursor.com/changelog/03-05-26), [Jun 18](https://cursor.com/changelog/06-18-26))
- **Claude Code routines.** Schedule, API, and GitHub-event runs on Anthropic's cloud. Slack is a connector for output, not a trigger. Pro is limited to 5 runs a day. ([announcement](https://claude.com/resources/articles/introducing-routines-in-claude-code), [docs](https://code.claude.com/docs/en/routines.md))
- **Warp Oz.** A cloud platform for orchestrating agents. Tagging the Slack bot starts a cloud agent that opens PRs back in the thread. Oz can also run on your own infrastructure. ([launch](https://www.warp.dev/newsroom/2026/2/10/warp-launches-oz-the-orchestration-platform-for-cloud-coding-agents), [Slack docs](https://docs.warp.dev/agent-platform/cloud-agents/integrations/slack/))
- **Conductor.** No bots. "Conductor for Slack" gives agents in cloud workspaces Slack's MCP tools: "Everything runs as you, with your Slack permissions." Linear issues are an entry point for workspaces. ([Slack](https://www.conductor.build/slack))
- **Paradev (Superset renamed on 2026-10-26, $11.5M seed).** Its automations open a workspace on the chosen Mac when a trigger fires, with up to 25 triggers per automation, on Pro and Enterprise plans. They are one-shot, with no bot identity. ([rename](https://paradev.com/blog/a-new-name))
- **Kiro Crew** (`kirodotdev/KiroCrew`, open source). "A persistent workspace for development work that self-improves and continues beyond one session." It is self-hosted, runs on `kiro-cli`, and its Crew Members keep private memory. Its affiliation with AWS isn't stated on the pages read. ([repo](https://github.com/kirodotdev/KiroCrew))
- **T3 Code.** No bots. "T3 Code has nothing with the same shape" as Orca Bots. ([bitdoze comparison](https://www.bitdoze.com/orca-vs-t3-code/))
- **GitHub Copilot and Codex.** Searches returned nothing on bot-style features. Not established, not ruled out.

## What AO already has

| Bot part | Existing AO substrate |
| --- | --- |
| Schedules | Durable RRULE/cron automations that start sessions, with catch-up and spawn leases (`backend/internal/service/automation/`) |
| Event-driven work | Issue intake that starts one worker per eligible GitHub/GitLab issue (`backend/internal/observe/trackerintake/`), plus the SCM observer for PR, CI, and review state (`docs/scm-observer.md`) |
| Orchestrator that delegates | The `KindOrchestrator` session kind (`backend/internal/domain/session.go`). Workers report progress and "needs input" to it |
| Long-lived context | Chat hibernate, handoff notes, checkpoints, side-chat memory (`backend/internal/service/chat/`) |
| Mobile and notifications | Push, mobile bridge, Cloudflare/Tailscale tunnels, keep-awake and closed-lid wake (`backend/internal/mobilebridge/`, ADRs 0004–0006) |
| Always-on hosts | Self-hosted remote hosts (`docs/self-hosted-remote.md`) and AO Cloud sandboxes (`private/ao-cloud`) |
| Team identity | AO Cloud control plane with orgs and auth (`docs/cloud-project-settings.md`) |
| Cost data | Usage and pricing services (`backend/internal/service/usage/`, `backend/internal/pricing/`) |
| Verification | Auto-review and the native reviewer (`backend/internal/autoreview/`, `backend/internal/review/`) |
| Chat-surface control | Grok Bot skill pack: status, answering questions and approvals, routing to orchestrators (`integrations/grok-bot/`) |

**Missing:**

- Inbound event ingress. Everything is polled today, and there is no webhook endpoint.
- A Slack app and Linear app identity.
- A persistent bot entity with identity and memory files.
- A "Needs you" inbox as the primary surface.
- An enforced "who can ask" list and a rule for whose credentials a request uses.
- Per-bot budgets.
- Templates.

## Where it is genuinely hard

1. **Ingress and identity.** Slack apps (one per bot, following Orca's App Configuration Token approach), GitHub App events, a Linear app, and webhook signing all need always-on infrastructure. AO Cloud is the natural relay. The daemon would connect out to it, with no inbound ports, which keeps the LAN-listener rules in `AGENTS.md` intact.
2. **Trust and permissions.** Text from Slack and issues is open to prompt injection. "Whose access does a teammate's request use?" has no safe default: Orca uses the owner's access, and Devin pools credentials. AO should enforce approvals and permissions in the daemon, not in the prompt. This is the part that takes months.
3. **Long-lived context.** Orchestrator transcripts degrade over weeks. That needs file-based memory, periodic recycling, and a memory tidy-up with no tools.
4. **Inbox quality.** A noisy bot gets muted within a week, which kills the product. Fixing this takes dogfooding, not architecture.
5. **Runaway cost.** One bot can quietly start dozens of sessions. Per-bot budgets and spend visibility were the top unclaimed lane in the 2026-08 competitive sweep, and none of the bot products has strong cost controls.
6. **Event reliability.** Dedupe, idempotency, catch-up after sleep, and expiry. The automation scheduler's catch-up limit and spawn lease are the pattern to reuse.

## Proposed plan

Estimates assume agents do most of the coding.

**Phase 0: local bots (~2 weeks).**
- A `Bot` entity layered on the orchestrator session kind: name, purpose, memory folder (`SOUL.md`, `MEMORY.md`), and its own schedules (reusing automations).
- A "Needs you" inbox, on desktop and mobile, fed by worker reports and pending approvals.
- Recycling plus a nightly memory tidy-up.
- Dogfood on AO's own backlog: issue triage, CI failures, PR review.

**Phase 1: ingress through AO Cloud (~3–4 weeks).**
- A relay the daemon polls out to, covering Slack (an app per bot), GitHub App events, Linear, and signed webhooks.
- "Who can ask" enforced in the relay.
- Issue intake rebuilt as bot rules.
- Replies go back to the thread, PR, or issue the request came from.

**Phase 2: differentiators (~3 weeks).**
- **Hybrid home.** A bot's home can be the laptop, a remote host, or a cloud sandbox, and it moves to the cloud when the laptop sleeps. No competitor offers this.
- Hard per-bot budgets and a cost ledger.
- Approvals enforced by the daemon instead of prompt rules.
- The native reviewer checks every bot PR automatically.
- Templates modeled on the strongest Orca and Devin use cases.

**Phase 3: team bots.**
- Org sharing roles (use, configure, share) and an audit log.
- Requests run on the requester's credentials, not pooled ones.
- Admin bot page with limits.

That is roughly **8–12 weeks to match Orca's beta**, with cost, always-on, and enforced permissions as AO's edge. Getting it right after that depends on inbox quality from real use.

**Business fit.** Free desktop bots run locally. The paid cloud tier is what keeps a bot always on, reachable from Slack and Linear, and shared across a team. That keeps the local-first trust story and gives the cloud a concrete reason to pay.

## Open decision

Should bots be local-first with a cloud relay (Orca's model, fits the free desktop app) or cloud-first (Devin's model, fits the paid cloud)? **Recommendation: a hybrid.** Phase 0 is local, ingress goes through AO Cloud from Phase 1, and the cloud home is the paid always-on upgrade.

## Caveats

- The claim that Capy's Build agent was retired comes only from a search snippet.
- Devin auto-merge and results-based budgets come only from Fortune.
- Orca's pricing FAQ answers didn't render, and the Orca beta date comes from release notes and third parties.
- The Kiro Crew README was only partly read.
- Paradev's cloud-workspace support for automations is unconfirmed.
- Copilot and Codex were not covered.
