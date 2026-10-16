# Agent harness integration path

For a full harness integration, evaluate every area below: TUI, Chat, reviewer,
AO installation/login/configuration, readiness, models/providers, branding and
lifecycle. Implement each supported capability; do not stop after the first
working interface. A narrower explicit user request scopes implementation, not
truthful reporting. Do not register a capability before its gate passes.

## 0. Triage intake and existing work

Read `AGENTS.md`, `docs/architecture.md`, `docs/STATUS.md`, and the nearest
current adapter before editing. Resolve the actual upstream identity first:
CLI names, aliases, repository, and package may differ. Search open, closed,
and merged PRs/issues and registered harnesses for all of those names. Record
an existing PR and the precise missing capability before continuing it. Do not
raise a second adapter PR for work already in flight.

Classify each candidate as already supported, existing PR to complete,
suitable new integration, blocked by upstream capability, or out of scope.
Before editing, prepare a capability plan: **Area | Native support/source |
Existing AO support/gap | Implementation/linked PR | Verification**. Publish it
in the evidence comment once the integration PR exists and publication is
authorized.
Keep separate rows for TUI, Chat, reviewer, install, login/setup, model discovery,
model visibility controls and provider management. Unsupported upstream behavior
needs pinned proof; an absent AO control for a supported behavior is unfinished
integration work, not an upstream limitation. Carry every row into the final
completion matrix, including non-pass and explicitly excluded scope.
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
| Authentication | Native login/setup/status/logout capabilities, safe AO entry point and configured versus verified authorization |
| Initial task | Interactive, race-free prompt delivery |
| Argv safety | A task beginning with `-` reaches the provider as data, never as an option |
| Standing instructions | Per-process hidden system/instruction input that preserves provider defaults |
| Permissions | Exact meaning of AO manual, accept-edits, auto, and bypass modes |
| Activity | Native hooks or structured events for active, settled, blocked, and exited |
| Identity | Stable provider-native session/conversation ID |
| Restore | Explicit restore by that ID; never "latest" or best-match selection |
| Cancellation | Bounded turn cancellation and process/session termination behavior |
| Models | Catalog/configured/mode/free-form/default-only behavior, stable IDs and effective selection |
| Providers | Explicit provider metadata, account/configuration ownership and supported add/change/remove flows |
| Reviewer | Native one-shot review or qualified prompt-driven review, target/head identity, result/submission and cancellation |
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
5. Qualified reviewer adapter/registration and actual AO review journey.
6. Nested-agent visibility only when separately supported and requested.

Separate delivery slices must remain linked in the capability plan. A separate
reviewer PR is not permission to silently omit reviewer evaluation or call an
unfinished full integration complete.

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

### AO login, model controls and providers

Wire native authentication through the actual `service/agentauth` plan and Agent
Harness Account UI. Where native login/setup exists, users must be able to start
it from AO, complete its terminal/browser/configuration flow and return to a
freshly checked account state. Check the actual action for unknown, unauthorized,
configured and authorized states; an unknown state must not hide the supported
setup action. Verify retry/error handling and supported account changes/logout
in isolated profiles. A guide or an external terminal login alone does not prove
AO-initiated login. Native manual-only limitations remain explicitly scoped;
unimplemented AO wiring is a gap to fix. Never log credentials or bypass the
existing credential boundary to build a new secret-storage scheme.

For models, trace discovery/configuration through the daemon catalog and both
Agent Harness Models and New Task. Reuse the checkout's shared catalog/provider
and picker-preference paths (for example `HarnessModelsPanel`, `useOfferedModels`
and daemon `harnessDefaults.disabledModels`); inspect current source names first.
Verify model on/off, bulk controls where offered, save/readback, reload and error
states, host/harness isolation, and effects in New Task/project/Chat/reviewer
pickers where applicable. Preserve the existing last-enabled-model and
already-selected-model rules. These are picker visibility preferences, not a
provider permission boundary or a promise that direct API launch is forbidden.
A default-only/free-form provider needs its truthful supported control; it must
not acquire a fabricated catalog or meaningless switches.

