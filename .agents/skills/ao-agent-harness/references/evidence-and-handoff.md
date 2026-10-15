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
| Messaging and cancellation | Second message changes the proof; cancellation requires a tokenized foreground-command PID plus kernel process-start identity witness while active, terminal input through the native contract, the same terminal settling, that exact process stopping, and absence of its scheduled late side effect |
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
   to find the correct AO session UI. A separate terminal is supplemental
   evidence. The JSON initially records capture as unavailable; it is updated
   after the helper finishes.
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


## Required AO UI evidence

Before claiming a harness's AO user journey is verified, complete every row of
the [mandatory step-by-step screenshot checklist](audit-journeys.md#mandatory-step-by-step-screenshots).
Capture the actual AO desktop or web UI connected to the tested isolated daemon,
with the relevant Agent Harness page, New Task dialog, board/archive or selected
session visible. Include each action and resulting state, both model surfaces,
and each claimed running interface (TUI/Chat). Show enough AO navigation and
harness/session context to identify the subject. A single running image, provider
CLI in an external terminal, terminal mirror, log rendered into a PNG, mockup or
synthetic UI cannot establish the complete journey.

Plan capture before the live run. The runner is API-first: it does not launch an
AO UI, capture successful gates, upload images, or enforce this publication gate.
Its `--capture-command` hook only handles failed/blocked gates. Provision a real
AO UI and capture successful session evidence separately before cleanup, or use
`--keep-daemon` and then clean up the exact owned sessions/daemon after capture.
Use isolated AO data on the authorized host; a VPS-only run requires the UI and
capture there too. If the UI cannot run or capture fails, keep the functional
results and report `AO UI evidence: BLOCKED`; do not claim the integration is
fully verified or convert missing screenshots into a pass.

Maintain a per-harness, per-attempt checkpoint index in its PR evidence comment:

| Checkpoint/substep | Action and expected → observed state | Functional status | Screenshot/attachment status | Image and functional proof or reason |
| --- | --- | --- | --- | --- |
| `install-run/result` | AO-page install → actual observed outcome | `PASS` / `FAIL` / `BLOCKED` / `NOT_RUN` / justified `N/A` | Captured and attached, or `unavailable` with reason | Linked real image(s), install-job/log receipt, or blocker and next action |

Include all checklist rows, failed attempts, skipped branches and planned features.
If a required UI feature does not exist, use functional `NOT_RUN` and reason
`NOT_IMPLEMENTED`, with a real capture of the available surface when possible;
it is not `N/A` or proof of the missing feature. Only explicit upstream limits or
agreed scope with truthful rejection justify `N/A`. Keep functional and screenshot
status separate: a screenshot cannot upgrade a failed function, and a passing
API probe cannot fill missing UI proof. The AO UI evidence gate passes only when
all applicable checkpoints have their required real captures and accessible
attachments. Complete journey verification also requires functional PASS on all
applicable steps; missing requested UI leaves the handoff incomplete.

Include this capture record beside each image (shared build fields may be linked
from the attempt header; every image still needs its own checkpoint/state/time):

| Required field | Contents |
| --- | --- |
| Checkpoint and attempt | Checklist ID/substep, unique attempt ID, action and expected/observed result; original attempt or labelled later reproduction |
| Harness and interface | Registered harness ID, selected host, page/interface, provider/model and native CLI version when known |
| Session identity | AO/native session ID or SHA-256 digest when redacted; correlate with functional proof. Pre-session install/auth/model checkpoints use agent/host/job identity and explicitly say no session yet |
| Tested build | Exact AO source SHA, executable hash, and UI source SHA if different |
| Capture | UTC timestamp, actual screenshot tool/command, AO desktop/web surface, local artifact path, and image SHA-256 |
| Observed state | Running/output, original failure, or later reproduction; name the gate and state exactly what is visible |
| Attachment | Durable GitHub-renderable image URL embedded with Markdown image syntax, plus the associated functional evidence link |

Preserve enough application context after masking sensitive information. If a
safe real screenshot is unavailable, record that fact and the capture reason.
Never create substitute pixels or replace an unavailable original with a later
reproduction. Screenshots prove a visible state only: cancellation, native-ID
continuity, refreshed instructions, and restore still need their independent
functional assertions and logs.

Label failures explicitly as `FAIL`, `BLOCKED`, or `NOT_RUN`, with the gate,
reason, timestamp, checkpoint/substep, attempt and AO session when one exists.
Capture the actual relevant AO error/failure state before cleanup when possible.
Retain the failing request/command, sanitized stdout/stderr or install/auth/daemon
logs, exit/error code, expected versus observed outcome and relevant identity/file/
process witness. Correlate these with the screenshot; collect only relevant logs
and redact secrets before sharing. Dependent steps stay `NOT_RUN` with the blocker;
continue safe independent checks without manufacturing success. A preflight/startup
failure may have no AO session or screen; use `screenshot: unavailable` and explain why.
A later reproduction gets its own timestamp and label and does not erase the
original missing capture or failure. Screenshot availability never upgrades a
failed functional gate.

## Capture provenance and sharing

A headless VPS may use a live terminal attached to the owned audit session and
an actual screenshot utility under a virtual display. Such a terminal mirror
must be labeled as a live mirror, not as the AO Electron UI, and does not satisfy
the required AO UI evidence above. Existing historical terminal-mirror images
retain that label; do not retroactively claim they prove execution inside AO.
Save capture time, source session/terminal generation, source/binary versions,
and whether it is the original failure or a new reproduction. An archived ANSI stream replayed
into a terminal is playback and must not be presented as the original screen.

The capture hook is not a built-in terminal viewer: host provisioning, live
attachment, screenshot masking, and external image hosting remain outside this
runner. Without that host helper, original failures have text/API evidence and
`screenshot: unavailable`. A later screenshot cannot retroactively fill that
gap. Never upload credential stores, process environments, raw sensitive
terminal output, or a whole profile directory with a report.

In a PR evidence comment, record tested source/binary hashes, native version,
provider/model (without credentials), fixture/live/CI scope, gate summary,
failed attempts/fixes, limitations, screenshot provenance and cleanup result.
Embed required checkpoint images in journey order using
`![checkpoint: observed state](attachment-url)` and link them from the index.
Every claimed running interface needs its own real AO UI proof. Upload new images
as durable GitHub attachments that render in the comment; never commit them to
obtain a raw/blob URL. Attach sanitized logs/receipts, or include bounded excerpts
and JSON in fenced blocks. Split oversized evidence into numbered PR comments
linked from one index comment; retain attempt identity and original-byte hashes.
A local/VPS path, expiring CI artifact or unavailable image link is insufficient.
Verify attachment access/rendering and read back the published comments.
If upload/rendering fails, label the attachment unavailable and the AO UI
evidence `BLOCKED` until corrected. Do not create reports on other services
unless the user requested that publication.


On POSIX hosts, command timeouts signal the command's owned process group with
SIGTERM and retain a bounded SIGKILL grace even if its leader exits first.
Windows fallback targets the child process. Detached descendants that create a
new session/group require native cleanup outside this command helper. A clean
runner report is not evidence that externally interrupted or deliberately
reparented processes have been reaped.

## Build provenance and validation scope

Record the actual serving daemon PID, executable hash, build revision/dirty bit,
runfile, native identity digest, and terminal generation before and after UI
capture or relaunch. Electron may replace the daemon; checkout HEAD alone does
not identify the binary serving the captured session. Preserve its required
native profile/PATH environment and requalify changed binaries.

Report native settlement separately from AO observation latency, and distinguish
a cancelled command's delayed side effect from a later explicitly authorized
rerun. Preserve stricter failed probes and label new controls independently.
Executing-child interrupt and AO Kill are separate gates: record kernel process
identity, bounded descendant reaping, no delayed artifact, and an unrelated live
control. State the tested OS; Linux proof does not establish Windows/macOS cleanup.

Honor the user's execution constraints. Record complete current-head CI results;
omitted/interrupted suites remain `NOT_RUN`, and previous fixture results remain
historical. Distinguish CI merge-build source from the implementation/UI head.
Qualify proprietary Chat protocols through actual daemon Chat APIs, including
streaming, approvals, and reconnect; standalone native receipts and this TUI
runner cannot replace those checks.

## Current status and reproduction receipt

Maintain one current-status evidence comment on the authorized integration PR.
Link it from the concise PR description; keep detailed matrices, screenshots,
logs and receipts in comments/attachments. Include the
[completion matrix](integration-path.md#completion-matrix) and these fields:

- **Current head:** SHA, CI result/links for that SHA, merge/conflict state and
  unresolved work. Green CI does not establish merge readiness or runtime proof.
- **Runtime-tested build:** exact AO/native/runner/UI revisions and executable
  hashes, OS, permission scope and attempt ID. If this differs from current head,
  name the intervening changes and what remains untested. Requalify affected
  behavior before calling it current-head verified.
- **Reproduce:** sanitized invocation and local-contract bytes plus hash, including
  probe argv, interrupt/readiness/draft-cleanup settings. For manual or standalone
  checks, provide equivalent pinned helper/commands and assertions. Explain
  redactions and unavailable inputs without exposing credentials or profiles.
- **Step-by-step UI evidence:** complete checkpoint index with action/result
  images and functional receipts, including both model surfaces, authentication
  branches, original failures and all unavailable/unimplemented steps. State the
  aggregate AO UI evidence and functional journey results separately.
- **Artifacts and cleanup:** report/receipt/image links and hashes, failed attempt
  references, owned-process teardown, and an inventory of retained artifact/data
  classes and their authorized locations. State which originals are unavailable;
  exclude credential stores, raw databases and profile directories from sharing.

Use fresh attempt paths for standalone, reconnect and capture helpers too. Preserve
original receipts before later actions; helpers must not overwrite lifecycle
proof. Label a reconstructed summary and its supporting observations explicitly;
it does not replace missing original bytes. Later reproductions remain separate.

Before publishing or handing off, reconcile the description with the final net
diff and current GitHub state: category counts, binary count wherever repeated,
capability claims, tested SHA, CI, merge conflicts and evidence links. Keep old
attempts and receipts identifiable as historical; update stale prose that calls
them current. Follow the repository PR-description skill for count calculation
and preserve unrelated description content. Read back the published report and
verify its links. These fields are contributor-authored; runner report schema
and automated validation are unchanged.

## Evidence ownership by integration PR

Audit evidence belongs in comments on the harness's integration PR and their
GitHub attachments. The PR description contains a concise validation summary
and links to the current evidence/index comments. Do not commit per-run Markdown
reports, JSON receipts/contracts/provenance, logs or audit screenshots anywhere
in the integration or skill branch, including `docs/research/` and asset folders.
Generated runtime files still exist in isolated local/VPS storage outside the
Git checkout; this policy changes publication, not runner output or retention.
Reusable synthetic test fixtures, source documentation and shipped product
assets such as harness logos and their licenses remain legitimate source files.

If upload/comment publication is unavailable, keep sanitized originals in host
storage, report attachment/publication `BLOCKED` and give the reason. Do not
substitute an evidence commit, external report service or expiring CI artifact.
Keep prior failed-attempt comments intact; later reproductions get separate
attempt-labelled comments. A current index may link all attempts without
rewriting failures into success. Never upload credentials or whole profiles.

For evidence already committed in an open PR, preserve original bytes, hashes,
failure labels and standalone-versus-registered scope; publish the report in
comments and upload its artifacts there. Verify every destination attachment and
comment link before removing only evidence files from the current PR diff.
Record migration and any unavailable originals in the comment, recheck the final
diff and category counts, and retain historical comments/commit references as
historical proof. Do not rewrite Git history or remove product assets/fixtures.
Evidence relocation establishes no new runtime PASS. New evidence must use
attachments even when old, already-published pinned links remain historical.

## Independent standing-instruction controls

Record instruction delivery separately from provider wire role. Additive private
system/developer inputs, hidden user-role hook context, and visible task text are
different routes. Observe wire roles only through a legitimate controlled provider
endpoint; label authenticated roles unknown when unobserved. Preserve native and
project/user rules; replacing the native system prompt is not additive delivery.
Bound aggregate native hook context and document fail-open/truncation behavior.

Use an ordinary visible file task that never names the private token, private
instruction path, private witness, or reading standing/project instructions.
Give the private instructions their own distinct prescribed file action and
an independent project rule control. On exact restore, introduce a new hidden
token only after confirmed Kill, inspect the current empty native composer,
and send another ordinary task. Preserve the first failed continuation even
if a later separate control passes.

The TUI runner's prompts that explicitly request hidden/project token fields
are assisted consumption controls. Their token gates do not establish the
independent ordinary-task proof above. Keep that scope visible in reports;
use a focused registered daemon session for the independent control. A
proprietary Chat harness still requires its actual Chat API and lifecycle.

For ordinary daemon `/sessions/{id}/send` follow-ups, set `userAuthored: true`.
Omitting it is coordination delivery: the native agent can legitimately treat
its tagged content as an unsolicited instruction. Keep coordination rejection
separate from user-input loss. Exercise long multiline tasks through the
actual guarded AO input path; a native fixture that already uses bracketed
paste can hide raw chunk submission and stranded-draft defects.

A supported read-only native SDK view can confirm that the new managed append,
native defaults and user rules are configured together. It cannot establish
model consumption or the authenticated provider wire role. Retain a configured
NEW token with an unchanged OLD private witness as a failed consumption control.

After every Electron relaunch, re-read the serving PID and preserve the native
profile/PATH environment needed by the replacement daemon. Binary lookup or
version-probe failure does not prove missing history rejection. A missing-history
control must reach that check, reject restore without a fresh conversation,
and restore the exact owned withheld history bytes afterward.

Failed or blank screenshots remain failed captures. Record any real graphics
fallback, exact UI source, and serving binary; DOM text, terminal-ring text, and
generated pixels cannot replace native viewport evidence.
