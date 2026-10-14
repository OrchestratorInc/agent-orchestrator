# Agent harness integration path

Use this checklist as a routing guide. Apply only the sections required by the
requested capability, but do not register a capability before its gate passes.

## 0. Triage intake and existing work

Read `AGENTS.md`, `docs/architecture.md`, `docs/STATUS.md`, and the nearest
current adapter before editing. Resolve the actual upstream identity first:
CLI names, aliases, repository, and package may differ. Search open, closed,
and merged PRs/issues and registered harnesses for all of those names. Record
an existing PR and the precise missing capability before continuing it. Do not
raise a second adapter PR for work already in flight.

Classify each candidate as already supported, existing PR to complete,
suitable new integration, blocked by upstream capability, or out of scope.
Evaluate the requested interface independently: a Chat-only provider need not
invent a TUI command. Missing private instruction input, exact native restore,
or another required capability needs a documented blocker. A deliberately
limited integration requires explicit user scope and truthful capability
rejection; it cannot claim a strict full audit PASS. Supportability depends on
demonstrated capabilities, not popularity or a successful `--help` command.

Use a branch/worktree from current `main` for each independently shippable
integration. Preserve unrelated dirty work. The audit scripts report evidence;
they do not discover upstream releases, generate adapters, or open PRs. Those
steps are contributor work under the user's existing authorization.

## 1. Map the upstream contract

Pin an actual released version and executable fingerprint. Record:

| Concern | Evidence required |
| --- | --- |
| Binary | Executable names, version command, supported operating systems |
| Install | Official installation path and whether AO can offer a safe installer |
| Authentication | Local status probe and the distinction between configured and verified |
| Initial task | Interactive, race-free prompt delivery |
| Argv safety | A task beginning with `-` reaches the provider as data, never as an option |
| Standing instructions | Per-process hidden system/instruction input that preserves provider defaults |
| Permissions | Exact meaning of AO manual, accept-edits, auto, and bypass modes |
| Activity | Native hooks or structured events for active, settled, blocked, and exited |
| Identity | Stable provider-native session/conversation ID |
| Restore | Explicit restore by that ID; never "latest" or best-match selection |
| Cancellation | Bounded turn cancellation and process/session termination behavior |
| Models | Catalog, mode list, or truthful free-form model input |
| Chat | Structured protocol, session load, replay, permissions, and cancellation |

Prefer an executable live-conformance test behind an explicit environment
variable. It must use temporary AO/workspace data and a disposable provider
profile, assert which files changed, and never require real credentials for the
default unit suite.

When a live Chat test launches AO's detached persistent host through
`os.Executable()`, remember that `go test` resolves that path to the package test
binary. The test binary needs a `TestMain` `chat-host` dispatch path, and the
start/resume configs need an absolute isolated `DataDir`; otherwise missing
`host.json` is a test-host failure, not a provider failure.

## 2. Design the delivery slices

Use separate issues and PRs when the scopes are independently shippable:

1. Upstream contract and conformance gate.
2. TUI worker/orchestrator adapter plus production registration.
3. Structured Chat driver after independent protocol conformance.
4. Interface handoff only after both interfaces prove shared native identity and
   history.
5. Reviewer support or nested-agent visibility only under separate designs.

An unproven adapter may exist in an unregistered package for development. It must
not appear in `domain.AllHarnesses`, the production registry, SQLite constraints,
API enums, or product pickers until its required gates pass.

## 3. Implement the TUI adapter

Create `backend/internal/adapters/agent/<harness>/` following the closest current
adapter. Typical files are:

```text
<harness>.go
<harness>_test.go
install.go
install_test.go
auth.go
auth_test.go
hooks.go
hooks_test.go
activity.go
activity_test.go
```

Cover the following behavior with table-driven tests:

