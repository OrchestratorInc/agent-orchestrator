# Accounts Manager: incremental implementation plan

Date: 2026-09-27. Status: runner privacy and validation follow-up implemented locally, with reviews between changes; M0 integration feasibility remains in progress. See section 11 for execution evidence.

Product contract: [Accounts Manager specification](2026-09-26-accounts-manager-spec.md). This plan supplies implementation order, transaction boundaries, verification, and review findings. The user subsequently requested implementation with reviews between increments. No changes have been published.

## 1. Decision and delivery order

Build on the runner and management layer in PR #5769. Keep CLIProxyAPI pinned to the reviewed version and use its extension points. AO owns user choices, account lifecycle operations, session bindings, and the interface. The library supplies supported authentication and request transport.

The required outcome is session 1 using account A while session 2 uses account B. The user can switch either session independently. No account ranking, automatic fallback, native credential migration, or global account replacement belongs in this work.

| Increment | Deliverable | Dependency and exit evidence |
| --- | --- | --- |
| M0 | Verified integration baseline and feasibility checks | Establish protected paths, supported modes, credential-store extension feasibility, and native coexistence before feature implementation. |
| M1 | Isolated account lifecycle | M0 passes. Add, cancel, reconnect, refresh, disable, and unbound removal survive faults without touching native credentials. |
| M2 | User-selected per-session routing | M1 passes. Concurrent A/B sessions, launch, restore, and route revocation enforce the selected account. |
| M3 | Transactional account switching | M2 passes. Idle and busy switching, recovery, and removal of in-use accounts have verified session-scoped behavior. |
| M4 | Complete user workflows | M3 passes. Separate Accounts page and two-action saved-account switch work in the real desktop app. |
| M5 | Regression, speed, and release evidence | All earlier gates pass. Full relevant checks, platform packaging, real desktop evidence, and latency measurements establish readiness. |

Continue the same PR through focused increments. Each increment includes its own tests and a review checkpoint. Do not accumulate all verification at M5. Keep incomplete capabilities unavailable to users. A reviewed increment is not a promise that the whole feature is ready to merge. Independent fixes to the existing runner's validation may land locally during M0 without enabling credential or session capabilities whose feasibility gates remain open.

## 2. Baseline and immutable boundaries

Three snapshots informed this plan:

- PR #5769 reviewed head: `6f064d626d55dd5aa1c7996c06a1349a73198460`, source branch `feat/accounts-manager`, targeting `main`. The PR describes CLIProxyAPI `v7.3.8`, a separate runner, management operations, an Accounts page, and durable pins. Its initial-account policy still permits ordered fallback; manual repinning is deferred.
- Original planning checkout: `a47db0e06`, containing the earlier device-global Codex account feature. Implementation started from the actual PR head, so findings from this older checkout were rechecked.
- Current local integration baseline: `048a59775999b60f8276a1f5d1107dbef57f5483` on `ao/agent-orchestrator-79/accounts-manager`, after rebasing onto main at `b398a95c59425c381ec2f3d36d507097c9ccc2de`. The validation increment is uncommitted on top of this baseline. The rebased history has not been pushed.

PR #5769 is attached to this session without taking over another active owner. Its last observed published head remains the reviewed commit; its base SHA at attachment was `f0a24edc389315fa85ade37eb643dace722cbd0b`. Review-comment lookup returned no inline comments. The only reported check was a successful review-statistics job, not build/test evidence. Refresh head/base and review status again before publication. Preserve existing work and do not replace this PR.

### Protected scope

The Subscriptions page and existing native Codex account-changing implementation must remain unchanged. Record an explicit path inventory from the integration baseline, including:

- Existing `CodexAccount*` settings components, `useCodexAccount*` hooks, account actions, and `codex-accounts-state`.
- Native `codex_account*` and `codex_accounts*` domain, ports, service, controller, storage, and query modules, including their tests.
- The current native coordinator at `backend/internal/service/agent/codex_account_switch_coordinator.go`, its tests, `backend/internal/session_manager/codex_operation_gate.go`, and native controller-admission tests. The older `session_manager/codex_account_switch.go` existed only in the original planning snapshot.
- Native app-server account handling, native credential reconciliation, and existing credential/configuration files.

Shared registration files may receive additive entries for Accounts. Existing settings entries, native routes, schemas, translations, and actions must retain their meaning. Generated aggregate files may change after regeneration, but their native definitions must remain compatible. A filename check alone cannot prove this boundary.

Never call the native global-switch operation to implement a managed switch. Never import native credentials automatically, share writable credential files, or redirect an existing native session merely because routing was enabled.

### M0 feasibility gates

