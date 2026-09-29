# Accounts Manager production-readiness implementation plan

Date: 2026-09-29. Source baseline: `d23bdaee42640da7d7fe70fd0b9af52843a3ca61`.

Contract: [production-readiness specification](SPEC.md). Execution ledger: [IMPLEMENTATION-GATES.md](IMPLEMENTATION-GATES.md). This is a documentation deliverable, not a claim that implementation or verification below has run. All implementation milestones start OPEN. Existing source, specification and historical review archives remain unchanged.

## 1. Outcome and execution rules

Complete the five specified release areas without replacing working account controls or the embedded library. Users retain explicit per-session account choice; no fallback account is selected automatically. Native switching, Subscriptions, personal credential homes and the 91 protected paths remain outside the edit scope.

Work in bounded milestones. For each substantiated defect: preserve a failing assertion, apply the narrow correction, run focused tests, review the first complete path, correct findings, then expand coverage. An existing red regression is reused, not weakened or rewritten to manufacture a new failure. A build error or empty test selection is not a valid red reproduction.

If a proposed boundary test is already green, retain it as a positive control and skip duplicate implementation. Only missing or incorrect behaviour needs a failed-first correction. Earlier implemented tombstones, refresh joining, public validation and cache fences are regression obligations, not invitations to rebuild them.

Use existing public HTTP/service/port boundaries. New files and test names labelled **proposed** below do not exist yet and are not evidence. At milestone admission, record an exact owned file list, actual test commands and expected oracles in the ledger. Broad directory names below locate work; they do not authorize a refactor of that directory.

Keep a single implementation owner for overlapping lifecycle/controller/storage files. Independent reviews are requested through the project orchestrator. If additional workers are arranged, dispatch only disjoint reviewed scopes; shared ports, migrations and generated contracts have one writer. Do not start destructive live-account checks before the relevant safety review clears.

### Common freeze and review protocol

1. Record source/base hashes, dirty-file inventory, accepted manifests, platform/toolchain, applicable tests and the acceptance IDs being addressed. Preserve the previous seal separately.
2. Reproduce defects with deterministic barriers or injected boundaries. Retain both failing assertions and positive/foreign-owner controls. Do not use arbitrary sleeps as the concurrency oracle.
3. Implement, then self-review ownership, admission, cancellation, persistence and secret boundaries. Review a complete first path before adding the remaining schedules.
4. Run focused race selections three times, then full affected packages and actual-process tests. Count tests and skips. An edit invalidates affected earlier results.
5. Freeze changed source, generated output, protected-path audit and logs. Independent review must name that exact manifest. Fix substantiated findings in a new seal and reverify; do not rewrite the old evidence.

## 2. Milestones and dependencies

| Milestone | Deliverable | Start dependency | Spec coverage |
| --- | --- | --- | --- |
| M00 | Baseline, base integration and shared contracts | Current preserved tree | R5-A foundation |
| M01 | Production Linux containment and exact retirement | M00; ownership design review | R1-A, R1-B, R4-A foundation |
| M02 | Coordinated removal, revocation and crash recovery | M01 independently clear on Linux | R1-C through R1-F; integrated R1-A/R1-B |
| M03 | Pending-switch restart and existing-operation repair | M00; primary sequence follows M02 | R2-A, R2-C, R2-D |
| M04 | Settings reconciliation, durable queues and cold restart | M03 | R2-B, R2-E, R2-F |
| M05 | Managed Codex Chat execution | M02-M04; enabled platform's ownership subgate | R3-A, R3-B |
| M06 | Isolated profiles and explicit migration/reconnect | M05 execution contract; platform containment | R3-C, R3-E |
| M07 | Credential verification, login and truthful observations | M00; migration interactions follow M06 | R3-D, R3-F |
| M08 | Native Windows acceptance and bounded corrections | Ownership checks: M00 and native runner; final interface matrix: M05-M07 | R4-B, Windows portion of R4-E |
| M09 | Mac feasibility, production integration and native acceptance | Ownership checks: M00 and native arm64/x64 hosts; final interface matrix: M05-M07 | R4-C, R4-D, Mac portion of R4-E |
| M10 | Integrated live desktop, platform recovery and performance | Relevant M01-M09 capabilities independently clear | R4-A/R4-E, R5-C, R5-D |
| M11 | Final base reconciliation, full verification and review | M01-M10 complete for required release matrix | R5-A, R5-B, R5-E |