- manifest identity and the `agent` capability;
- binary resolution, including Windows shims when supported;
- fresh launch argv and prompt-delivery strategy;
- leading-dash prompt delivery through the real installed executable;
- exact restore argv and missing/invalid native IDs;
- model and permission mappings, rejecting unsupported modes;
- environment propagation without secret logging;
- cancellation, exit detection, and native detached-process cleanup when needed;
- `SessionInfo` native-ID discovery;
- hook installation, idempotence, preservation, permissions, and cleanup footprint;
- activity derivation for startup, active, blocked, settled, and exited states;
- authentication timeout, malformed output, cancellation, and configured-versus-authorized semantics.

AO standing instructions are private configuration produced by the session
manager. Never concatenate them into the visible initial task. Restore must
reapply them because provider history may not retain launch configuration.
Creating a provider rule/plugin file is configuration evidence only. Require a
unique audit-only token in a provider-authored proof artifact independently in
TUI and Chat. That establishes instruction consumption, not resistance to
model echo; never place a real secret in the instruction probe.

Qualify effective native settings, not merely accepted flags: prove model/provider
selection, permission behavior, workspace writes, and instruction consumption.
Verify edits land in the actual AO worktree and leave the source checkout unchanged.
Preserve native defaults, user hooks/rules, tools, and credential filtering.
Reject unsupported configuration variants rather than silently replacing defaults.
Native hooks may initialize lazily or fail open; distinguish an observed context
witness from a pre-execution security boundary. Bound the combined hook payload
and preserve exact instruction text without truncation.

Keep process/runtime I/O, bounded polling, exclusive input admission, publication,
and rollback in the manager; keep native command/history interpretation in the
adapter. Resume must validate durable identity/workspace/history and positively
observe the loaded target. Reject silent fresh-session fallback and stale launch
signals. Never fabricate native IDs or replay the initial task as restore.
Keep startup callbacks nonblocking, and check daemon-startup adoption
as well as direct resume callers.

Use current rendered terminal cues for adapter input admission. Serialize writes
and guard pending submissions, drafts, and permission decisions; stale output or
an echoed prompt must not authorize a send. Qualify long multiline input through
the actual AO path. Process ownership alone does not establish the current native
conversation after native new-session or compaction operations.

Any worktree file created by `GetAgentHooks` must be covered by the AO-managed
sibling `.gitignore`, as enforced by the agent registry tests. Prefer AO data
storage when the provider supports an explicit external hook/config path.

## 4. Add readiness and model support

Expose a truthful `GetConfigSpec` containing a model or mode field. Extend the
model catalog only as far as upstream evidence supports:

- Use a provider-reported catalog when it is stable and bounded.
- Use configured-only selection when custom models must first be added upstream.
- Use direct text input when the CLI accepts a model ID but has no discoverable
  catalog.
- Do not claim authorization from credential-file or environment-variable
  presence alone; that is `configured` unless a safe provider round trip proves
  it valid.

Trace model selection through `modelcatalog/catalog.go` (discovery source and
custom-entry policy), applicable `daemon.go` discovery callbacks, the model API,
New Task/project defaults, and effective native selection. `GetConfigSpec` or
in-session ACP options alone do not establish pre-launch picker support. Verify
the chosen catalog/configured/text mode and its fallback; preserve an intentional
default-only scope explicitly. A provider without discovery need not invent a
catalog. Its strict runner catalog gates remain non-pass; record separately
qualified product behavior without upgrading the runner verdict.

Check readiness on each advertised OS before launch: binary, required config,
supported permissions and safe auth state. Unsupported OS or missing/invalid
required config must surface a useful blocker before the user starts a task.
For setup, trace the actual Settings target/plan registration and linked user
guide; an API enum alone does not add onboarding. If automatic installation is
unsuitable, provide the official manual path and prerequisites.

Add installation metadata under `backend/internal/service/systeminstall/` only
for official, supportable installation methods. Never silently execute a remote
shell script merely because the vendor documentation presents one.

## 5. Register the proven TUI harness

After conformance and adapter tests pass:

1. Add `domain.Harness<Name>` and one entry in `domain.AllHarnesses`.
2. Add one constructor in `backend/internal/adapters/agent/registry/registry.go`.
3. Allocate the next migration number at implementation time and widen only the
   current `sessions.harness` CHECK constraint, with a tested inverse migration.
