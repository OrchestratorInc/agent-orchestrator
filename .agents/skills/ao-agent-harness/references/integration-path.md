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
A script/IDE-only product without a stable interactive CLI, private instruction
input, or exact native restore needs a documented blocker, not speculative
production registration. Supportability depends on demonstrated capabilities,
not popularity or a successful `--help` command.

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

Do not infer that a TUI harness supports Chat. The Chat registry is the explicit
capability gate.

## 6. Add Chat only when proven

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
history, permission state, and cancellation semantics.

## 7. Verification

Honor the user's execution host: when local checks are prohibited, run checks
on the authorized VPS and GitHub CI. Do not silently run builds, formatting,
lint, fixtures, or provider turns on the workstation. Keep source/binary hashes,
pinned runtime versions, failed attempts, and the exact tested commit in the
report. Read the current `AGENTS.md` and workflow files for the required suites.

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
precisely. Do not publish or deploy as validation.

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


## Supported native initialization and subprocess boundaries

When argv resume is broken but the native TUI has a supported interactive resume
command, qualify that path with native controls before registering it. Validate
full native identity and real nonempty supported history before launch. Keep
runtime I/O, bounded polling, exclusive input admission, publication and owned
rollback in the manager; keep native command/history/terminal interpretation in
the adapter. Do not label a taskless fresh launch as successful native resume.
Require a current-launch witness naming the exact target, real loaded messages,
and the initialized empty composer after the single resume command. A frontend
banner alone can lie after native fallback. Ignore stale generations/sequences.
Use the existing current rendered-viewport runtime boundary for terminal cues;
raw output-ring tails can omit unterminated current rows and retain overwritten
status frames. Fail closed when rendered evidence is unavailable.
Never publish the transient fresh ID, and never replay the saved task as restore
initialization. Check every direct resume caller and daemon-startup adoption path.

Native startup hooks must return promptly. If AO parks callbacks until launch
publication, use a local generation-scoped witness during initialization; a
synchronous startup callback can deadlock. Publish the verified target afterward
and read it back because stale publication signals can be silently discarded.
Distinguish native conversation-end during interactive resume from process exit.
If native hook stdout is capped per hook, preserve private instructions through
ordered bounded chunks only when the native trim/join rule reproduces the original
context exactly. Split at compatible paragraph boundaries, check native combination,
and reject oversized indivisible segments or altered whitespace; never truncate.

A visible Cancelled label is insufficient for an executing tool. Wait for a
child-authored start marker, retain owned PID plus kernel start identity, then
cancel. Check process reaping, composer recovery and delayed side effects across
the scheduled deadline. Exercise AO Kill independently, with an unrelated live
control whose state/generation are rechecked immediately and later. Keep native
history, cross-session isolation and exact restore as separate assertions.

Prefer supported native SDK/plugin APIs that propagate cancellation into the
official sandbox while preserving tool schemas/errors/permissions. Prove real
SDK initialization order and same-name replacement. Do not add a tool to narrowed
children or overwrite unsupported custom tools. Check saved custom subagents,
sandboxes/root IDs/session managers and system-prompt overrides as well as argv.
An initial foreground flag does not constrain model-driven native reconfiguration;
restrict that route through a supported capability boundary and verify public
witnesses at the lifecycle phase where they exist. Document explicit human
in-TUI profile/plugin takeover separately. Do not claim managed guarantees survive
unsupported takeover or rely on private monkeypatches/terminal interception.

Trace actual native provider/model precedence. A parsed flag or matching one
saved field does not prove the TUI honored an AO override. Reject overrides when
the effective selection cannot be proved, and qualify the native configured model
with real provider/UI evidence. Native decision cards may suppress idle/spinner
chrome; detect the newest current decision cue regardless of selected option,
without letting earlier idle/spinner scrollback hide it. Prove ordinary AO input
is refused while a decision is pending and only positively observed native
resolution clears it.

Measure native settlement separately from AO observation. Continuous terminal
classification may still run on the observer's normal ticker rather than emit
an event immediately. Inspect the actual daemon wiring/tick before choosing a
confirmation bound; retain a stricter failed probe unchanged. Record current
rendered-native and AO-state UTC/generation samples, distinguish fast owned tool
reaping from delayed AO idle/blocked publication, and do not present a longer
bounded confirmation as an instantaneous or guaranteed status transition.

## Scoped native terminal contracts

A supported additive per-process instruction route can preserve native defaults
without a literal append-file flag. Qualify the actual selected interface: Hermes
v0.21.6 classic `--cli` consumes its ephemeral environment prompt, while the modern
TUI and ACP do not establish that same contract. Compose effective personality or
prefix settings rather than replacing native safety with an explicit system flag.
Fail closed for configuration variants whose effective instructions cannot be
preserved. A model may refuse to disclose private instruction text; a public effect
whose operands occur only in standing instructions can prove consumption without
weakening that policy.

