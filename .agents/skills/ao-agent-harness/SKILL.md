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

## Judge the evidence

The strict audit requires every mandatory gate. Native proof does not replace AO auth
or model evidence. `configured` records credential presence; user authorization
to test and a successful native call do not change the adapter's returned state.
Missing evidence stays `BLOCKED`, `FAIL`, or `NOT_RUN`.

When explicitly investigating later lifecycle behavior after native proof,
`--diagnostic-after-native-proof` may continue past configured/unknown AO auth
or a successful-but-empty model response. It retains the original non-pass
results and never exits as a full passing audit. Explicit login failures,
stale probes, missing binaries, and failed HTTP requests still block dependent
work. See the exact prerequisites in the evidence reference.

Restore HTTP 200 and synthetic `idle` do not establish native input readiness.
Supply a cue for the initialized empty composer with the configured model and
workspace, not an echoed prompt, loading screen, or draft. Missing cancellation
or restored-readiness contracts yield `NOT_RUN` without sending those turns.
For a native CLI that restores the cancelled prompt as a draft, the local
contract documents optional post-cancel cleanup for exclusive runner-owned
sessions. It requires a current audit-draft cue and verified empty composer;
it never clears a user draft or relaxes restored readiness.
Activity requires an observed active-to-settled turn. Hidden-instruction token
consumption proves delivery, not confidentiality against model echo. The runner
introduces a fresh restore token only after confirmed kill and verifies its
project-config write before restore, so initial history cannot supply it.

Every claim that a harness runs **inside AO** requires an attached real screenshot
of that harness session in the actual AO UI, with AO context and live native
output visible. Native CLI success, terminal mirrors, text/log PNGs, and synthetic
UI do not satisfy this requirement. Follow the required capture record and
publication gate in [Evidence and handoff](references/evidence-and-handoff.md#required-ao-ui-evidence).
A runner `PASS` alone is insufficient: missing AO UI screenshots leave
`AO UI evidence: BLOCKED` and the integration handoff incomplete.

Preserve the first failure, later fixes, and controls separately. Label every
failed/blocked/not-run gate and attach its actual failure screen when available;
otherwise report `screenshot: unavailable` with the reason. Screenshots show
visual state; retain functional logs/assertions for cancellation and exact
restore. Inspect artifacts for secrets before sharing. Report the verdict,
non-pass gates, tested source and binary, evidence links, and cleanup state.
Publish detailed PR reports only when requested or already authorized; otherwise
summarize and attach the same evidence in the authorized handoff.

## Validate changes

On the authorized execution host, from this skill directory:

```bash
node --test scripts/*.test.mjs
```

Fixtures use local fake daemons/processes and real loopback WebSocket transport,
without provider credentials or calls. A passing fixture suite proves runner
behavior, not provider conformance or real screenshot coverage. Follow the
repository's CI/PR guidance for integration changes.