Primary local order: M00, M01, M02, M03, M04, M05, M06, M07, M10, M11. Arrange native hosts and test identities during M00. M08/M09 feasibility and non-overlapping verification may progress alongside local work; their missing resources must not stop independent local milestones. Common contract changes are reviewed before adoption on another platform.

M05/M06 may be enabled per verified platform during development. The cross-platform product gate stays OPEN until Windows and both Mac architectures meet the same contract. Do not call a partial Linux result release completion.

Ownership subgates and final workflow acceptance are distinct to avoid a circular dependency: native containment is reviewed first (M08a or M09a/M09b), then M05-M07 integrate account features, then the complete native interface matrix is rerun before M08/M09 close. A synthetic ownership proof alone never clears the final platform milestone.

## 3. Implementation work

### M00. Preserve the baseline and settle integration contracts

**Work areas:** this plan/ledger; current source and protection manifests; applicable workflows; additive migration inventory; existing domain and port contracts. Production edits are limited to independently justified base-conflict resolutions after the baseline is preserved.

1. Verify the current specification hash, source HEAD, 91 protected paths, five guest-design files and accepted source/generated manifests. Resolve which manifest is current before comparing; do not restore an older baseline over newer accepted work.
2. Preserve the three existing failing retirement groups and the two observed restart defects. Capture an ordinary full-package baseline using the same test environment intended for the corrections. Separate known inherited shutdown/media fixtures from introduced failures.
3. Inspect current base/head conflicts, migration numbers, required CI jobs and review verdicts. Preserve the pre-integration tree; perform any necessary normal base merge without history rewriting, then repeat baseline checks. Document the successor source hash used by M01 onward.
4. Record the provider/version/authentication/interface/platform capability matrix. Identify native Windows, Mac arm64/x64, disposable live-account and signing resources. Missing resources get explicit owners and OPEN gates, not compile-only substitutions.
5. Settle shared contracts before launcher implementation: immutable launch identity; start permit; exact stop/join result; durable tombstone; binding/credential/controller generation; route revocation and worker draining. Reuse existing ports where sufficient. Any necessary extension must preserve native implementations and be reviewed before use.

**Verification:** source and protected-manifest checks, existing-test discovery, migration/route contract inventory, unchanged-control comparison where required. No new broad runtime abstraction without two concrete call sites.

**Exit/review:** reproducible baseline, exact scope, reviewed ownership contract and a resource ledger. Planning this milestone alone does not satisfy it.

### M01. Wire Linux containment into real managed launches

**Existing code:** `backend/internal/adapters/chatdriver/persistenthost/contained_launch_linux.go`, `contained_permit_linux.go`, `contained_identity_linux.go`, `provider_child_unix.go`, `provider_owner_linux.go`, `exact_shutdown.go`; `host.go` and the direct/fallback runtime launch composition. The namespace helpers currently exist separately from the ordinary provider-child launch path.

**First red:** run the existing three positive retirement groups unchanged. Add a **proposed production-launch containment test** through the real dependency construction that holds provider execution until identity and permit persistence are verified. It must fail if only the standalone namespace test helper is contained.

**Implement in two reviewed slices:**

- M01a: add a managed-only Linux launcher that uses the existing durable launch/permit primitives. Publish exact identity before execution; withhold the one-use permit until admission succeeds. Contain provider code and commands at creation. Reject unsupported kernels/prerequisites before launch; retain the native path unchanged.
- M01b: provide usable workspace, history, I/O and bounded network access without host control sockets or execution brokers. Connect exact stop/join and cold reopening to `exact_shutdown.go`. Keep original and replacement identities independent, and make the runtime wrapper forward the correct capability for both direct and fallback handles.

