---
name: ao-agent-harness
description: Use when adding or completing an AO coding-agent harness integration, checking an existing integration PR, or auditing native CLI and AO TUI lifecycle, cancellation, and exact-session restore.
---

# AO agent harness integration and audit

This contributor skill provides an integration checklist and an executable,
API-first audit runner. Discover registered harnesses from the selected AO
checkout; keep provider commands in a runtime contract, not an allowlist inside
the runner. It is not a shipped desktop feature, scheduled intake service,
automatic PR publisher, or Chat conformance suite.

| Task | Start here |
| --- | --- |
| Add/complete a harness | [Integration path](references/integration-path.md): check duplicate PRs, pin upstream, prove capabilities, implement/register only supported surfaces |
| Run the same lifecycle audit across harnesses | [Local CLI contract](references/local-cli-contract.md): native probes, cancellation input, restored composer cue |
| Add ACP Chat and reuse the existing UI | [Chat integration and verification](references/chat-integration.md): source entry points, capability gates, real Chat journeys |
| Operate an audit and identify coverage | [Operating recipe and journey map](references/audit-journeys.md): Doctor, actions, readiness, proof, UI gaps, cleanup |
| Interpret results, preserve failures, capture screens | [Evidence and handoff](references/evidence-and-handoff.md): gates, diagnostic mode, artifacts, authentication, screenshots |

## Run

Use Node 24 or newer, Git, SQLite CLI, installed native agents, and the Go version
required by the selected checkout when building AO. Prepare existing authorized
credentials in the execution account and isolated native profiles. Live audits
make provider calls and create disposable Git workspaces. Apply the user's
execution-host constraint: a VPS-only request means builds, fixtures, and live
runs all execute there.

```bash
node /path/to/ao/.agents/skills/ao-agent-harness/scripts/audit_ao.mjs \
  --agent example-agent --repo /path/to/ao \
  --local-contract /path/to/local-cli-contract.json \
  --report /path/to/audit/attempt-001/report
```

Use repeated `--agent ID` or `--all`. The runner builds and starts an isolated
AO daemon, waits for `/readyz`, then performs native checks and AO API checks.
`--ao PATH` selects a prebuilt binary but still starts isolated AO state. Its
actual executable basename must be `ao` (`ao.exe` on Windows): use a real copy
or hardlink, not a symlink to a renamed binary. Native hooks resolve canonical
`ao` through the daemon's child environment.

Use a fresh `--report` basename for every attempt. Default cleanup kills created
AO sessions and stops the daemon; `--keep-daemon` preserves them for inspection.
Native profiles and evidence remain for review. An externally interrupted runner
can leave owned processes behind; inspect the report and clean those exact
processes before retrying. `--no-live` skips provider turns
and AO sessions, but still builds/starts AO and runs safe binary, version,
integration, and model-list commands. It is not an execution-free review mode.

## Operating checklist

1. **Prepare:** resolve the authorized host, selected source/native build, existing
   credentials, isolated profile, native contract, and fresh report path from the
   repo and session context. Confirm the runner's bypass-permission scope.
2. **Launch:** use the command above; the runner owns isolated daemon/data/port
   setup. Plan UI capture first and use `--keep-daemon` if capture follows the run.
3. **Doctor:** before manual input/capture, verify the retained daemon's PID/port,
   build, cached readiness, session/workspace and current native composer.
4. **Drive:** use the [journey map](references/audit-journeys.md) for each action,
   prerequisite and expected proof. Qualify actual UI and Chat paths separately.
5. **Evidence:** retain action/result and functional artifacts; distinguish strict,
   diagnostic, assisted-instruction and independent user-path results.
6. **Cleanup:** verify owned session/process teardown and confirm report/artifacts
   survive it. Kept or interrupted runs require separately recorded cleanup.

See the map for the exact read-only Doctor routes and a fresh-reader/live rehearsal
check. Fixture success alone does not validate the operational recipe end to end.

## Judge and hand off the evidence

A strict audit requires every mandatory gate and successful cleanup. Credential
presence is `configured`, not verified authorization. Missing proof stays
`BLOCKED`, `FAIL`, or `NOT_RUN`. Explicit diagnostic continuation preserves those
results and cannot produce a full strict PASS.

Use the [evidence contract](references/evidence-and-handoff.md) for gate criteria,
artifacts, login handoff, and publication. Restore HTTP success or synthetic idle
is insufficient: require native readiness, exact-session continuity, and a fresh
instruction token introduced only after confirmed kill. Cancellation requires
an active command's PID/kernel-start witness, confirmed stop, and no delayed
side effect. Missing native interrupt/readiness contracts skip dependent turns;
see the [local CLI contract](references/local-cli-contract.md) for configuration
and optional owned-draft cleanup.

Runner token prompts establish assisted instruction consumption. Claims about
ordinary-task delivery require the reference's independent instruction controls.
For every inside-AO claim, attach a real screenshot of the claimed TUI/Chat
interface in the actual AO UI with functional evidence and capture provenance.
A runner PASS cannot fill missing `AO UI evidence: BLOCKED`.

Keep each attempt and failure distinct. Detailed harness reports, JSON receipts,
screenshots, and provenance belong with that harness's integration PR. The skill
contains reusable guidance and tooling. Publish reports only when requested or
already authorized, and inspect artifacts for secrets before sharing.

## Validate changes

On the authorized execution host, from this skill directory:

```bash
node --test scripts/*.test.mjs
```

Fixtures use local fake daemons/processes and real loopback WebSocket transport,
without provider credentials or calls. A passing fixture suite proves runner
behavior, not provider conformance or real screenshot coverage. Follow the
repository's CI/PR guidance for integration changes.