1. **Native coexistence:** the original snapshot's coordinator enumerated running Codex sessions, but the attached PR's coordinator changes only the device credential and explicitly excludes existing controllers. That earlier restart conflict does not apply to the actual PR head. A shared admission gate still exists. Trace native switching, bootstrap, session restore, and interface transitions together and prove managed isolation without editing protected code. Do not hide sessions, misreport liveness, rename the harness, or bypass safety gates. Existing native unit checks passing does not establish full live coexistence.
2. **Credential persistence:** trace every login, import, API-key configuration, refresh, metadata update, watcher reload, and management write. The attached source confirms separate paths: SDK-managed OAuth persistence, raw import writes in `auth_files_crud.go`, API-key configuration writes, and an explicit file store in the device-login subprocess. One token-store override is insufficient. Prove AO-owned encrypted persistence and runtime reload for every advertised method without plaintext working copies or vendored-engine edits. A method remains unavailable until its path passes.
3. **Capability support:** record exact CLI/adapter versions and demonstrate child-local endpoint/auth configuration, streaming, tools, cancellation, and continuation for each proposed mode. The reviewed PR has a Codex terminal path and second-CLI terminal/ACP paths. Codex Chat remains native until a separate managed integration passes these checks without touching protected code.
4. **Baseline evidence:** run existing native checks unchanged and capture the current desktop Subscriptions and native switching flows with scratch data. Identify pre-existing failures and missing platform access. They are not passing checks and do not justify unrelated edits.

Implementation is authorized, including scoped M0 diagnostic tests. M0 must end with a recorded proceed/block decision for each gate, not assumptions hidden in later tasks.

## 3. Ownership and proposed integration areas

Reuse the PR's existing types and endpoints when their contracts fit. Names below describe responsibilities; allocate exact new filenames after attaching the actual PR.

| Area | Responsibility and allowed work |
| --- | --- |
| `accounts-manager/engine/` | Pinned upstream source. No initial edits or opportunistic version upgrade. |
| `accounts-manager/runner/internal/runner/` | Credential-store adapter, pending login operations, exact selection, route authorization, runtime registration, configuration validation, and private management protocol. |
| Accounts Manager daemon service and supervisor | Durable user intent, operation orchestration, safe cached projections, runner health, and restart reconciliation. |
| New managed-account storage/query modules | Account records, explicit defaults, per-session bindings, tombstones, and recoverable operation facts. Add a new migration; never rewrite a merged migration. |
| Shared session launch/lifecycle integration | Branch on a persisted native/managed connection mode; launch and restore the selected managed route. Preserve the native branch and protected coordinators. |
| Accounts controllers and generated API contract | Safe DTOs, revision conflicts, idempotent mutations, operation progress, and loopback-only access. |
| New Accounts components and session account control | User selection, switching, login, reconnect, removal, cached state, and accessibility. Separate queries/actions from Subscriptions. |

The runner is the only writer of gateway credential material. The daemon is the authority for user choices and durable session bindings. Use an operation journal and acknowledged private commands to reconcile these two ownership domains; do not pretend a SQLite commit and a runner mutation are one atomic transaction.

Keep the daemon's primary listener and existing listener policy unchanged. Account-control routes must remain inaccessible through mobile/LAN listeners; the runner binds only to `127.0.0.1`. Enforce the loopback route restriction server-side, not by hiding controls in the mobile interface.

## 4. Data and command contract

Persist facts needed to recover, not derived display status:

| Record | Minimum facts |
| --- | --- |
| Account | Stable public ID, provider, label, verified identity evidence where available, enabled flag, lifecycle revision, credential generation, and deletion tombstone. Engine indexes/paths remain private and may change. |
| Default | Explicit user-selected account ID per provider. Applies only to future managed sessions. Absence means a choice is required. |
| Session connection | Explicit native/managed mode, provider, account ID, and monotonic binding revision. Preserve prior per-provider choices when changing CLI. |
| Operation | ID, idempotency key and request fingerprint, target IDs, expected revisions, phase, affected controller generation, sanitized error code, and recovery facts. No credentials or route tokens. |
| Safe snapshot | Snapshot epoch/revision, account summaries, supported actions/modes, freshness, and operation progress. Persist only necessary source facts; cache derived projections. |

Public mutations accept stable IDs and expected revisions. Same idempotency key plus same payload returns the existing result; a different payload is a conflict. Concurrent switches or switch/delete races return a scoped conflict, not a silently overwritten choice.

Expose commands through AO's existing typed API boundary: start/cancel connection, reconnect, rename, set default, enable/disable, remove, choose account for launch, and start/cancel a session switch. Long operations return an operation ID and publish safe progress. The renderer never calls the runner directly. Programmatic session creation accepts the same explicit account choice or explicit saved default.