Trace explicit provider metadata and configured-account state through the native
config/catalog, daemon and shared AO UI; model namespace text is not proof of a
provider. Verify browsing/switching providers, corresponding models and effective
native routing. Assess supported add/edit/remove/account configuration and expose
it through AO's existing setup/configuration path where applicable, preserving
other providers and profiles. A configured badge or provider dropdown alone does
not establish provider login or configuration management. Record each operation
as implemented/verified, unsupported with native proof, or an AO gap; absence of
an existing generic provider editor does not make a supported required operation
`N/A`. Recheck affected auth/catalog/cache state after changes and retain secrets
only in the established credential/configuration boundary.

## 5. Register the proven TUI harness

After conformance and adapter tests pass:

1. Add `domain.Harness<Name>` and one entry in `domain.AllHarnesses`.
2. Add one constructor in `backend/internal/adapters/agent/registry/registry.go`.
3. Allocate the next migration number at implementation time and widen only the
   current `sessions.harness` CHECK constraint, with a tested inverse migration.
4. Update the burned-migration ledger and migration tests.
5. Update every relevant worker/orchestrator/session/delegation enum in
   `backend/internal/httpd/controllers/dto.go`; widen reviewer enums only through
   the separately qualified reviewer path below.
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

Use temporary registration in an isolated candidate build to exercise the
registered AO cases. Enable production registration only after those cases pass. Do not implement
`ports.AgentInterfaceHandoff` until TUI and Chat are proven to share identity,
history, permission state, and cancellation semantics. Chat qualification uses
actual registered daemon Chat sessions and controller states, not the TUI runner.

### Reviewer integration when supported

Assess reviewer suitability independently of TUI/Chat. Follow
`ports/reviewer.go`, `adapters/reviewer/<harness>/`,
`adapters/reviewer/registry.go`, `domain/reviewerharness.go` and the current
`internal/review` service, reviewer DTOs and shared `ReviewerSelect`/review pane.
Implement `ports.Reviewer` with real target/head/workspace identity, correct
instructions/configuration, result submission and explicit failure behavior.
Register in the reviewer vocabulary/registry and relevant API enums only after
qualification; regenerate contracts when changed. Worker registration does not
register a reviewer. Use optional Chat, cancellation, restoration and reuse
interfaces only when their actual semantics are supported and verified.