Use proposed platform-specific launcher files rather than making the current generic Unix path silently apply Linux assumptions to Mac. Any shared dispatch change is opt-in for managed execution only. Carry only non-secret ownership metadata in durable records.

**Expand tests:** no permit, duplicate permit, cancellation before permit, interrupted rename/sync, host/helper death, late fork after census, moved group/session, replacement publication then shutdown, lost stop response, identity reuse and inaccessible/corrupt proof. Include local file/tool/network operations so containment is usable, not just stoppable.

**Review gates:** review M01a identity/admission/escape prevention before M01b product wiring. After real production recovery is green, freeze for independent exact-retirement review before M02. No group-absence shortcut and no weakening the five existing positive cases.

### M02. Complete coordinated deletion and revocation

**Existing code:** `backend/internal/session_manager/accounts_manager_removal.go`; Chat `accounts_manager_removal.go`; account service `removals.go`; SQLite removal/chat-host stores and queries; runner `credential_removal.go`, `route_bindings.go`, `route_capability.go`, `credential_vault_admission.go` and refresh/reconnect lifecycle files.

**First boundary check:** production coordinator with two managed sessions on A, one on B and an unrelated native binding. Kill A's original Chat host, publish and stop a replacement, reopen SQLite, then request/retry removal. Assert no completion while an original descendant or authorization worker survives. This must use M01's production path, not a fake retirement acknowledgement. Preserve it as a positive control if M01 already closes the sequence; reproduce remaining coordinator defects before correcting them.

**Implement:**

1. Enumerate all positively matched active, inactive and dormant bindings/owners. Validate exact impact revision, reserve the removal generation and install the durable account fence before new work can enter.
2. Preserve queues; compare-and-swap journal admission immediately before each irreversible step. Preserve pre-stop cancellation after lost responses and reject stale cached retries.
3. Stop/join each exact owner. At stopping, cancel and join local upstream workers; revoke capabilities and leases; join refresh/reconnect publication. Do not treat another generation in the slot as proof that the original descendants stopped.
4. Remove the encrypted credential idempotently only after acknowledgements. Complete matched binding/default cleanup in one database transaction. Preserve history and queued input; require explicit rebinding before dispatch.
5. Recover incomplete operations before opening request admission on restart. Retain tombstones against watchers, SDK writes and delayed callbacks. Keep missing-proof cases actionable and fenced; do not offer an unsafe force-complete action.

**Expand tests:** every durable write/acknowledgement crash cut, already-missing credential/runtime with proper proof, lost completion/cancellation responses, partial multi-session stop, concurrent switch/remove/reconnect, generation takeover after a stop error and repeated cold retry. Assert B/native survive, queues do not duplicate and stale authorization fails.

**Verification/review:** coordinator/store/Chat/runner races, complete persistent-host package and direct/fallback process matrix, then independent deletion review. Only afterward schedule disposable live removal.

### M03. Repair shutdown-interrupted switches

**Existing code:** `backend/internal/session_manager/accounts_manager_switch.go` (`finishAccountSwitchRun`), `accounts_manager_recovery.go` (`RetryAccountsManagerSwitch`), startup reconciliation and SQLite switch store/queries. The current finish path maps requested/waiting work to `failed`; retry admits only recoverable/requested/waiting phases.

**First red:** a deterministic requested/waiting switch interrupted by manager shutdown, followed by SQLite reopen and real manager reconstruction. Assert the original operation retains pre-stop retry/cancel semantics. Add a fixture shaped like the already-stuck live operation, plus truly cancelled, stale-revision and post-stop negative controls.

**Implement:** distinguish shutdown interruption from a user cancellation, target error or irreversible failure. Preserve durable intent and ownership. Restore only the recorded generation's fences. Add a generation-checked repair path for historically misclassified operations when durable pre-stop proof exists; do not globally reopen terminal failures. Add an additive migration only if the current journal cannot safely distinguish required evidence.