4. Update the burned-migration ledger and migration tests.
5. Update every relevant worker/orchestrator/session/delegation enum in
   `backend/internal/httpd/controllers/dto.go` without touching reviewer enums.
6. Run `npm run api` and commit both generated artifacts.
7. Add the identity and label to `packages/product-ui/src/agents.ts`.
8. Add a licensed upstream brand asset and renderer avatar mapping when available.
9. Verify inventory, settings, project defaults, task creation, CLI spawn, restore,
   kill, cleanup, and daemon restart behavior.

For branding, record the upstream asset source/license and verify the avatar in
the claimed UI, or document why the existing initials fallback is intentional.

Do not infer that a TUI harness supports Chat. The Chat registry is the explicit
capability gate.

## 6. Add Chat only when proven

Use the [ACP-to-shared-Chat-UI guide](chat-integration.md) for exact source entry
points, frontend reuse decisions, and the focused Chat verification checklist.
A new agent normally reuses the existing AO Chat screen; provider protocol and
lifecycle behavior stay in the daemon.

For ACP, reuse `backend/internal/adapters/chatdriver/nativeacp` and the existing
persistent-host architecture. A provider-specific wrapper should own only binary
configuration and truthful capabilities.

Before production Chat registration, prove with the real pinned executable:

- initialize and authentication behavior;
- `session/new` and deterministic `session/load`;
- provider conversation ID stability;
- text/reasoning/tool/plan streaming;
- permission approve, reject, and cancellation behavior;
- model/configuration options;
- process restart and daemon host reconnection;
- history replay without duplicate messages;
- typed failure on missing history or workspace mismatch;
- accurate attachment, MCP, compaction, steering, and structured-input claims.

For kill/restore validation, do not accept the restore response's immediate
`idle` as a provider signal. Wait for provider readiness, send a unique
continuation, and verify that the provider appends that token to the pre-kill
proof file before declaring continuity.

Register the driver only after all required cases pass. Do not implement
`ports.AgentInterfaceHandoff` until TUI and Chat are proven to share identity,
history, permission state, and cancellation semantics. Chat qualification uses
actual registered daemon Chat sessions and controller states, not the TUI runner.

## 7. Verification

Honor the user's execution host: when local checks are prohibited, run checks
on the authorized VPS and GitHub CI. Do not silently run builds, formatting,
lint, fixtures, or provider turns on the workstation. Keep source/binary hashes,
pinned runtime versions, failed attempts, and the exact tested commit in the
report. Read the current `AGENTS.md` and workflow files for the required suites.

Use the [operating recipe and journey map](audit-journeys.md) to plan API/native
checks, actual UI controls, independent instruction proof, and evidence-preserving
teardown.

Run the narrowest affected packages first. The complete local gate for a
production harness normally includes:

```bash
cd backend && go test ./...
cd backend && go test -race ./...
cd backend && go vet ./...
npm run api
npm run lint
npm run frontend:typecheck
cd frontend && npm run build
```

Run workflow emulation only when allowed by the user and supported by the
execution host. After pushing, inspect all required GitHub checks on the current
PR head and report failures or unavailable OS/authenticated-provider coverage
precisely. Do not publish or deploy as validation. A native source patch or
prerelease artifact is not conformance of an unchanged released CLI.

Exercise executing-tool cancellation and AO Kill independently. Record the owned
PID and kernel start identity, bounded child reaping, delayed side-effect absence,
and an unrelated usable control session. Distinguish native cancellation latency
from AO's status-observation cadence and report OS coverage explicitly.