Once adopted, a managed connection stays managed across restore and feature-toggle changes. An unavailable runner blocks its requests with a recovery action. It never converts that session into a native launch.

## 5. M1: credential lifecycle and secure persistence

### Storage decision

Use an AO-owned authenticated-encryption adapter for credential records, with per-write nonces and account ID, provider, and format version authenticated as associated data. Use a separate random per-installation encryption key, not the route-token key. Keep keys and ciphertext under the configured AO data root with owner-only directories/files and equivalent Windows ACL checks.

This key-file model protects isolated credential exports and accidental plaintext disclosure; it does not protect against a process that can read both the encrypted store and its key as the same OS user. State that limitation explicitly. Do not claim hardware-backed or user-unlocked vault protection. Missing/corrupt keys block credential use and offer reconnect; never silently regenerate a key over existing ciphertext. Key rotation needs a journaled, restart-safe re-encryption procedure before shipping a rotation command.

No raw secrets in account DTOs, events, logs, crash diagnostics, argv, or persisted controller environment. An authenticated private daemon/runner exchange may deliver a scoped route token to the child-launch mechanism. The child receives only that route token, never the provider refresh token. This does not create a sandbox against hostile code running as the same OS user.

The SDK exposes custom auth managers, token providers, request access managers, and watcher hooks. Its default file store writes JSON or delegates file persistence; encryption is work for this integration, not a property to assume. Audit all read/write paths, not just `Save`. [Builder extension points](https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/v7.3.8/sdk/cliproxy/builder.go), [default persistence](https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/v7.3.8/sdk/auth/filestore.go).

### Lifecycle implementation

1. **Add:** create a pending operation bound to provider and callback state. Keep returned credentials quarantined until validation and durable completion succeed. Duplicate callbacks, cancellation, expiry, and a late callback after restart cannot create an active account. Do not change defaults or bind a session automatically.
2. **Import/API key:** validate input size/schema and supported provider, allocate AO-owned IDs and paths, and reject caller-controlled storage paths. Read only the explicitly selected import file. Do not erase that source file, auto-import native files, or retain plaintext staging. Reject unsupported identity claims rather than merging by email.
3. **Reconnect:** serialize with refresh using a credential-generation compare-and-swap. Validate that replacement belongs to the intended account/context where the provider exposes identity. On mismatch, offer a separate account. If identity cannot be established, do not silently replace a bound account as though identity were proven. Keep the previous committed credential until a valid replacement is durable.
4. **Refresh:** single-flight per credential, with bounded independent account checks. A late refresh may write only if its generation is current and the account is not deleting. Refresh keeps the stable account ID. The SDK owns provider refresh mechanics; AO owns lifecycle admission and persistence safety.
5. **Disable:** fence new launches and new logical requests after acknowledgement. A currently authorized response may finish. Re-enable is a separate user action and cannot make invalid credentials valid.
6. **Remove:** preview affected sessions and defaults without changing state. After confirmation, atomically mark deletion intent and fence new work, revoke route admission, cancel/wait for account writers, unregister runtime credentials, delete gateway ciphertext, clear managed defaults, and finalize the tombstone. Repeat safely after restart. Do not claim upstream login revocation. Stopped sessions retaining that ID must require another choice on restore.

Do not hold account locks while waiting for a response, a provider, or a session stop. Use short admission transactions and generation checks. Deletion fences a target before looking for conflicting work; a switch into it must recheck that fence immediately before commit. Switching away from a deleting account must remain possible.

M1 completes removal for unbound/stopped accounts and returns an actionable in-use conflict for active bindings before setting deletion intent. M3 adds the coordinated switch-or-stop flow for those accounts. Do not call active-account removal complete at M1.

Credential commit must use a crash-safe encrypted write and a recorded generation before publishing runtime registration. If persistence succeeds but registration acknowledgement is lost, recovery reconciles that generation; it does not repeat the provider login or create another account. Reconnect may invalidate an old provider grant even when its local bytes are retained, so recheck usability before describing a failed replacement as safely restored. Prefer independent sign-in grants over copying an actively refreshed credential from another client; surface that import risk explicitly.

**Exit evidence:** deterministic fake-provider and storage tests cover cancellation, repeated callbacks, identity mismatch, refresh/reconnect/delete races, path and symlink rejection, encrypted writes, lost keys, crash recovery, and no deleted-account resurrection. Native credentials and defaults remain byte-for-byte unchanged in isolated fixtures.

## 6. M2: explicit routing and simultaneous account isolation