Keep reserved and retired target generations intact through launch/publication failures. Require fresh exact-owner proof before input or teardown, and preserve the no-replay rule after stopping. Use the durable admission compare-and-swap as the cancellation linearization point, with generation-checked local worker/fence cleanup. Do not claim one transaction spans database and memory.

**Expand tests:** cancel-versus-retry with three-party barriers, both stop-wins and cancel-wins, stale readiness, target publication failure, empty history, source exhaustion, last tmux-server absence and replacement takeover during a stop retry. Run through production runtime wrappers.

**Exit/review:** old affected operations and newly interrupted ones recover, cancelled operations remain terminal, and full switch/store/Chat ownership suites pass. Freeze for independent recovery review before M04 integration.

### M04. Reconcile settings and preserve queues through cold restart

**Existing code:** Chat service/controller and account handoff; session manager target start/readiness; `frontend/src/renderer/components/chat/SessionChatSurface.tsx`, `ChatComposer.tsx`, account controls and conversation configuration hooks. Trace the live configuration source before selecting the smallest files to edit.

**First checks:** reproduce the selected model/effort/permission mismatch between persisted state and the live composer after restart. Add a boundary test for acknowledged queued input across daemon plus runner/vault reconstruction, using a barrier around queue adoption; retain correct existing queue behaviour as a positive control.

**Implement:** separate requested configuration, committed configuration and observed runtime configuration. Rehydrate from the authoritative controller epoch and invalidate outgoing catalogs. Never silently replace model, effort, permission or account when a capability is missing. Gate dispatch on reconciliation or expose a concrete recoverable mismatch.

Adopt a queue under one controller generation, preserve order through cancellation/handoff, and prevent duplicate local dispatch. Preserve uncertain remote receipt without automatic replay. Rebuild route authorization from current binding and credential generation; reject old capabilities before accepting resumed work. Cold restart must construct a new runner and reopen the vault, not reconnect to the existing process.

**Expand tests:** reattachment versus cold start, active/idle/queued states, terminal-to-Chat transitions, stale catalogs and readiness, target startup failure, revoked/removed account and unrelated B progression. Pair every uncertain-result test with an ordinary resume control.

**Exit/review:** public saved settings, renderer state and the next actual execution agree. Account, queue and authorization invariants survive both restart types. Self-review after the first complete restart path; independent review on the integrated M03/M04 snapshot.

### M05. Implement managed Codex Chat

**Existing boundary:** `backend/internal/session_manager/accounts_manager_routing.go` deliberately rejects this mode. Use account-specific daemon composition, existing Chat service/ports, persistent-host ownership and runner routes. Native Chat adapters and protected native switching remain unchanged.

**Proposed code:** a managed-only adapter package, for example `backend/internal/adapters/chatdriver/managedcodex/`, and minimal optional factory/port wiring. Final names are set during design review. Removing the existing rejection is the last enablement step, not the implementation.

**First red:** two production-created managed Chat controllers must produce overlapping streams under distinct synthetic upstream credentials. Inspect actual requests; a bound label, shared process or native fallback cannot pass. Test missing capability and foreign ambient credentials with zero provider calls.

**Implement:** dedicate an app-server process and transport to each controller; apply selected account configuration before any protocol request; bind readiness, request correlation, history/resume and exact teardown to immutable launch identity. Integrate switch, disable, removal and terminal/Chat transitions. For unsupported continuation, require an explicit new-conversation decision before source stop.

**Expand tests:** A/B and same-account concurrency, tool continuations, reconnect/refresh while active, conflicting helper/keyring/settings, launch failure, stale transport replies, resumed conversation and removal during initialization. Native control remains unaffected.