Exercise the AO reviewer picker and review trigger against a disposable target:
selected harness/model/permissions, actual review start, reviewer pane output,
result/verdict submitted to the correct run/head once, cancellation/error and
rerun behavior. Verify worker isolation and no unintended checkout changes;
reviewer IDs are distinct from the worker session ID. A worker asked to review
in a normal task does not prove the AO reviewer integration. Publish an external
GitHub review only within existing authorization for that target/action. Keep
reviewer scope, screenshots and functional receipts separate from worker/Chat
proof. Unsupported review remains explicit with pinned capability evidence;
supported but unfinished reviewer wiring keeps full integration incomplete.

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
contract and [step-by-step checklist](audit-journeys.md#mandatory-step-by-step-screenshots):
plan capture before onboarding and retain each action/result from Agent Harness
listing/install/auth and both model surfaces through New Task, running, Kill,
archive, exact restore and Kanban return, plus supported Chat. Correlate images
with identities and functional logs and retain clearly labelled failures. Native
CLI and terminal-mirror captures do not fulfill this gate.

For UI changes, use `ao preview` from the session or the isolated real-Electron
desktop lab described in `AGENTS.md`. Never use the invoking checkout with real AO
data for desktop-lab verification.

Before handoff, audit the diff for unqualified reviewer registration, generated drift,
edits to old migrations, provider-profile writes, credentials, temporary data,
and unsupported capability claims.

## 8. Report and publish the integration

Use `.agents/skills/pr-description/SKILL.md` for the verified change-count header.
Explain supported interfaces and permissions, pinned native version, exact
restore identity, intentional omissions, upstream/runner/adapter failures, and
validation scope. Link the existing PR and issue when continuing work. Preserve
failed attempts separately from the final run; a later passing fixture or live
attempt does not erase a real failed reproduction.

Keep the PR description concise: source behavior, validation summary and links
to its evidence comments. Publish detailed reports, checkpoint indexes, matrices,
logs/receipts and images as PR comments/attachments; never commit per-run evidence
files. Follow [comment-only evidence ownership](evidence-and-handoff.md#evidence-ownership-by-integration-pr).

A useful evidence comment separates:

- strict audit status from individual functional lifecycle results;
- adapter defects from fixture/launcher defects and upstream limitations;
- native provider response from AO observation latency;
- stored/configured credentials from verified provider authorization;
- actual AO UI screenshots from native-only terminals, mirrors, and later reproductions.

For each harness, publish the complete checkpoint index in a PR evidence comment
and embed real AO UI screenshots in journey order, with required capture records
and durable GitHub attachment URLs. A passing fixture suite, runner result or final running
image cannot fill missing earlier steps. Missing AO UI capture or attachment
means `AO UI evidence: BLOCKED` and an incomplete
integration handoff. List every non-pass gate separately with its actual failure
screenshot, or `screenshot: unavailable` and the reason; keep the first failed
attempt visible even after a successful fix.

Publish the PR/report when requested or already authorized. Do not send reports
to unrelated channels or merge/publish releases as part of integration testing.

## Completion matrix

Include one matrix in the integration PR's current-status evidence comment before
handoff; link that comment from the PR description.
Use columns **Area | Supported scope | Verification | Proof or reason**.
Keep every row; scope an excluded interface explicitly rather than deleting it.

| Area | Required disposition |
| --- | --- |
| Registration and UI | Trace adapter/driver, registry, API/generated contracts and claimed task/settings surfaces |
| Models | Catalog/configured/text/default-only mode; both model surfaces; on/off/bulk persistence and applicable picker effects; effective native selection; explicit unimplemented controls |
| Providers | Native provider/account support, explicit metadata/grouping, configured versus verified state, AO setup/configuration operations and effective routing; unsupported operations versus unfinished AO wiring |
| Setup | Official installation and AO-initiated login/setup with Account action/error/retry/readback; supported logout/account changes; manual-only limits and unexercised branches explicit |
| Readiness | Advertised OS, binary, required config, safe auth state and actionable pre-launch failures |
| Identity and logo | Product label, licensed asset/source and avatar, or explicit fallback reason |
| TUI and Chat | Each interface's supported scope and functional/UI evidence; reject excluded surfaces truthfully |
| Reviewer | Native suitability, reviewer adapter/vocabulary/picker, actual AO review run/result/cancel/rerun and its own screenshots/receipts; unsupported versus unfinished scope explicit |
| Permissions and instructions | Effective supported modes and private instruction delivery/consumption scope |
| Lifecycle | Active-turn cancel, Kill, archived entry, exact restore and same card back in Kanban with continuation; action/result captures and functional proof; reconnect/restart and cleanup as claimed |
| Evidence | Tested source/binaries, complete screenshot checkpoint index with action/result and failure logs, distinct attempts and reproduction receipt |
| CI and handoff | Current head checks, merge/conflict state, unresolved gaps and retained-data cleanup |

Verification is `PASS`, `FAIL`, `BLOCKED`, `NOT_RUN`, or `N/A`, with linked proof
or a concrete reason. `N/A` requires an upstream limitation or explicitly agreed
scope and truthful capability rejection; unfinished supported wiring stays
`FAIL`/`BLOCKED`/`NOT_RUN`. An intentional licensed-asset fallback is a recorded
branding disposition, not permission to omit its row.

Resolve failures within the authorized scope and reverify affected paths. If an
external blocker remains, hand off the matrix as incomplete with the next action;
do not call the integration fully verified. A limited-scope integration may be
complete within that agreed scope but cannot claim verification outside it.
Report the runner's actual verdict separately: a strict TUI runner PASS does not
qualify Chat, onboarding or the complete UI journey.
Use the [current-status receipt](evidence-and-handoff.md#current-status-and-reproduction-receipt)
to keep historical proof separate from the current head. This matrix requires
contributor review; the audit scripts do not generate or enforce it.