1. Replace initial preferred/fallback selection with one explicit user account or explicit saved default. No selection means a structured choice-required response. Validate provider, enabled state, and supported mode; unknown quota alone is not exhaustion. Never pick the first usable account.
2. Persist connection mode and binding before child launch. Mint a capability scoped to provider, session, account, and binding revision. Keep engine credential lookup private and independent of account labels or filenames.
3. Add an authoritative live route registry in the runner. Synchronize committed bindings and account lifecycle revisions through the private management protocol. A token is usable only when its revision matches an admitted binding and its account remains eligible. On runner start, deny new requests until snapshot reconciliation completes. Use a bounded control lease so a detached runner cannot keep accepting new work indefinitely.
4. Validate authorization at every logical request, including each generation on a reused WebSocket and each retry. Do not authorize a connection forever at its handshake. Missing/expired caller context, stale capabilities, or unavailable state must reject the request, never reach a generic selector. Private management credentials must not provide an unscoped generation route. Account/model fallback and cross-account SDK retries must not broaden the user's choice.
5. Normalize only the managed child's connection configuration. Test inherited API keys, bearer credentials, provider-mode flags, saved profiles, helper settings, and command-line precedence for recorded CLI versions. A conflicting enforced configuration produces an explicit unsupported/conflict result; do not override organization policy. Native child environments remain unchanged.
6. Test all launch owners, including restore, interface changes, agent changes, and any secondary controller. A secondary process must inherit the selected session boundary or be explicitly unsupported. Never send its requests through a native account accidentally.

The reviewed route capability contains provider, engine auth index, and session ID, but no binding revision. Its selector has a generic fallback branch when caller scope cannot be found. This makes live revision checks and strict missing-context rejection implementation work, not a guarantee supplied by encrypted tokens alone. Evidence: PR head above, `accounts-manager/runner/internal/runner/route_capability.go`.

**Exit evidence:** a controlled upstream identifies the actual credential used by each request. Run A/B sessions concurrently through streaming, tools, retry, long-lived connections, daemon/runner restart, and stale-token replay. Assert neither account is substituted. Run a native session alongside them. Do not infer identity from the displayed label or a successful model response.

## 7. M3: switch transaction and in-use removal

Use a separate managed-session operation, not the native device-wide coordinator. Reuse ordinary lifecycle primitives only where M0 proves the shared boundary is safe. Permit one mutating managed operation per session; unrelated sessions remain usable.

| Phase | Durable truth and required behavior |
| --- | --- |
| Requested | Store target, expected binding revision, operation ID, and the user's timing choice. Source remains the committed account. Validate only the target for use; an expired source must not block switching away. |
| Draining or interrupting | Freeze new user-turn intake while allowing the current turn's tool/request cycle to finish, or explicitly interrupt it. Cancel is available before commitment. Do not splice accounts within a response. |
| Stopping | When required by the adapter, stop the exact recorded controller generation. Unknown liveness is not proof of a stop. Obtain acknowledgement of a runner admission fence for that session before committing; if it cannot be confirmed, remain blocked. |
| Committing | Revalidate target eligibility, lifecycle generation, session identity, and expected revision. Commit target binding plus a new revision and an input-blocked transition state. |
| Applying | Publish the new binding to the runner and obtain acknowledgement. Old revisions cannot start another request. If acknowledgement is uncertain, keep the session blocked and recover the committed target. |
| Resuming | Launch/resume with the committed target and new capability. Preserve history only through a verified adapter path. Persist controller identity before admitting user input. |
| Complete | Target can accept input and its route is admitted. Publish completion and restore focus. A renamed badge alone is not success. |
| Recovery required | Expose committed account, requested account, last confirmed phase, and retry/switch actions. Never select a different account to hide the failure. |

For a verified hot-rebind adapter, omit process stop/start only after proving a request boundary and acknowledgement that prevents reuse of old credentials. Assume restart is needed until that proof exists. Long-lived connections must be closed or reauthorized at the transition.

Treat **After current response** as the end of the current user turn, including its tools and follow-up model calls, not the first closed HTTP stream. Require positive adapter evidence of that boundary. If a terminal adapter cannot observe it reliably, keep that action unavailable and explain the limitation; offer explicit interruption instead. Never infer idle from missing events or an empty request counter. Before-commit cancellation lifts any admission fence only after reconciling the unchanged binding and confirming the correct controller generation.

Recovery before commitment retains the old binding but need not restart an invalid old account. Recovery after commitment retains the new binding even if launch fails. A rollback to the old account requires a new explicit user action. Duplicate or stale operation workers cannot launch another controller. Never replay an interrupted tool action or partially delivered response automatically.

Selecting an account for a stopped session must not start it implicitly. Persist the choice and report **Selection saved**; start/resume requires the user's continue action. Local transition steps have bounded deadlines and actionable errors. Waiting for the current turn is visible and cancellable, with **Switch now** available; a timeout does not authorize automatic interruption.