**Exit/review:** a working Linux path must use independently cleared M01/M02 containment. Windows/Mac development enablement additionally requires the native ownership subgate and independent review of the managed adapter; final platform acceptance still requires the complete M08/M09 matrix. Review architecture before code, the first overlapping path midway, and the exact final enablement snapshot before turning on capability flags.

### M06. Isolate credential profiles and implement explicit migration

**Existing code:** account selection/routing and management capabilities, runner `credential_migration.go`, `credential_reconnect.go`, `credential_refresh.go`, vault generation/admission, and account creation/reconnect controls. Existing draft-vault migration is not itself setup-token semantic migration.

**First reds:** a managed profile launch with conflicting global login/helper state must not authenticate as the ambient account; an explicitly migrated legacy record must not replace the wrong identity or generation when reconnect/refresh/remove races with verification.

**Implement in two slices:**

- M06a: create a managed-only private profile strategy with platform permissions, explicit effective configuration, private history as required, and contained execution. Verify supported binary/keyring/helper behaviour before enabling it. Do not copy a whole personal home or call the global native account switch.
- M06b: preview legacy format, identity and impacted sessions; require explicit migration/reconnect intent. Verify same identity and generation, journal publication, preserve the old credential on unsuccessful replacement unless independent revocation won, and expose recovery after a lost response. A different identity requires a separate user choice.

**Expand tests:** successful same-identity replacement, wrong method/identity, stale generation, competing refresh, remove-wins, cancelled/lost-response publication, crash before/after journal/vault commit and profile permissions. Ensure old capabilities cannot authorize a replacement generation without current admission.

**Exit/review:** effective isolation is proven, not inferred from a different directory. Both slices get self-review at initial green; independent credential/isolation review precedes capability enablement and live migration. Unsupported platform/provider contracts remain OPEN with a concrete reason.

### M07. Finish credential verification and truthful status

**Existing code:** runner `credential_verification.go`, `credential_quota.go`, `credential_quota_error.go`, browser/device coordination and auto-refresh; management client/service projections; `AccountUsage.tsx`, `AccountsManagerSection.tsx`, `useAccountsManagerQuery.ts` and account picker controls.

**First reds:** exhausted capacity followed by a generation authentication-looking error must not hide valid quota or permanently misclassify the account; a stale pre-revalidation response cannot overwrite the newer generation's status. Reuse current wrong-key/wrong-form and browser opener regressions instead of rebuilding passed behaviour.

**Implement:** classify authentication rejection, capacity, permission, unsupported method and transient transport failure independently. Revalidate before changing readiness; preserve available quota and its timestamp. Keep device Harness login separate. Verify supported setup-token/API-key semantics without treating a prefix as validation. Standard forms use the correct upstream default; custom endpoints enforce the existing transport/redirect policy before secret transmission.

Retain per-account single-flight, causal caches, the four-check concurrency cap and joined refresh shutdown. Browser sign-in keeps exact loopback redirect/listener ownership with expiry, cancellation and manual-link recovery. Return safe actionable errors, not raw upstream credentials or private metadata.

**Expand tests:** valid/invalid/insufficient-scope credentials; occupied callback port/opener failure; cancelled or expired delayed callback; positive quota for generation-unavailable accounts; large rounded-equal revisions; unrelated account cache preservation; blocked refresh cancellation and vault closure ordering.

**Exit/review:** API/CLI/renderer agree on current capability, identity and observations. Preserve the normal model/effort/account layout and no-fallback controls. Self-review the first error-revalidation path, then independently review credential changes before integrated live checks.

### M08. Verify Windows natively and fix only substantiated defects

**Existing code:** persistent-host `provider_child_windows.go`, `provider_owner_windows.go`, direct contract/recovery tests, ConPTY launch identity, runtime wrapper and Windows vault permissions. Creation-time Job Object containment is already present.

**Entry:** actual native Windows machine with recorded OS/architecture and the required SDK/toolchain. Before the machine is available, cross-compile/vet and contract review are useful diagnostics but cannot satisfy this milestone.

