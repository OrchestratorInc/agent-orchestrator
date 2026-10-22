---
name: ao-agent-harness
description: Use when given a link, repo or name of a coding-agent CLI to integrate into AO as a harness (TUI, Chat, installer, models, auth/login), when completing or reviewing a harness integration PR, or when auditing an integrated harness (Codex, Claude Code or any other) with step-by-step screenshots and a system-prompt check.
---

# AO harness: integrate and audit

Give this skill a link or a name (a GitHub repo, npm/PyPI package, docs page)
and it walks you from "is it already in AO?" to a working, audited harness:
the TUI and Chat adapters, installer, model list, auth detection, login button
and Connected state. It then verifies everything in the real app, with a
screenshot for every step, pass or fail.

Work through the steps in order. Each one says what to do, which tool runs it,
and when it is done.

| Step | What | Tool | Done when |
| --- | --- | --- | --- |
| 0 | **Intake:** already integrated? PR already open? | `scripts/harness_intake.mjs --link URL` | Decision printed: new, continue PR, complete existing, or update checkout |
| 1 | **Compatibility:** what the CLI supports and how much AO can use | [integration-path §1](references/integration-path.md#1-map-the-upstream-contract) | Compatibility table filled from the CLI's real `--help`/behaviour |
| 2 | **Build** in AO (order below) | `scripts/harness_touchpoints.mjs --harness ID --like SIMILAR` | Every required touchpoint `ok` for the surfaces you support |
| 3 | **Local test:** standalone agent, New Task, TUI and Chat | `scripts/run_local.sh audit ID` | Live audit gates pass |
| 4 | **UI audit** with screenshots | `scripts/ui_audit.mjs` | Every checkpoint has a PASS/FAIL screenshot |
| 5 | **V1: system prompt** (worker vs orchestrator) | `--suites roles` (included in `audit`) | Both sessions name their own role |
| 6 | **Fix, then hand off** | `run_local.sh next`, `harness_pr_check.mjs` | Fix loop `done` (or only `needs_user`/upstream left); evidence posted on the PR |

## Step 0: Intake

```bash
node scripts/harness_intake.mjs --link https://github.com/org/agent-cli --binary agent --repo /path/to/ao
```

It scans the AO checkout for the harness, searches all PRs (open, merged,
closed) on `OrchestratorInc/agent-orchestrator`, and reads upstream facts
(release OS coverage, installers, and what the README and the local `--help`
mention). Follow its decision:

- **Open PR:** continue that PR, do not duplicate it.
- **Already integrated:** jump to step 3 and audit it.
- **Merged but missing locally:** update your checkout first.
- **New:** go to step 1.

## Step 1: Compatibility

Pin a released version. Then fill the table in
[integration-path §1](references/integration-path.md#1-map-the-upstream-contract)
from the CLI's real behaviour, not the README. The intake's hints are only a
starting point. Answer at least:

- Does it have an interactive TUI that AO can launch?
- Does it have a structured protocol for Chat: **ACP** (Agent Client Protocol)
  or an SDK/app-server?
- Can it list models? How does it log in? Which OSes and installers does it
  support?
- Can it resume a session by ID, and how does it take a system prompt?

Record what it does not support, with proof. Those become truthful "not
supported" states, not gaps.

## Step 2: Build, in this order

Copy the closest existing harness. `--like` prints where that harness is wired
in every file:

```bash
node scripts/harness_touchpoints.mjs --harness <id> --like <closest-harness> --repo /path/to/ao
```

1. **TUI adapter:** launch, prompt delivery, hooks/activity, native session ID,
   resume, and the system prompt channel ([integration-path §3](references/integration-path.md#3-implement-the-tui-adapter)).
2. **Chat:** only if ACP or an SDK exists and passes its checks
   ([chat-integration](references/chat-integration.md)).
3. **Installer:** every OS and installer the CLI officially supports, so AO's
   **Install** button works (touchpoints `install-target`, `install-plan`).
4. **Models:** list them (`selectionMode: catalog`), or declare free-form entry
   (`text`) when the CLI has no list.
5. **Auth detection** on macOS, Linux and Windows. Report `authorized` only after
   real verification. A key that is merely present is `configured`.
6. **Login/Setup button:** whatever the harness offers (CLI login, API key,
   config), as an auth plan (touchpoint `auth-plan`).
7. **Connected state:** after auth and configuration the Agent Harness page
   shows Connected. Otherwise it shows the same Not connected / Configure state
   other harnesses show.

Then add branding (logo and licence), docs, and the reviewer if it is
supported. Read [lessons from earlier harness PRs](references/lessons-from-harness-prs.md)
before writing the adapter. The same mistakes failed 16 PRs.

## Step 3: Local test (API audit)

```bash
scripts/run_local.sh doctor        # Node, AO checkout freshness, CLIs, logins
scripts/run_local.sh audit <id>    # live audit with your system's existing logins
```

The runner builds AO from the checkout and runs it on an isolated daemon. It
then drives the harness through the API:

- install and auth readiness
- the model list
- a standalone (scratch) TUI session: spawn, prompt, follow-up, cancel, kill
  and exact restore
- Chat: spawn, messages, approvals, interrupt and restore
- the TUI ↔ Chat switch
- the system-prompt checks

A new harness needs its own entry in a local CLI contract. Start from
[`assets/local-contract.codex-claude.json`](assets/local-contract.codex-claude.json)
and the [contract reference](references/local-cli-contract.md).

## Step 4: UI audit with screenshots

Start the real desktop app from the AO checkout with a debugging port. The
first time only, run `npm ci` in `frontend/`. See the `ao-desktop-dev` skill for
details.

```bash
cd /path/to/ao/frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_PORT npm run dev -- -- --remote-debugging-port=9222
```

Then, in another terminal, run:

```bash
scripts/run_local.sh ui <id>            # add --install / --login to click those buttons too
```

It clicks through the same journey a user takes, and saves a screenshot at
every checkpoint, success or failure:

1. **Settings › Harness lists it**, and its detail view opens.
2. **Account tab: install.** Install is offered, or it is already installed.
3. **Health tab** shows Authentication, Executable and Last checked.
4. **Models tab** lists the harness's models (with their on/off switches).
5. **Account tab: login.** Login or Set up is offered, or it is already
   Connected.
6. **Auth check:** Health › Check again says Connected.
7. **Update** on the Account tab. **Skipped in v0.**
8. **New Task** lists the agent and its models.
9. **Spawn a task**, and see the running TUI and/or Chat.

Each checkpoint is `PASS`, `FAIL`, `BLOCKED`, `NEEDS_USER` or `NOT_RUN`, with
its screenshot and reason, in `ui-report.md`. By default the audit installs and
logs in nothing: it reports what is offered. Selectors live in one table at
the top of `scripts/ui_audit.mjs`; update them there when the UI changes.

## Step 5: V1, the system prompt

`--suites roles` spawns one **worker** and one **orchestrator** and asks each:
"Are you a worker or an orchestrator?". Each must answer with its own role,
from AO's system prompt alone. A wrong answer means the role prompt never
reached the agent. The `instruction_channel*` gates then show where it was
lost: argv flag, Chat protocol, config file, or the last-resort rule file (see
[local-cli-contract](references/local-cli-contract.md)).

## Step 6: Fix, then hand off

Every run writes `<report>.next-actions.json`.

1. Run `scripts/run_local.sh next` to see the next fix.
2. Fix it, commit, then run `scripts/run_local.sh only <gate> <id>` to rerun
   just that gate.
3. Stop when only `needs_user` items remain (a login or API key you must
   provide) or upstream limits.

Before pushing, run `scripts/harness_pr_check.mjs --base upstream/main --pr N`.
Post the reports and screenshots as **comments on the integration PR**, never
commit them. Details: [fix loop](references/fix-loop.md),
[evidence and handoff](references/evidence-and-handoff.md).

## Rules

- **Auth:** use the machine's existing logins. Never log in or out, read
  credential files, or paste keys yourself. When a login or API key is
  missing, stop and ask the user for it.
- **Never pass a check by weakening it.** A pass must come from product behaviour.
- **Use a fresh report folder per attempt,** outside the Git checkout.
- **Audit an up-to-date AO checkout.** `run_local.sh doctor` shows how far
  behind it is.
- **Missing screenshots stay `BLOCKED`** with their reason. They never count as
  passes.

## Reference map

| Need | Read |
| --- | --- |
| Full build checklist and completion matrix | [integration-path](references/integration-path.md) |
| Chat (ACP/SDK) wiring | [chat-integration](references/chat-integration.md) |
| Local CLI contract fields | [local-cli-contract](references/local-cli-contract.md) |
| What every gate proves | [capability-suites](references/capability-suites.md) |
| Manual UI journeys and screenshot checklist | [audit-journeys](references/audit-journeys.md) |
| Failure classes and the fix loop | [fix-loop](references/fix-loop.md) |
| Gate contract and evidence rules | [evidence-and-handoff](references/evidence-and-handoff.md) |
| Mistakes from 16 earlier harness PRs | [lessons-from-harness-prs](references/lessons-from-harness-prs.md) |

Fixture tests (offline): `scripts/run_local.sh fixtures`.
