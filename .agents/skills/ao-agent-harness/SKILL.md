---
name: ao-agent-harness
description: Use when given a link, repo or name of a coding-agent CLI to integrate into AO as a harness (TUI, Chat, installer, models, login), when completing a harness integration PR, or when auditing any integrated harness live and in the desktop app with evidence and a report.
---

# AO harness: integrate and audit

Give this skill a link or a name for a coding-agent CLI. It takes you from
"is it already in AO?" to a working harness that you have checked live, with
evidence and a report that says what to fix.

It works the same for every harness. Everything specific to one harness lives
in that harness's **contract**: one JSON entry you write in step 3. Codex and
Claude Code are already integrated, so copy from them as worked examples.

| Step | What | Run | Done when |
| --- | --- | --- | --- |
| 0 | Intake: already in AO? A PR already open? | `node scripts/harness_intake.mjs --link URL` | It prints a decision |
| 1 | Compatibility | [integration-guide §1](references/integration-guide.md#1-compatibility) | The table is filled from the CLI's real behaviour |
| 2 | Build it in AO | `node scripts/harness_touchpoints.mjs --harness ID --like CLOSEST` | Every required touchpoint is `ok` |
| 3 | Write its contract | `assets/local-contracts.json` | `run_local.sh doctor ID` shows its version and login |
| 4 | Live audit | `scripts/run_local.sh audit ID` | `report.md` has no AO-side failures |
| 5 | UI audit with screenshots | `scripts/run_local.sh ui ID` | Every checkpoint has a PASS or FAIL screenshot |
| 6 | Fix, then hand off | the report's **Fix** section | Only user-side items are left; evidence is posted on the PR |

## 0. Intake

```bash
node scripts/harness_intake.mjs --link https://github.com/org/agent-cli --binary agent --repo /path/to/ao
```

It checks the AO checkout for the harness and searches open, merged and closed
PRs. It also reads upstream facts (releases per OS, installers) and the local
`--help`. Follow its decision:

- **Open PR:** continue that PR; don't start a second one.
- **Already integrated:** go to step 3.
- **Merged but not in your checkout:** update the checkout.
- **New:** go to step 1.

## 1–2. Compatibility and build

Follow [references/integration-guide.md](references/integration-guide.md). It
has the compatibility table, the build order (TUI adapter, Chat driver,
installer, models, auth detection, login button, Connected state) and the
mistakes that failed earlier harness PRs. Read its "Avoid" list **before**
writing the adapter.

## 3. Write the contract

Copy the entry in [`assets/contract-template.json`](assets/contract-template.json)
into [`assets/local-contracts.json`](assets/local-contracts.json) under the
harness's AO ID. Fill it from the CLI's real behaviour:

| Field | What to put |
| --- | --- |
| `binary`, `versionArgs` | The CLI's command and its version arguments |
| `integration` | `args`: a login-status command; `expect`: text it prints only when logged in |
| `models` | `args`: a model-list command, and `format` (`json` or `lines`). Leave it out if the CLI has none |
| `surfaces` | `["tui"]`, `["chat"]` or both: what AO integrates |
| `adapters` | AO's folder names for the TUI adapter and Chat driver. They default to the ID without dashes, plus `acp` for Chat |
| `restoredReady` | `patterns`: regexes that match only the empty input box after a restore (anchored to the bottom of the screen) |
| `model` | Optional: a model the audit uses for every session, one the logged-in account can run. Leave it out to test AO's own default |

## 4. Live audit

```bash
scripts/run_local.sh doctor ID   # Node, Go, the checkout, the CLI and its login
scripts/run_local.sh audit ID    # uses the machine's existing logins; no ID audits every harness
```

`scripts/audit.mjs` builds AO from the checkout and starts it on an isolated
daemon, in a temporary folder that it deletes afterwards. Then it checks:

| Group | Checks |
| --- | --- |
| CLI on its own | installed, version, logged in, model list |
| AO sees it | registered, installed, authenticated (verified, not just configured), models |
| TUI | spawn in a scratch project; first prompt runs once in the worktree; follows AO's system prompt and AGENTS.md; session status tracked by hooks (working → idle); session ID captured; follow-up; usage; Kill stops a running command; exact restore; remembers history after restore |
| Chat | spawn; first prompt, worktree, system prompt, AGENTS.md; follow-up; models and switching model; Kill and restore of the same conversation; remembers history |
| Roles (V1) | a standalone worker and an orchestrator each answer "worker" / "orchestrator" from AO's system prompt alone |

Each run writes a folder (default `~/Desktop/agents/ao-audit-reports/<time>-audit/`):

- `report.md`: every check with PASS, FAIL, BLOCKED (needs something first) or
  SKIP, and the reason.
- `evidence/<agent>/NN-<check>.json`: the API responses and command output
  behind each check.
- `daemon.log`, `run.log`.

The **Fix** section of `report.md` sorts every failure by who fixes it:

- **AO implementation:** a bug in the adapter or driver. It names the source
  folders to open. Fix it, add a Go test that fails without the fix, and
  rerun.
- **Harness contract or upstream CLI:** the CLI fails on its own, or the
  contract is wrong (for example a ready pattern that doesn't match the
  screen saved in the evidence file).
- **Machine setup:** something the machine lacks, such as AO's Chat bridge
  for ACP drivers (`npm ci && npm run build:acp-runtime` in `frontend/`).
- **Needs the user:** a login, API key, quota or install. Ask the user, then
  rerun.

## 5. UI audit with screenshots

Start the real desktop app from the checkout with a debugging port (the first
time, run `npm ci` in `frontend/`):

```bash
cd /path/to/ao/frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_PORT npm run dev -- -- --remote-debugging-port=9222
```

Then run `scripts/run_local.sh ui ID`. Add `--install` or `--login` to click
those buttons too. It takes the same path a user does, and saves a screenshot
at each step, pass or fail:

1. Harness page lists it.
2. Install.
3. Health.
4. Models.
5. Login.
6. Auth check says Connected.
7. Update (skipped in v0).
8. New Task lists the agent and its models.
9. A spawned task runs in TUI and/or Chat.

The screenshots and `ui-report.md` go in the run folder.

## 6. Fix, then hand off

Work through the Fix section, then rerun the audit until only user-side items
remain. Post `report.md`, the evidence and the screenshots as **comments on
the integration PR**. Never commit them.

## Rules

- **Auth:** use the machine's existing logins. Never log in or out, read
  credential files, or paste keys yourself. When something is missing, ask
  the user.
- **Never make a check pass by weakening it.** A pass must come from product
  behaviour.
- **Audit an up-to-date AO checkout.** `doctor` prints its commit.
