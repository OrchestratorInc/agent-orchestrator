# Failure evidence and host handoff

This is the contributor runner interface a future AO caller could consume.
The skill remains standalone; it does not add an AO service, scheduler,
notification transport, or browser framework.

## Invoke and consume

Invoke `node <skill>/scripts/audit_ao.mjs` with an explicit AO checkout or binary,
selected agents, local CLI contract, and a **new report basename** for each run.
Use `--help` for flags. Reusing an existing artifact directory is rejected before
starting AO, so login retries cannot overwrite the original evidence.

Read report JSON schema version `4` (including its diagnostic metadata). `state` is `running` until cleanup/report
finalization, then `finished`. Do not treat intermediate snapshots as final.
`summary.status` is `PASS`, `FAIL`, `BLOCKED`, or `NOT_RUN`.
Cleanup failures affect the overall verdict even if functional gates passed.

| Exit | Meaning |
|---|---|
| 0 | Finished and all mandatory gates passed, including required cleanup |
| 1 | Failed, blocked, or incomplete audit; inspect the report |
| 2 | Invalid input or unusable report destination; inspect stderr and any report |

Configuration loading, build/startup, and discovery failures produce run-level
errors; agents that were never discovered are not invented. Invalid CLI syntax
or an unwritable artifact destination can prevent report creation. The runner has no SIGINT/SIGTERM shutdown handler. An externally interrupted
process cannot guarantee finalization or cleanup; the host must check `state`,
exit status, and its exact owned processes before retrying. Do not use broad
process-name kills on a shared host.

## Gate contract

Use names and evidence rather than a fixed report row count: an early
preflight exit can omit later preflight rows, while lifecycle prerequisites
record dependent gates as `NOT_RUN`. A complete strict path covers:

| Group | Required evidence |
| --- | --- |
| Native CLI | Resolved binary, version, safe integration probe, direct workspace/proof session, native model catalog |
| AO preflight | Registered harness, installed executable, fresh probe, verified authentication, effective launch readiness, non-empty AO model catalog |
| Cross-check | At least one exact model ID shared by native and AO catalogs |
| Initial TUI turn | Spawn, correct workspace, initial prompt handled once, hidden instructions consumed, project `AGENTS.md` consumed, proof file, observed active-to-settled completion |
| Messaging and cancellation | Second message changes the proof, cancellation of an observed active turn through the native terminal input contract |
| Native restore | Persisted native identity, confirmed termination, exact native-ID restore, same AO session/workspace, initialized native terminal, post-restore mutation, history/file continuity, refreshed hidden instructions |

The native ID is not in the public session DTO. The runner reads
`sessions.agent_session_id` from the selected isolated SQLite database with
`sqlite3 -readonly`, keeps the raw native value in memory for equality, and
writes its SHA-256 digest. This is an audit-only observation, not permission
to move CLI product behavior around the daemon/API boundary.

`PASS` requires positive evidence. A version string, accepted API send,
restore HTTP 200, kill acknowledgement, or immediate synthetic idle does not
prove an entire lifecycle. The restore token is created only after confirmed termination, appended to the
existing project rules through `PUT /projects/{id}/config`, and checked by a
fresh project read before restore. Other config fields must remain unchanged.
A failed write or readback makes `system_prompt_restore` fail and skips restore
continuation, preventing prior history from masquerading as refreshed input.
Standing-instruction proof checks consumption only;
use audit-only tokens because a model can repeat them in output. Proof files
are model-authored assertions backed by workspace/API observations, not a
formal guarantee that a model cannot forge a history answer.

## Explicit diagnostic mode

The default mode stops dependent AO lifecycle work at the first failed
preflight/model prerequisite. `--diagnostic-after-native-proof` allows later
functional evidence only when all these conditions hold:

- The same agent passed native binary, version, integration, and direct
  workspace-proof session gates, without a local login action.
- Targeted AO probe and readiness HTTP requests succeeded, installation is
  present, and observations are fresh.
- Either strict preflight passed, or only authentication is blocked and both
  returned auth states are among configured, unknown, authorized, and
  not-applicable, with at least one configured/unknown state.
- The AO models HTTP request succeeded, even if its catalog is empty.

Explicit unauthorized states, login actions, missing installation, stale
observations, failed native proof, or HTTP errors do not qualify. The mode
retains configured/unknown auth as `BLOCKED` and an empty AO catalog as `FAIL`;
missing native model discovery remains `NOT_RUN`. It marks the report
`auditClassification` as diagnostic-only and cannot exit zero or claim a full
strict audit pass, even if all later functional gates pass.

Existing user authorization can permit an unattended diagnostic run. It does
not convert AO's `configured` API state into `authorized`. Report the native
provider success and the adapter's unverified state separately. Do not rerun
login or request credentials to make the labels agree.

## Artifacts

For basename `/runs/attempt-001/report`:

```text
report.json
report.md
report.artifacts/
  runner.log
  daemon.log
  events.jsonl
  issue-0001.json
  issue-0001.png    # only when actual capture succeeds
```

Every observed failed/blocked gate has `agent`, `sessionId` when available,
`gate`, `status`, `reason`, timestamp, `evidencePath`, and `screenshot` status.
Local command evidence includes redacted failing stdout/stderr tails, exit code,
timeout, byte counts, hashes, timestamps, and arguments. Successful session
output stays hashed. AO evidence includes HTTP status, request ID, response,
and timings. Daemon output is bounded to its latest 64 KiB; issue files preserve
the tail observed at failure even if later shutdown output replaces daemon.log.
Missing or truncated output must not be described as a complete transcript.

Secrets in recognized keys, bearer values, common credential assignments, and
emails are redacted in text evidence. Do not collect credential stores or full
environment dumps. Redaction is best effort; inspect artifacts before external
sharing. PNGs cannot be sanitized as text: the capture helper must mask secrets
or decline capture if it cannot produce a safe image.

## Screenshot helper

```sh
node <skill>/scripts/audit_ao.mjs --agent example --repo /path/to/ao \
  --local-contract /path/to/contract.json --report /runs/attempt-001/report \
  --capture-command '["node","/path/to/capture.mjs"]'
```

`capture.mjs` is a trusted host-provided helper, not a bundled implementation.
The runner invokes the argv array directly without shell expansion, appending:

1. The issue JSON path. Read `agent`, `sessionId`, `baseURL`, `gate`, and evidence
   to find the correct audit UI or terminal. The JSON initially records capture
   as unavailable; it is updated after the helper finishes.
2. The destination PNG path. Write a real screenshot here and exit zero.

The hook runs at failed/blocked gates before subsequent lifecycle actions and
cleanup. It has a ten-second timeout. The result must be a regular PNG file
no larger than 10 MiB; a symlink, absent output, invalid signature, timeout, or
nonzero exit records capture failure without changing the original gate result.
The check verifies file type/signature, not screenshot relevance: that remains
the host helper's responsibility. Nonzero stderr should briefly explain why
capture was unavailable. With no helper, status is `unavailable` (headless).
API preflight and startup errors may have no visible screen; do not fabricate one.

## Login handoff

`events.jsonl` is append-only within a run. Tail it for:

- `type: "needs_user_action"`, `actionType: "authentication"`, `agent`,
  `issueId`, `message`, `requiresFreshProbe: true`, and `evidencePath`.
- `type: "issue"` with completed evidence and screenshot metadata.

The runner also prints the action on stderr and includes it in JSON/Markdown.
AO can later display the event in its existing user-notification surface.
The runner itself does not send Discord, Slack, or email messages.

AO auth that is fresh but unverified remains `BLOCKED`. Explicit local CLI
errors such as “session expired” also request user help, retain the failed
command gate, and skip dependent live turns. Generic timeouts/network errors
are not automatically diagnosed as expired credentials. Safe independent
checks and other agents continue. There is no automatic login or auth retry.

After the user resolves login in the audit account, invoke a new run for that
agent with the same intended inputs and fresh auth probes. Keep both attempts.
Do not mark auth passed solely because the user said “done”.

## Local verification

`node --test scripts/*.test.mjs` uses synthetic processes, HTTP responses,
temporary workspaces, and a fake daemon. PNG fixtures verify the capture
interface only. Real UI screenshots and real provider lifecycle conformance
require a separately authorized live audit in the eventual host environment.


## Capture provenance and sharing

A headless VPS may use a live terminal attached to the owned audit session and
an actual screenshot utility under a virtual display. Such a terminal mirror
must be labeled as a live mirror, not as the AO Electron UI. Save capture time,
source session/terminal generation, source/binary versions, and whether it is
the original failure or a new reproduction. An archived ANSI stream replayed
into a terminal is playback and must not be presented as the original screen.

The capture hook is not a built-in terminal viewer: host provisioning, live
attachment, screenshot masking, and external image hosting remain outside this
runner. Without that host helper, original failures have text/API evidence and
`screenshot: unavailable`. A later screenshot cannot retroactively fill that
gap. Never upload credential stores, process environments, raw sensitive
terminal output, or a whole profile directory with a report.

For a PR report, record the tested commit and binary hash, native version,
provider/model (without credentials), fixture/live/CI scope, gate summary,
failed attempts and fixes, unresolved limitations, screenshot provenance, and
cleanup result. Use actual repository or published URLs; workstation paths
are not public GitHub attachments. Do not create reports on other services
unless the user requested that publication.


On POSIX hosts, command timeouts signal the command's owned process group with
SIGTERM and retain a bounded SIGKILL grace even if its leader exits first.
Windows fallback targets the child process. Detached descendants that create a
new session/group require native cleanup outside this command helper. A clean
runner report is not evidence that externally interrupted or deliberately
reparented processes have been reaped.