Use the [independent instruction controls](evidence-and-handoff.md#independent-standing-instruction-controls)
when claiming ordinary-task instruction delivery. Runner token prompts are assisted
consumption checks; configured context and token consumption do not establish the
authenticated provider's message role.

For every harness claimed as running inside AO, capture the actual AO session
UI on the tested build, even when the integration changes only backend code.
Follow the [required AO UI evidence](evidence-and-handoff.md#required-ao-ui-evidence)
contract: attach real screenshots for each claimed interface, correlate them
with session/native identity and functional logs, and retain clearly labelled
failures. Native CLI and terminal-mirror captures do not fulfill this gate.

For UI changes, use `ao preview` from the session or the isolated real-Electron
desktop lab described in `AGENTS.md`. Never use the invoking checkout with real AO
data for desktop-lab verification.

Before handoff, audit the diff for accidental reviewer changes, generated drift,
edits to old migrations, provider-profile writes, credentials, temporary data,
and unsupported capability claims.

## 8. Report and publish the integration

Use `.agents/skills/pr-description/SKILL.md` for the verified change-count header.
Explain supported interfaces and permissions, pinned native version, exact
restore identity, intentional omissions, upstream/runner/adapter failures, and
validation scope. Link the existing PR and issue when continuing work. Preserve
failed attempts separately from the final run; a later passing fixture or live
attempt does not erase a real failed reproduction.

A useful report separates:

- strict audit status from individual functional lifecycle results;
- adapter defects from fixture/launcher defects and upstream limitations;
- native provider response from AO observation latency;
- stored/configured credentials from verified provider authorization;
- actual AO UI screenshots from native-only terminals, mirrors, and later reproductions.

For each claimed running harness, embed its real AO UI screenshot in the PR
report/comment with the required capture record and durable GitHub-renderable
URL. A passing fixture suite or runner result cannot fill this slot. Missing
AO UI capture or attachment means `AO UI evidence: BLOCKED` and an incomplete
integration handoff. List every non-pass gate separately with its actual failure
screenshot, or `screenshot: unavailable` and the reason; keep the first failed
attempt visible even after a successful fix.

Publish the PR/report when requested or already authorized. Do not send reports
to unrelated channels or merge/publish releases as part of integration testing.

## Completion matrix

Include one matrix in the integration PR's current-status section before handoff.
Use columns **Area | Supported scope | Verification | Proof or reason**.
Keep every row; scope an excluded interface explicitly rather than deleting it.

| Area | Required disposition |
| --- | --- |
| Registration and UI | Trace adapter/driver, registry, API/generated contracts and claimed task/settings surfaces |
| Models | Catalog/configured/text/default-only mode; pre-launch picker/defaults and effective native selection; in-session options separately |
| Setup | Official installer or manual guide, actual Settings plan and prerequisites |
| Readiness | Advertised OS, binary, required config, safe auth state and actionable pre-launch failures |
| Identity and logo | Product label, licensed asset/source and avatar, or explicit fallback reason |
| TUI and Chat | Each interface's supported scope and functional/UI evidence; reject excluded surfaces truthfully |
| Permissions and instructions | Effective supported modes and private instruction delivery/consumption scope |
| Lifecycle | Active-turn cancel, Kill, exact restore, reconnect/restart and cleanup as claimed |
| Evidence | Tested source/binaries, real AO UI captures, distinct attempts and reproduction receipt |
| CI and handoff | Current head checks, merge/conflict state, unresolved gaps and retained-data cleanup |

Verification is `PASS`, `FAIL`, `BLOCKED`, `NOT_RUN`, or `N/A`, with linked proof
or a concrete reason. `N/A` requires an upstream limitation or explicitly agreed
scope and truthful capability rejection; unfinished supported wiring stays
`FAIL`/`BLOCKED`/`NOT_RUN`. An intentional licensed-asset fallback is a recorded
branding disposition, not permission to omit its row.

Resolve failures within the authorized scope and reverify affected paths. If an
external blocker remains, hand off the matrix as incomplete with the next action;
do not call the integration fully verified. A limited-scope integration may be
complete within that agreed scope but cannot claim a full strict runner PASS.
Use the [current-status receipt](evidence-and-handoff.md#current-status-and-reproduction-receipt)
to keep historical proof separate from the current head. This matrix requires
contributor review; the audit scripts do not generate or enforce it.