**Execute:** run the direct kernel-contract test first, then birth/publication/resume crash cuts, kill-on-close/descendant cleanup, same-name foreign jobs, exact cold reopening, interrupted receipt writes, host death, SQLite reopen, replacement preservation and active/inactive multi-session removal. Retain existing compatibility-runtime red logs as historical evidence.

Record this initial ownership review as M08a. After M05-M07 integrate the completed interfaces and credential paths, rerun the full native matrix as M08b. Initial kernel CLEAR permits reviewed integration work; it does not close M08 or R4-B.

If a native test exposes a defect, preserve its deterministic failure and apply a bounded Windows correction. Do not rewrite correct kernel-proof logic to make an unsupported compatibility runtime pass. Verify both account interfaces, browser sign-in, file locking, native coexistence and packaged install/upgrade.

**Exit/review:** native logs, discovered tests, process oracles and artifact hashes satisfy R4-B. Independent review includes kernel identity, cold teardown and wrapper semantics. Final desktop/install integration is repeated in M10.

### M09. Resolve Mac feasibility before production adoption

**Baseline design:** [per-launch guest contract](../MACOS-GUEST-CONTAINMENT.md) and its existing review archive. Preserve design bytes; proposed successor decisions are separate reviewed documents. Relevant host areas are persistent-host platform ownership, an optional managed guest launcher, existing workspace/snapshot/provider infrastructure and desktop packaging scripts.

**M09a, feasibility:** on real arm64 and x64 Macs, implement only the accepted G1/G4 synthetic experiment first. Persist immutable launch identity before guest start; kill the keeper during bootstrap, active execution and stop publication; reconstruct the daemon in the same boot. Assert no guest descendant survives successful retirement and unrelated guests continue. No live credentials or new production capability in this experiment.

If exact same-boot proof cannot be established, preserve the failed schedule and retain HOLD. Review a stronger boundary before further implementation; missing descriptors, helper disappearance, process-group absence or rebooting the machine are not a fix for same-boot retirement.

**M09b, product path after independent feasibility CLEAR:** add the managed-only guest launcher, consumed permits/tombstones, authenticated narrow transport, exact stop/join and registration cleanup. Keep provider commands inside the guest. Provide required workspace/history, bounded file sharing, development networking, terminal/Chat I/O and tools without a host execution escape. Integrate restart, queues and credential admission.

**M09c, packaging:** after M05-M07 interface integration, package and verify helpers/images, permissions, signing/notarization, both architectures, upgrades and interrupted updates. Repeat production retirement and full A/B workflows on each native architecture. Use the repository artifact verifier; do not publish as a validation step.

**Exit/review:** independent review after M09a and M09b; native evidence and packaging acceptance after M09c. A design approval or a usable guest with an unproven death boundary cannot satisfy M09.

### M10. Prove the integrated desktop and measure responsiveness

**Work areas:** existing public controls and desktop integration tests; isolated runtime fixtures; proposed benchmark fixtures for the boundaries in SPEC.md. Product edits are limited to reproduced integration/usability failures, including the recorded narrow-window removal action issue after classifying its ownership.

Follow the desktop-development skill and preview/browser guides. Use a separate checkout with its own installed dependencies, disposable AO data, real provider catalog and authorized accounts. Do not substitute component mocks or use personal active sessions as destructive test fixtures.

**Workflow sequence:**

1. Sign in two distinct accounts; check invalid/wrong-method input, explicit initial selection, model/effort, identity labels, positive quota and device-login distinction.
2. Run concurrent A/B terminal and Chat work plus an unrelated native control. Verify replies and request attribution, then exercise both switch directions, history, target failure/retry, cancel-wins/stop-wins and queued-message preservation.
3. Exercise separate window restart, daemon reattachment, full runner/vault cold restart and OS reboot. Confirm settings, operation identity, queues, revocation and no fallback after each.
4. On disposable credentials after safety CLEAR, preview exact removal impact, reject stale confirmation, perform active/inactive removal, inject a recoverable failure, restart/retry and prove no stale authorization or automatic relaunch. Separately record provider-side revocation only where actually supported and observed.
5. Inspect keyboard/focus, screen-reader progress, narrow-window actions, unavailable-service recovery and actual captures/recording. Redact private information; no reconstructed visuals or optimistic success labels.