If continuation with the target is unavailable, present the new-conversation consequence before commitment. Keep the old history and offer a new conversation in the same workspace. A managed-to-unsupported-mode transition must not silently use the native account. In particular, Codex Chat remains clearly native in this release unless its separate compatibility gate passes.

For active-account removal, preview all affected managed sessions. At confirmation, reserve the account against new bindings and show any changed usage before proceeding. Let the user explicitly switch or stop each active session. This preparation fence must still allow an already admitted turn to drain when the user chose to finish it. Only after affected controllers have switched or stopped does the final deletion fence reject all new requests and credential writes and execute M1 cleanup. An unresolved live turn, ambiguous controller stop, or active reconnect prevents deletion completion. Persist a retryable deletion operation rather than showing a false success. Before irreversible cleanup, cancel can release the reservation without undoing session switches already requested; afterward, cancellation must not resurrect the credential.

**Exit evidence:** idle A-to-B switch; expired/disabled/exhausted A-to-B switch; busy drain/cancel/interrupt; simultaneous competing targets; target deletion during preparation; source deletion while switching away; mode changes; and crashes between every phase. B-bound session 2 and native account files remain unchanged throughout.

## 8. M4: user workflows and responsiveness

Build on the PR's separate Accounts surface. Add a session account control beside the CLI/model selector and an initial account choice at managed-session creation. Preserve the existing Subscriptions surface, its queries, and its native switch interaction.

- Ordinary idle switching uses two actions: open the picker, choose the account. Do not add confirmation when scope is only this session and no interruption or history consequence applies.
- A busy session offers explicit **After current response** and **Switch now** actions. Until the user's default preference is settled, do not silently assume either action. The preference does not block backend planning.
- Show current versus pending selection, stages, cancel/retry, reconnect, and any new-conversation consequence in place. Default-for-new-sessions is a separate action.
- Support add/cancel/reconnect/rename/enable/disable/remove, search, keyboard navigation, stable row identity, visible focus, and accessible progress. Removal names affected sessions and states that removal is local.
- Cached rows render immediately. Do not wait for provider checks to open a picker or Accounts. Cap independent checks at four; coalesce same-account refreshes; use a 10-second status-check deadline.
- Version events with an epoch plus revision. Ignore stale updates; after reconnect or an event gap, resync a full snapshot. Desktop closure must not cancel a committed daemon-owned switch. Login cancellation is explicit.
- Separate auth state, user disablement, cooldown, quota, and observation freshness. Unknown quota is not zero; refreshing local state must not manufacture a fresh provider observation or run a billable prompt implicitly.

**Exit evidence:** real desktop walkthroughs for adding two accounts, concurrent A/B sessions, idle and busy switching, reconnect, removal, runner outage, and mode limitations. Verify composer focus and keyboard-only operation. Renderer fixtures are useful tests but are not release screenshots.

## 9. M5: verification and release discipline

### Required behavior matrix

Run the new managed tests alongside unchanged native tests with routing off, routing on, and mixed native/managed sessions. Include:

| Boundary | Required proof |
| --- | --- |
| Native coexistence | Native switch, native login, managed switch, and managed removal do not redirect credentials or unexpectedly interrupt unrelated controllers. Verify live processes, not only file diffs. |
| Precedence and continuity | Selected account handles actual requests in every enabled mode; restore and interface transitions preserve intent. Unsupported paths are explicit. |
| Credential lifecycle | Pending/cancelled operations cannot publish credentials; concurrent refresh cannot resurrect removed data or overwrite a newer reconnect. |
| Route revocation | Stale tokens, old sockets, missing caller context, and detached/restarting runners cannot bypass revision or deletion checks. |
| Privacy | Raw credentials, tokens, internal paths, and prompt bodies are absent from public responses, events, diagnostics, and process argv. Seed fixtures with known markers so leakage checks have positive controls. |
| Configuration | Both daemon and standalone runner reject remote management, request/file logging of secrets, usage-statistics persistence, plugins, profiling, and discovery. Test each rejected flag independently. |
| Recovery | Fault injection at durable transition boundaries proves one writer per session and idempotent replay of control operations, without replaying user work. |
| Platform behavior | Owner-only storage, child environment isolation, termination, packaging, and runner supervision work on supported desktop platforms. |

The standalone runner validator at the reviewed PR head did not reject `RequestLog`, `LoggingToFile`, and the SDK's `UsageStatisticsEnabled`, although the daemon's equivalent configuration policy rejected them. The first local implementation slice closes that startup-validation gap. Live configuration mutation remains a separate policy boundary to verify. Evidence: `accounts-manager/runner/internal/runner/state.go` and its configuration tests.

