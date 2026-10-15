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
report basename outside the Git checkout. Generated reports/captures stay in
untracked host storage and are shared through the integration PR's evidence
comments/attachments, never committed. Ask only for inputs that cannot be
established from the repo or existing authorization. The runner's TUI spawn currently requests
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
| Chat | Follow the [Chat integration checklist](chat-integration.md#chat-verification-checklist); create/select a supported **Chat** session and drive its actual composer and permission controls. | Qualify registered daemon Chat APIs, streaming, approvals, cancellation and reconnect separately. This TUI runner supplies no Chat conformance. |
| Independent instructions | In a disposable registered session, configure a private file action and separate project rule, then submit an ordinary file task that mentions neither private token/path nor reading instructions. | Verify both prescribed effects independently. After confirmed Kill, configure a new private token, read back config, restore the same history, and send another ordinary task. Keep assisted runner gates separate. |

For manual ordinary API follow-ups, include `userAuthored: true` in the
`/sessions/{sessionId}/send` body. The runner currently omits that field; report
its observed delivery separately rather than claiming it exercised user-authored
UI input. See the [independent instruction controls](evidence-and-handoff.md#independent-standing-instruction-controls).

## Mandatory step-by-step screenshots

For each harness, create a checkpoint index **before** driving the real AO UI.
Keep every row below, including unexecuted and unavailable steps. Capture each
meaningful action and resulting state while it happens, before cleanup; one final
running screenshot cannot qualify earlier steps. Use separate images for distinct
states, such as installation before/active/result or archive before/after. One
image may cover two checkpoints only when both required states are actually
visible; link it from both rows. Check current labels in the selected checkout.

| Checkpoint ID | Required real UI captures | Functional proof beside the images |
| --- | --- | --- |
| `harness-listed` | Agent Harness page with the harness row, name/logo and actual install/auth state. | Registered agent ID and selected host; readiness/installer/auth-plan observations from that host. |
| `install-offer` | Before installation: the row's Install/Download action and method, or actual manual-guide/unavailable state. | Advertised installer method/prerequisites and whether installation can start from this AO page. |
| `install-run` | Start installation from that row; capture active progress and completed or failed result. | Correlated install-job ID, status/exit/error logs and native binary/version verification on the selected host. Manual native installation does not prove AO-page download. |
| `auth-before` | After install: Login/Setup/Configure affordance, or Connected/Configured with existing credentials. | Fresh auth/readiness result; distinguish stored configuration, verified authorization and auth-not-applicable. A cached Connected label alone is insufficient. |
| `auth-action` | Start the offered login/configuration from Agent Harness; capture its AO terminal/dialog/guidance and return/result. If it opens an external browser, capture the safe handoff and AO return. | Correlate actual auth/setup plan, completion/error and fresh verification. An external CLI login alone does not prove this entry point. Never capture tokens, passwords, device codes or credential contents. |
| `auth-ready` | Agent Harness row after fresh verification, showing the actual Connected/Configured/error state. | Fresh probe plus the first successful provider turn below; configured-only remains unverified until supported verification succeeds. |
| `harness-models` | Agent Harness model listing/refresh, including loading/error and populated result when available. | Native catalog or documented configured/default-only mode, AO response and refresh outcome. If this panel is planned/absent, capture the actual page and record `NOT_RUN` with `NOT_IMPLEMENTED` reason; this step remains incomplete. |
| `task-agent` | Open **New task** from the board and show the harness visible and selected in Agent. | Registered selectable ID, intended project/host/interface; actual UI creation path, not API-only spawn. |
| `task-model` | Open Model selection and capture offered models and chosen model before submit; show freeform/default-only control if that is the supported mode. | Native/AO catalog comparison or documented model mode, saved choice and effective native model after launch. Do not invent a list for a default-only provider. |
| `task-running` | Submit the task; capture its Kanban card and selected running TUI/Chat interface, including real initial output and settled result. | AO/native identity, workspace, selected/effective model and first completed provider turn with its file/output witness. A card or accepted request alone is insufficient. |
| `task-followup` | Send a normal follow-up through the actual composer; capture submission and response. | Same conversation/workspace, one dispatch and expected effect. Retain independent standing-instruction controls when claimed. |
| `task-cancel` | During a witnessed executing command, capture actual interrupt/Stop action and settled UI. | PID/kernel-start witness, bounded stop/reaping, no delayed side effect and usable continuation. Active-turn cancellation is separate from Kill. |
| `kill-action` | Running session before Kill/archive; actual **Archive session** control and confirmation, then terminated state. | Action/request result, `isTerminated: true`, exact native identity/workspace retained and owned-child cleanup. Use the checkout's actual termination control. |
| `archived` | Archived section with that exact session entry after termination. | Same AO ID and terminated record; archive visibility is not proof of native cleanup. |
| `restore-action` | Open that archived entry; capture **Restore session** action and restored ready state. | Restore result, same native-ID digest/workspace, initialized composer and history continuity; reopening an entry alone is not restore. |
| `kanban-return` | Same restored session card back on the Kanban board; capture selected session after a new continuation completes. | Same AO/native identity, preserved history and expected fresh continuation/instruction effects. A replacement task is not exact restore. |
| `chat` | For supported Chat: actual New Task Chat selection, shared composer, streaming/output/history; capture each applicable approval/rejection, Stop, reconnect and recovery action/result in the linked checklist. | [Chat verification checklist](chat-integration.md#chat-verification-checklist) through registered daemon APIs; TUI proof cannot qualify Chat. Record unsupported Chat explicitly with truthful capability rejection. |
| `reconnect-cleanup` | For claimed reconnect/restart, capture before/after UI and fresh continuation; capture final owned-session cleanup state when the UI remains available. | Identity/history and no duplicate turn after reconnect; separate daemon-recovery proof where claimed; exact owned teardown and evidence survival. If teardown removes the UI, retain final pre-teardown capture and stop/process logs. |

Use [required AO UI evidence](evidence-and-handoff.md#required-ao-ui-evidence)
for the index format, provenance, failure records and attachments. Subdivide a row
into named substeps when multiple actions/states occur; add a checkpoint for every
additional exercised UI action and outcome, including errors and retries.

Already installed or already connected is a valid observed starting state, not
proof of fresh installation or login. Capture that state and mark unexercised
branches `NOT_RUN`. When the agreed journey accepts existing credentials,
`auth-before` and `auth-ready` plus fresh verified authorization and a successful
provider turn qualify that auth route. Keep `auth-action: NOT_RUN` with reason
`EXISTING_VERIFIED_CREDENTIALS`: this unexercised alternative does not block the
accepted route or establish fresh-login proof. Missing authentication/configuration
or an explicit fresh-login claim requires `auth-action`; a cached Connected label
or configured-only state cannot qualify the existing-credential route.

Qualify required missing install/login paths with a separate disposable
installation or authorized isolated account when available. Never uninstall the
user's real agent, delete saved credentials or force logout to create a screenshot.
If a required branch has no safe fixture/account, leave it `BLOCKED` with its
next action.
Auth-not-applicable or unsupported Chat may be `N/A` only with concrete capability
proof and truthful UI behavior. Missing product wiring, an unavailable requested
model panel, or a manual guide cannot become a successful in-page install/login.
For an intentionally manual-only installer/auth plan, record the in-page action
`NOT_RUN` with reason `MANUAL_ONLY`, capture the actual guide/refusal and any
separately exercised manual setup as distinct substeps. It can qualify a limited
manual scope only when explicitly agreed; it cannot satisfy a requested in-page
install/login journey. Model modes and Agent Harness versus New Task are separate
claims: one surface never substitutes for the other.

These are contributor-driven verification gates. The runner is API-first and
does not drive these controls, pause for each checkpoint or capture successes.
Its capture hook handles failed/blocked gates only. Capture success states during
a separate UI journey before teardown; `--keep-daemon` can retain sessions for
later inspection but cannot recover missed installation/login/active-turn states.
Missing required screenshots leave `AO UI evidence: BLOCKED`, regardless of runner
results. Preserve the actual functional status of each step separately.

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