**Performance:** use the spec's release build/reference machine, 50 accounts, 20 bindings, 100 warm samples and 30 cold starts. Measure every specified p95 boundary, p50, full end-to-end switch time, failures and provider/drain/guest costs. Use controlled upstream timing for gateway overhead, not uncontrolled live latency. Do not invent measurements from test duration.

**Exit/review:** R5-C/R5-D and final platform workflow portions pass with source/package hashes and bounded real-account claims. Any correction returns to its affected milestone for tests and review before the final freeze.

### M11. Final integration, verification and handoff

**Entry:** all implementation/native milestones complete for the requested matrix. Recheck current base state; integrate new conflicts by normal merge if required and rerun affected plus full verification. Keep merged migrations unchanged and preserve protected/native behaviour.

Run the full checklist in section 4 on one final snapshot. Resolve introduced failures. Keep unchanged-control diagnoses for inherited issues, but do not call a failed job green. Skipped required acceptance cases and missing native evidence keep the release blocked.

Require an independent integrated review of containment, authorization, settings/queue recovery, credentials and public controls together. Earlier slice approvals do not approve new interactions. Freeze the final source/generated/protected/evidence manifests, current test results, review verdict and exact unresolved gaps (if any).

**Exit:** all 28 specification acceptance cases have current evidence, all five product areas pass and required local/remote jobs have observed results. Prepare the final PR summary, verified change counts, testing guide, reviewer-accessible desktop evidence and recovery/support notes. Publishing is separate: announce the exact push/PR action and obtain the required approval. No force push, merge or release is implied by this plan.

## 4. Verification runbook

Commands below are planned checks, not results from writing this document. Use a session-owned temporary/build directory, the pinned toolchains in the candidate workflows and a credential-stripped test environment. Do not redirect or expose personal credential directories. Run heavy suites serially when required by disk/memory limits.

### First existing regression

From `backend`, discover the three retained test groups and confirm the names before running:

```text
go test -list 'TestRemovalHost(ExactShutdownSurvivesReplacementAndReattach|CrashReplacementCannotHideOriginalProvider|CrashLateForkSQLiteRecovery)' ./internal/adapters/chatdriver/persistenthost
go test -p 1 -race -count=1 -timeout=10m ./internal/adapters/chatdriver/persistenthost -run '^TestRemovalHost(ExactShutdownSurvivesReplacementAndReattach|CrashReplacementCannotHideOriginalProvider|CrashLateForkSQLiteRecovery)$'
```

The current expected baseline is assertion failure in five cases across these groups on the applicable Linux environment. Preserve exact output. A different result requires investigation of source, discovery, prerequisites and test environment before treating the bug as fixed.

After correction, repeat the same selection three times, then the full persistent-host and affected coordinator suites. Never exclude the positive recovery cases to obtain a green package result.

### Layered checks

| Working directory | Required checks |
| --- | --- |
| `backend` | `go build ./...`; `go vet ./...`; `go test ./...`; `go test -race -count=1 -timeout=20m ./...` |
| `backend` | `go test -tags e2e -race -count=3 -timeout=20m ./internal/session_manager -run '^TestAccountsManager'` on applicable platforms; inspect discovery/build constraints and record skips |
| `backend` | `go test -tags e2e -v ./internal/cli/...`; applicable installed-provider/runtime e2e packages and all current platform kernel-contract tests |
| `accounts-manager/runner` | `go build ./...`; `go vet ./...`; `go test ./...`; `go test -race -count=1 -timeout=20m ./...`; pinned full lint |
| `accounts-manager/engine` | Callback/auth and affected engine ordinary/race checks, plus the full engine scope required by the current workflows; preserve untouched controls for inherited failures |
| Repository root | `npm run api`; `npm run sqlc`; generated drift check; `npm run lint`; applicable locale, secret and contract checks; `npm run product-ui:check` |
| `frontend` | `npm test`; `npm run typecheck`; `npm run typecheck:e2e`; `npm run test:e2e:renderer` with workflow CI settings; `npm run build` and platform packaging checks |
| Each native platform | R4 kernel/containment and production recovery matrix, install/update and actual desktop workflows; compile-only results recorded separately |