With explicit user-authorized scope, a useful terminal adapter may deliberately
refuse unsupported native restore, Chat, handoff and history surfaces. OpenJarvis
CLI1.0.4 chat has process-local history. Do not manufacture native IDs or replay
messages as restore. This narrow working surface does not qualify a strict full
audit PASS.

Native plain-line REPLs need current rendered input admission, serialized writes,
and a pending-submission fence with baseline-aware exact echo evidence. Physical
wrapping and Unicode controls matter. Reject unsupported inputs before writing;
historical prompts or an embedded composer marker must never authorize a new send
or answer a later permission question. Missing current composer evidence remains
unknown even if a separate UI still displays a prompt.

A process lease may establish ownership without identifying its current native
conversation. Hermes can retain its startup lease ID after `/new` or compression;
use the actual process's PTY-bound breadcrumb with ownership/cwd corroboration.
Mutable stopped-generation breadcrumbs can be overwritten by PTY reuse: refuse
unprovable restore rather than selecting an older conversation.

Native tool environment filtering can drop AO routing variables even when the
correct `ao` binary is on PATH. Append bounded instructions for the native tool's
explicit AO-variable passthrough, preserve credential filtering, and verify a
report receipt in the intended daemon. Do not infer success from model narration.

A native `/new` conversation may have a NULL persisted workspace even after successful coding. Corroborate live identity through the owned process and current native PTY breadcrumb, but refuse persisted restore without its required durable workspace. Preserve cursor-bearing rows when a normal terminal shrinks; never recover readiness by replaying ring history. This vertical preservation does not prove complete width reflow. Explicitly include the actual AO worktree in additive instructions when native tools otherwise default to a configured project directory.


## Native ACP Chat qualification lessons (Docker Agent)

A Chat-only provider may still require a common agent metadata plugin for AO's
binary/auth/config inventory. Reject terminal admission before allocating a
session, default omitted mode to Chat, and prevent fallback to terminal. Do not
invent a TUI command or use a permissive permission default to satisfy an interface.

Private additive native hooks can carry standing context while preserving user
hooks and tools. Include the actual AO workspace explicitly: native cwd isolation
alone does not prevent an agent from following a source-repository path elsewhere
in AO context. Prove the write landed in the session worktree and the source
checkout stayed unchanged. Preserve the first wrong-workspace failure separately.

Use the daemon's actual Chat enums when observing settlement (controller `ready`,
with no running or queued turns); terminal activity and Chat controller states are
different contracts. A local credentials inventory does not verify remote access.
Keep provider token-exhaustion and observer errors visible instead of treating
those attempts as lifecycle passes. Qualify ACP through real AO APIs, not the
TUI-only audit runner. Record detached-host and native-child PID/kernel start times
before daemon replacement, Kill, and exact restore.


## Native Mastra TUI lessons

Mastra0.45.0 discards hook additionalContext in its TUI; use its additive native
plugin instructions and preserve plugin/hook registries and disabledPlugins.
SessionStart can expose bootstrap session-init; verify restored identity with the
supported active-session plugin getter. Prevalidate history before resume because
missing/locked resume may silently create a new thread. Environment permission
flags alone need live confirmation: code-sdk1.11.0 defaults yolo=true if native
preferences are absent. Set supported yolo=false in an owned profile and reject
explicit permissive preferences unless bypass was requested. Protect current
rendered drafts/permissions with serialized admission. Native optional embedding
initialization can outlive a turn; do not infer prompt readiness or cancellation
from an old raw output ring or describe embedding/background ownership as proven.


## Pool compatible-provider qualification hold

Pool v1.0.16 initializes ACP and creates standalone sessions without a successful
model call. With the available authorized Z.ai coding endpoint, it emitted
`/v4/v1/chat/completions` and failed with 404; explicitly adding `/v1` did not
repair routing. A direct `/v4/chat/completions` provider control succeeded, but
that control is not native Pool or AO conformance. Do not convert initialize,
session/new, empty authMethods or credential presence into authorization.
Unknown native history hard-failed; valid load/replay remains unproved.

Keep Pool unregistered until a released native provider-path fix or an already
authorized working endpoint enables full qualification. Unnamed SessionStart
hooks are additive, but fail open and cap model-visible context at 32 KiB.
Bound serialized output as well as raw instruction bytes (JSON escaping grows
payloads); actual hook invocation, consumption and failure detection remain
required. Source-only helper construction must never count as instruction proof.
See the [native hold report](../../../../docs/research/pool-acp-hold-2026-10-11.md).


## Nested instruction discovery is not global delivery

Freebuff0.2.26 persisted nested AGENTS.md in knowledgeFiles and could read it when
a visible task requested standing context, yet an ordinary fresh task did not
apply its mandatory private receipt instruction. Keep those controls separate:
file-tree inclusion or saved metadata alone does not prove private standing
instructions independently of user input. Qualify a fresh task that never names
the private path/token/receipt and verify the privately required side artifact.
Do not change root/user AGENTS.md or replace native bundled agents to force it.