### Commands to execute during implementation

These are release verification requirements; section 11 identifies the subset actually executed so far. Re-read the attached PR's manifests/workflows before execution. The original first slice used Go `1.26.5`; after rebasing, backend and the root workspace declare Go `1.27.1`, matching the current installed toolchain. Runner declares Go `1.26.0`. The root workspace includes backend/cloud but not runner/engine, so those module checks require `GOWORK=off`. Backend tests below used workspace mode. The backend suite does not cover the runner/engine modules.

| Working directory | Check |
| --- | --- |
| `accounts-manager/runner` | `env GOWORK=off go build ./...`, `env GOWORK=off go vet ./...`, and `env GOWORK=off go test -race ./... -count=1`. |
| `accounts-manager/engine` | Use `GOWORK=off` and run the vendored suite according to its checked-in requirements; distinguish environmental/integration failures from regressions. Do not change upstream merely to silence unrelated failures. |
| `backend` | Focused affected packages first; then `go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race -timeout=15m ./...`. |
| Repository root | `npm run sqlc` when queries change; `npm run api` when DTOs/routes change; verify generated drift and run `npm run lint`. |
| `frontend` | `npm run typecheck`, `npm run typecheck:e2e`, `npm test`, and `npm run test:e2e:renderer` with the workflow's runtime/dependency setup. |
| Packaging/workflows | Follow actual frontend `package`/`make` scripts and the relevant build, packaged-app, and CLI-smoke workflows. In the rebased checkout, `build` aliases `package`; its prepackage step includes platform runtimes. Never run publishing/deployment as validation. |

Read the full CI job setup, including shared frontend packages and the pinned browser runtime, before treating a command as workflow-equivalent. Run native OS jobs on their actual platforms or report the local gap and obtain remote CI evidence. The current PR check list does not establish build/test coverage.

### Performance evidence

Use the specification's measurement boundaries, with 50 accounts, 20 bindings, a release desktop build, and a recorded reference machine. Collect at least 100 warm samples and 30 cold samples; report p50 and p95.

- Usable cached picker and Accounts list: p95 at most 200 ms.
- Input feedback: p95 at most 100 ms; confirmed local label/default save: at most 300 ms.
- Prepared authenticated route: p95 at most 100 ms, excluding external refresh and process start.
- Hot idle switch: p95 at most 300 ms only where no restart/refresh is required. Record unsupported hot switching honestly.
- Local restart/reconnect stage: p95 target 2 seconds. Report total switch time including drain and remote waits separately.
- Gateway overhead to first event: p95 at most 25 ms against the same controlled streaming upstream. Include authorization/conversion and verify account identity while measuring.

Do not relax targets silently. If measurements miss, report the stage and evidence and optimize that bottleneck before release. Never make a UI optimistic success hide an uncommitted binding or a failed process start.

### Rollout and rollback

Keep the feature opt-in. A provider enable toggle controls availability for future explicit managed choices, not migration of existing sessions. Turning it off prevents new opt-ins; it must not rewrite existing bindings to native. Show remaining managed sessions and provide explicit switch/stop actions before shutting down their runner.

Use additive migrations. Existing draft-PR fallback settings may require confirmation as a single default; never promote a fallback entry automatically. Preserve existing draft managed pins only when their identity can be resolved safely. An older executable that cannot understand managed bindings is not an automatic safe downgrade. Keep the compatible runner/daemon available or require managed sessions to be stopped before downgrade, and verify schema compatibility independently.

Before any later visible-change publication, capture the actual desktop with scratch AO data and a real provider catalog. Include before/after native-regression evidence and a short switching recording where practical. Review captures for secrets, attach accessible evidence, verify it renders, and report missing OS/CI coverage. Follow the repository's PR-description and publication rules. This plan itself publishes nothing.

## 10. Plan review and remaining decisions

The review covered the library boundary, account lifecycle, transaction ordering, native coexistence, version-sensitive launch behavior, performance claims, and rollout. Corrections incorporated into this plan:

| Review finding | Correction and future proof |
| --- | --- |
| Separate files do not ensure isolation from a global lifecycle coordinator. | The attached PR no longer uses the old controller-restarting coordinator. M0 still checks native admission and launch behavior; protected modules remain unchanged. |
| Encrypted route tokens do not revoke themselves when a binding changes. | M2 requires a live revision registry, private acknowledgement, strict missing-context rejection, and per-generation authorization on persistent connections. |
| Refresh, reconnect, and deletion can race across daemon/runner boundaries. | M1 assigns a credential writer, generation checks, tombstones, and an operation journal. Fault tests cover late completion and restart. |
| A committed switch and a ready process are different outcomes. | M3 separates database commit, runner acknowledgement, controller readiness, and recovery; completion waits for readiness. |
| One user turn can contain several HTTP streams and tool calls. | Drain uses a positively observed turn boundary; a runner admission fence is acknowledged before commitment. Unknown idle is not safe completion. |
| Requiring a healthy source account defeats switching away from failures. | Target validation is independent of source authentication. Recovery never blocks a fresh target choice on the old credential. |
| Active-account deletion depends on session coordination. | M1 handles unbound removal; M3 completes the confirmed switch-or-stop workflow. |
| Revoking every request too early prevents a user-approved drain from finishing. | Removal first blocks new bindings, completes explicitly chosen session actions, and then applies the final request/write fence. |
| Mode changes, toggles, and downgrade can silently revert to native auth. | Persist connection mode, gate unsupported modes, and forbid implicit native recovery or downgrade. |
| A default file store and private permissions do not establish encrypted persistence. | Add an encrypted adapter and explicit key threat model; audit bypass paths before enabling each method. |
| Fast acknowledgement can be mistaken for fast completed switching. | Measure usable choices, persisted results, input readiness, and total switch duration separately. |
| Full verification was not established by the PR description. | Require all relevant module suites, platform checks, actual desktop evidence, and measured performance before readiness. |

Still open, intentionally visible:

1. M0 native coexistence and encrypted-storage extension feasibility still require executable proof. The scratch desktop walkthrough does not establish live native-switch coexistence. These are release blockers, not assumptions already validated by this document.
2. Exact CLI-version support, same-conversation continuation, and observable end-of-turn boundaries must be verified per mode. Managed Codex Chat is not included by implication.
3. The default busy-switch timing is unconfirmed. Both explicit actions are planned; no default preference has been inferred.

The next integration task is to complete the M0 persistence and mode-support proofs, followed by M1's full lifecycle. No UI-first rollout or protected-code workaround is authorized by this plan.

Original planning verification is recorded in the [planning review ledger](accounts-manager-plan-review/GATES.md). Implementation checks are tracked separately below so those historical planning checks cannot be mistaken for application verification.

## 11. Implementation reviews and evidence

### Review checkpoint 1: actual PR baseline

- Attached PR #5769 and fetched its head into a session-local branch, preserving all planning documents. No PR contents were changed remotely.
- Corrected the stale native-switch concern, the runner's statistics-field name, and the missing `GOWORK=off` requirement for separate modules.
- Mapped import, OAuth, API-key, device-login, and refresh persistence separately. The SDK hook alone does not cover all writes. The public watcher wrapper also contains private callback fields, so a custom watcher cannot be assumed to be an ordinary external interface implementation.
- The code-relationship graph identified a `Serve` to `LoadState` call; direct source inspection confirmed validation occurs before runtime-record creation and engine startup. No whole-codebase graph coverage is claimed.

### Slice 1: runner startup privacy guard

Changed only the runner validator and its configuration tests. Reject request logging, file logging, and usage aggregation individually. Corrected the profiling fixture to use the real `pprof.enable` key and added independent profiling/discovery cases.

Verification observed:

1. The original `TestLoadState` suite passed before changes.
2. New tests failed for all three missing guards before the production fix, each receiving no validation error.
3. The updated focused configuration suite passed.
4. Runner `go build ./...`, `go vet ./...`, and full `go test -race ./... -count=1`, each with `GOWORK=off`, passed on Linux with Go `1.26.5`.
5. Existing backend native-account/admission and route tests passed unchanged using `go test ./internal/service/agent ./internal/session_manager -run 'Test.*(CodexAccount|CodexControllerAdmission|AccountsManagerRoute)' -count=1`.

### Review checkpoint 2: after slice 1

Checked real parser field names, independent fixture mutations, safe error messages, unchanged valid configuration, the pre-start validation boundary, and the final protected-path diff. No new native or UI behavior was introduced. The three red tests provide positive controls for the rejection assertions. The full runner suite includes its existing fake-provider streaming integration test.

This proves startup rejection, not enforcement of every live management/configuration mutation. Native checks were focused, not the full backend suite or a live multi-account desktop run. Full frontend/engine suites, cross-platform packaging, real credential operations, A/B session execution, and performance benchmarks remain unverified. No screenshots are needed for this backend-only slice; real desktop baseline evidence is still required before advancing integration readiness.

The [implementation review ledger](accounts-manager-implementation-review/GATES.md) applies only to this reviewed slice. M0 as a whole and M1-M5 are not marked complete. Nothing has been committed or pushed.

### Slice 2: integration validation and first-launch repair

After the local rebase, reproduced and fixed 139 backend lint findings without changing lint rules or the vendored engine. Changes cover missing contract documentation, resource cleanup, checked listener handling, and equivalent expression simplifications. Existing native account modules remain unchanged.