Bind new focused selections to real tests when added, with nonzero discovered counts. Before using inherited scripts/commands, read them. Include their setup and test-owned cleanup in evidence. No publishing/deployment job is a validation command.

Run security/compatibility checks for callback/listener scope, credentials at rest, public/telemetry/error redaction, missing-proof refusal, stale leases, full generated drift and all protected files. Verify full suites after final edits; do not combine passing results from incompatible snapshots.

## 5. Data changes, rollback and operational recovery

- Prefer the current journal/schema. If added evidence is required, introduce an additive migration with explicit zero/unknown semantics, legacy fixtures and failure-atomic upgrade tests. Generate SQLite bindings and API artifacts from their sources.
- Back up disposable test data before migration experiments. Never alter the user's active database or downgrade it to test a rollback.
- Capability disablement blocks new managed launches; it must leave pending stop/revocation/recovery obligations visible and recoverable. Disabling UI does not authorize dropping tombstones or bypassing admission.
- Do not roll a credential or binding back to an account the user did not choose. Unsupported binary/schema downgrades refuse unsafe access with recovery guidance. A stale runner cannot reopen storage or authorize against an incompatible generation.
- Support diagnostics include safe operation/request IDs, phase, revisions, capabilities and next steps. Secret contents, private endpoints, runtime handles and personal data remain excluded. Missing ownership proof has no broad-kill or force-complete recovery shortcut.

## 6. Self-review of this plan

- The Linux primitive-to-production gap is explicit. Tests through the real launch construction prevent a standalone-helper pass from certifying the shipped runtime.
- The waiting-switch shutdown defect is tied to the current finish/retry code, with existing damaged journals covered separately from new shutdown behaviour.
- Settings reconciliation remains a traced root-cause task; this plan does not assume the mismatch is exclusively a renderer bug or overwrite a live controller's state blindly.
- Managed Chat is a separate owned execution path, not removal of a capability guard. Profiles and migration cannot mutate protected native credentials or infer identity from email.
- The current macOS design is a feasibility candidate, not an approved production backend. Windows guards are retained until native evidence demonstrates a real defect.
- Local completion cannot silently satisfy native/live/release gates. The dependency graph allows useful local work without treating unavailable machines as successful tests.
- Native ownership subgates precede new-interface integration, while final native workflow acceptance follows it. This avoids making managed Chat and native verification wait on each other's full milestone.
- The deletion journal coordinates runtime, vault and database without claiming one cross-resource transaction. Queue dispatch ownership is distinguished from ambiguous remote execution.
- Already-green boundaries remain positive controls rather than fabricated failed-first work. Cancellation uses a durable linearization point with generation-checked cleanup, not an imaginary database/memory transaction.

## 7. Completion and immediate next implementation action

The [implementation ledger](IMPLEMENTATION-GATES.md) starts with 12 OPEN milestones. Each contains the acceptance IDs it must produce or contribute to; shared IDs require all contributing milestones, not the first passing slice. The five product areas in SPEC.md remain OPEN until M11 reconciles final evidence.

Next implementation action, when implementation is requested: execute M00 preservation and test discovery, retain the existing M01 red assertions, then review the production containment wiring contract. Do not start by changing UI or weakening the recovery tests.

No dependable completion date follows from this plan alone. Linux production containment is unfinished implementation; Mac same-boot feasibility and native host availability are critical-path uncertainties. Update estimates from completed reviewed milestones and measured native experiments, not file counts or old passing test totals.
