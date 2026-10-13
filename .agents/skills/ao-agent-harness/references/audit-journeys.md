# Audit operating recipe and journey map

Use this map with the selected checkout and native CLI contract. It describes
existing runner behavior and the additional user-path proof required for an AO
integration. HTTP route paths below are relative to `/api/v1/` except root `/readyz`;
the terminal WebSocket uses root `/mux`.
substitute only IDs from the same owned audit. Keep per-agent results on that
agent's integration PR, not in this guide.

## Prepare and launch

Read the checkout's `AGENTS.md`, documented run commands, current adapter, and
available test harnesses first. Resolve the execution host, native version/model,
existing authorized credentials, isolated profile, selected agent ID, and a fresh
report basename. Ask only for inputs that cannot be established from the repo or
existing authorization. The runner's TUI spawn currently requests
`bypass-permissions`; that scope must be authorized and does not qualify manual
or accept-edits permission modes.

Run the command in [SKILL.md](../SKILL.md#run). The runner builds the selected
checkout or uses a canonical `ao` binary, allocates its own loopback port, and
starts isolated data/run files. `/readyz` establishes daemon readiness; native
installation, authentication, models, and terminal readiness are separate gates.
`--no-live` still builds/starts AO and runs safe native probes. Add `--keep-daemon`
when successful UI evidence will be captured after the runner finishes. It does
not launch a UI or pause before each turn.

For actual UI verification, follow the checkout's desktop-lab instructions in
`AGENTS.md`. Connect only to the owned isolated daemon. Electron can replace it:
repeat Doctor and record the actual serving build rather than assuming the runner
and UI use the same process.

## Doctor: read-only checks before manual driving

The automated runner performs its own startup and fresh preflight checks. Before
manually driving or capturing a retained session, and whenever anything looks off:

1. Read `report.json` for `baseURL`, `runFile`, `dataDir`, `daemon.pid`,
   `daemon.port`, `daemon.command`, and the selected result's `sessionId`.
   These fields may be absent in an early running snapshot; wait for a populated
   report or stop. A default cleaned-up run is not a live instance to drive.
2. Read that exact `runFile`; its PID/port must match the owned launch record.
   `GET /readyz` on `baseURL` must succeed and return the same `pid`. Verify the
   reported `executablePath` and its hash against the chosen build record.
3. `GET /agents` must include the selected ID in `supported`.
   `GET /agents/readiness` reads cached installation/authentication/readiness.
   Cached observations do not replace the runner's fresh POST preflight.
   Unverified auth stays `BLOCKED`; only the evidence contract's explicit
   diagnostic prerequisites can permit diagnostic continuation.
4. For an existing session, `GET /sessions/{sessionId}` and
   `GET /desktop/sessions/{sessionId}/workspace` must identify the intended
   harness and disposable worktree. Before a live send, verify its terminal
   generation and current native composer cue. A terminated session has no
   live composer: check its saved identity/workspace before restore, then
   repeat live readiness checks before continuation.

These reads do not refresh probes, log in, send a task, or kill/restore anything.
Record a failing check and investigate before further input; do not fall back to
another daemon, port, session, or native history.

## Runner journey map

The runner executes the lifecycle sequence; these rows are a checklist, not
independently selectable CLI flags. The prerequisite column states what valid proof
requires, not a new enforcement layer. The runner can collect later evidence
after a functional failure; that failure remains. Missing contracts and other
blocking prerequisites leave dependent gates `NOT_RUN` as documented.

| Journey | Entry and action | Proof prerequisites | Observable proof |
| --- | --- | --- | --- |
| Fresh task | `POST /sessions` creates a TUI worker with the generated initial task. | Native contract and AO preflight meet the applicable strict/diagnostic prerequisites. | Proof JSON is in the AO worktree; initial token handled once; hidden/project token fields match; activity was observed active then settled. |
| Follow-up | `POST /sessions/{sessionId}/send` asks for `secondMessageToken`. | The first proof/settled completion was observed; verify guarded input admission for a separate UI control. | Existing proof gains the expected field and the turn settles. HTTP acceptance alone is insufficient. |
| Cancellation | Send the cancellation task, then deliver `interrupt.input` through the exact `/mux` terminal attachment. | A tokenized foreground-command PID and kernel-start witness exist while the turn is active; session/terminal generation still match. | That process stops, the same terminal settles, and no delayed artifact appears past its scheduled write time. Optional owned-draft cleanup has a separate gate. |
| Kill | `POST /sessions/{sessionId}/kill`. | Record persisted native identity and workspace first; configured draft cleanup must have passed. | Runner observes `isTerminated: true`. Independent child-reaping/control-session proof remains necessary; an acknowledgement alone does not prove kernel cleanup. |
| Exact restore | `POST /sessions/{sessionId}/restore`, then send the generated continuation. | Termination and fresh config readback succeeded; native restore mode, same ID/workspace, and initialized empty composer are verified before continuation. | Same native-ID digest and AO workspace; provider mutates the existing proof, preserves its earlier fields, and recalls the history-only token. |
| Fresh instructions | After confirmed Kill, update `agentRules` through `PUT /projects/{projectId}/config` and verify `GET /projects/{projectId}` before restore. | A new token was absent from initial history; the full expected config was stored/read back without changing other fields. | `restoreHiddenToken` matches after continuation. This is assisted consumption; ordinary-task instruction delivery needs the independent control below. |

Gate definitions and diagnostic prerequisites remain in
[Evidence and handoff](evidence-and-handoff.md#gate-contract). Cancellation,
restored-readiness cues, and optional draft persistence are configured by the
[local CLI contract](local-cli-contract.md#cancellation-and-restored-input).

## Additional user-path and instruction controls

A runner result proves its API/native path, not every AO entry point. Capture the
action and resulting state through the real user path, plus the corresponding
file/process/session evidence. Use stable accessible labels or repository handles;
English labels below come from the current renderer/i18n source, so check the
selected checkout and locale before driving.

| User journey | Actual AO entry point | Additional proof and scope |
| --- | --- | --- |
| Create task | Project board **New task**; choose **Project**, **Agent**, **Model**, title/brief, then **Create task** or **Create as Terminal UI** where offered. | Selected harness/interface launches once in the intended worktree. Exercise this UI path separately; the runner creates sessions directly through the API. |
| Terminal follow-up/cancel | Select the owned session and use its native terminal after the current composer cue; cancellation input comes from the pinned native contract. | Exercise the actual guarded input path, including multiline paste. Observe submission/completion or witnessed cancellation; API-only sends and stale terminal text cannot qualify this path. |
| Terminate and restore | Selected session's **Archive session** control and confirmation; after termination, reopen its archived entry and use **Restore session** where supported. | The topbar archive action invokes termination; reopening alone is not native restore. Correlate actual UI action, exact native identity, readiness and continuation. Preserve an unrelated live control. |
| Chat | Create/select a supported **Chat** session and drive its actual composer and permission controls. | Qualify registered daemon Chat APIs, streaming, approvals, cancellation and reconnect separately. This TUI runner supplies no Chat conformance. |
| Independent instructions | In a disposable registered session, configure a private file action and separate project rule, then submit an ordinary file task that mentions neither private token/path nor reading instructions. | Verify both prescribed effects independently. After confirmed Kill, configure a new private token, read back config, restore the same history, and send another ordinary task. Keep assisted runner gates separate. |

For manual ordinary API follow-ups, include `userAuthored: true` in the
`/sessions/{sessionId}/send` body. The runner currently omits that field; report
its observed delivery separately rather than claiming it exercised user-authored
UI input. See the [independent instruction controls](evidence-and-handoff.md#independent-standing-instruction-controls).

Attach real AO UI captures for every claimed running interface using the required
[capture record](evidence-and-handoff.md#required-ao-ui-evidence). The capture hook
handles failed/blocked gates only. Capture successful sessions separately before
teardown, or retain the owned daemon with `--keep-daemon`. A missing UI capture
leaves `AO UI evidence: BLOCKED` even when functional checks pass.

## Cleanup and evidence survival

Default cleanup kills the runner-created sessions and stops its daemon. Inspect
the finalized `state: finished`, `cleanup[].confirmedTerminated`, daemon stop/exit,
and any run-level errors. `--keep-daemon` intentionally skips this teardown: after
capture, terminate the exact owned session IDs and stop only the recorded owned
daemon/processes, recording the outcome separately. Recheck identities before
signaling processes; never kill by a shared process name.

After either path, confirm `report.json`, `report.md`, and retained issue/log/PNG
artifacts still exist at the named paths. Preserve first failures, later attempts,
and isolated native profiles for review. Scratch data removal must not remove
proof; if evidence lives under the disposable root, preserve it before any later
root cleanup. Failed or externally interrupted runs need exact-process inspection
and cleanup before retrying with a fresh report basename.

## Verify this recipe and keep it current

After changing an operational command or journey, have a fresh reader retrieve
its entry/action/readiness/proof and rehearse one mapped journey end to end on the
authorized host: launch, Doctor, drive, capture, cleanup, then verify evidence
survival. Clean up failed iterations too. Report unexecuted journeys or stand-in
host differences explicitly; a documentation review is not a live rehearsal.

Keep validation levels separate: fixtures check runner behavior, reader checks
check the instructions, and live registered/UI journeys check the integration.
A rehearsal of one journey does not satisfy all mandatory strict gates. Update
this map when routes, controls, readiness or native behavior change; do not append
session reports or version-specific investigation history here.