Moved only the new Accounts surface into all eight existing locale catalogs. A Spanish controls/error regression failed before the fix. Review then found the Accounts navigation label outside the JSX coverage scan; a separate English/Spanish regression failed before its one-line correction. All pre-existing translation values remain unchanged.

The actual desktop review found a first-launch crash: unset routing account lists were serialized as `null`, while the picker requires arrays. A new HTTP controller regression reproduced the defect for both providers. The shared response converter now emits empty arrays for empty policies, without changing stored choices or account-selection behavior. Polling and event snapshots use that converter.

Verification observed:

1. All six gates in the [validation review ledger](accounts-manager-validation-review/GATES.md) pass, including zero backend lint findings, backend build/vet, focused account race tests, both frontend typechecks, the five-file frontend regression group, runner build/vet/full race tests, and the first-launch HTTP regression.
2. Full native-account service, session-manager, chat service, controller, API-schema, and SQLite store suites pass with the race detector in a credential-scrubbed environment. Controller and API-schema suites passed again after the response fix. API regeneration produces no artifact changes.
3. The final full frontend suite passes all 337 files: 5,330 tests passed and six skipped. A prior Electron/Node native-dependency mismatch was corrected in the local test setup; no application source change or test suppression was used to hide it.
4. Real Electron, preload, daemon, and runner were built and launched with scratch data and the built-in provider catalog. Empty Accounts opens successfully; changing to Spanish translates navigation and controls; keyboard activation opens Add; cancelling unsaved input clears it. The final snapshot contains no saved accounts or login operations.
5. Subscriptions renders before and after the desktop walkthrough. No native account was signed in or switched. This proves scratch-mode rendering continuity, not live multi-account coexistence.
6. Native-window screenshots and a 4.8-second recording were captured locally and inspected. The recording passes a full decode check. These are not uploaded PR evidence or latency measurements. The app was stopped afterward.

### Review checkpoint 3: after slice 2

Reviewed the changed contracts, response-array invariant, locale interpolation and plurals, accessible labels, navigation registration, generated-artifact drift, and protected-path diff. The second review found and fixed an actual empty-install defect rather than relying only on populated test fixtures. Evidence and the full frontend rerun outcome are recorded in the validation ledger.

The existing ordered initial fallback policy is unchanged by this increment and still conflicts with the target explicit-selection contract. M2 must replace it; no session-choice or switching claim is made here. Startup privacy guards still do not constrain every live configuration mutation. Encrypted credential persistence, lifecycle races, supported-mode proof, live native coexistence, A/B request identity, transactional switching, cross-platform packaging, and performance targets remain open.

Next: finish M0's persistence-path and supported-mode proofs, including live configuration policy, before enabling M1 lifecycle operations. This increment does not complete M0 or M1-M5. No changes have been published.

### Whole-branch review follow-up

The [branch review](accounts-manager-branch-review/REVIEW.md) records nine defect groups and their local fixes. The review removes automatic fallback/default selection, preserves durable pins, restricts management and execution admission, fixes mixed-pool selection, makes configured keys visible/removable, waits for asynchronous registration, and protects event/cancellation ordering. Managed sessions cannot silently enter unsupported native Chat. Explicit account removal clears only its own selected default without changing session pins.

The real desktop workflow added two local test accounts, explicitly selected the second default, and removed both. Safe identifiers distinguish key accounts; saved keys are not labeled as verified. The staged release requirements above are unchanged. These fixes do not implement encrypted credential persistence, the durable native/managed mode record, per-session selection, revocation, or transactional switching. Final verification and remaining gaps are recorded separately from the historical validation increment in the branch-review ledger. Nothing has been published.

### Credential persistence foundation and M0 extension review

The [completion ledger](accounts-manager-completion/PLAN.md) tracks the remaining implementation separately from prior review fixes. An encrypted store with durable operation cancellation, single-writer locking, stable identity, and deletion tombstones now has focused and concurrent race-test coverage. Runner build, vet, full race tests, and Windows/macOS cross-compilation passed. The store is not wired into `Serve`, and this does not complete M1 or establish end-to-end encryption.

The [persistence review](accounts-manager-completion/storage/REVIEW.md) records an M0 decision: management callbacks write plaintext authorization codes, while direct SDK authenticator callbacks bind all interfaces with no caller-supplied host/listener option. The public token-store hook fixes neither transport. Recommend a minimal SDK loopback-listener extension; changing the pinned engine or withholding browser sign-in needs an explicit scope decision. Subscriptions and native account-switch modules remain unchanged. No live credentials were used, and nothing was committed or pushed.
