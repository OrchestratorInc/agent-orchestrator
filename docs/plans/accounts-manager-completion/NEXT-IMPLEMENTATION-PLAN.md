# Account isolation and completion plan

Date: 2026-09-28. Baseline: `04e12ca3dc78ba96674a63c2039ac6b0b271b52f`.

Status: implementation authorized by the user on 2026-09-28, with review between slices. Implementation and release gates remain open. Continue routine local work without approval pauses; preserve publication, credential-use and protected-path boundaries. This plan consolidates the remaining work without replacing historical review reports or their immutable evidence. Current execution is recorded in NEXT-EXECUTION.md.

## Outcome

A user starts Session A with Account 1 and Session B with Account 2. Both work concurrently. Switching, reconnecting or removing Account 1 cannot change Session B, the device login, native account records or a saved default. AO manages the lifecycle; the user chooses every account and any change of authentication mode.

Build on the embedded library and existing account coordinator. Do not build another proxy or use the library's automatic account balancing.

## 1. What exists, and what still needs work

The quoted premise needs a correction: the branch already implements session-aware proxy routing. The missing work is reliable production execution, complete interface coverage, safe teardown, and the remaining credential and selection experience. Separate credential homes are conditional, not an unconditional prerequisite for a custom-provider proxy session.

| Area | Evidence in the published tree | Remaining obligation |
| --- | --- | --- |
| Exact account routing | `route_capability.go`, `route_bindings.go`, `policy.go`: authenticated route claims include session, account and binding revision; `exactRouteSelector.Pick` returns only the pinned credential | Prove simultaneous A/B behavior through production launches, streaming, retries and restart; no broader selector or native fallback |
| Terminal child isolation | `accounts_manager_routing.go` passes a child-specific connection; `appendAccountsManagerRouteFlags` uses a custom provider and session token | Verify installed-version precedence, ambient credentials, helpers and configuration overrides, and persistence/resume isolation |
| Managed Chat | ACP has a managed route; `checkAccountsManagerChatMode` and Chat service explicitly reject managed Codex app-server sessions | Implement a separate managed execution path without modifying the protected native adapter; prove process-level authentication isolation |
| Initial choice | Spawn DTO has no explicit managed-account selection; launch currently consults an existing binding or saved routing policy | Add atomic selection at creation across daemon, CLI and desktop, before the first provider request |
| Switching | Journals, ownership fencing, cancellation and recovery exist; production controls are wired | Production execution tests stop at `TARGET_NOT_READY`; cancellation has an intermittent 409/202 schedule to classify |
| Deletion | Durable blocking journal, queue preservation and multi-session coordination exist | Escaped-descendant regression demonstrates false retirement and completed deletion while a child remains alive |
| Credentials and usage | Verification, encrypted storage, refresh and quota UI exist; browser-account usage parsing was corrected | Setup-token input still uses API-key classification; finish supported credential-kind handling, expiry, usage eligibility and clear Harness scope |
| Platform coverage | Windows Job Object implementation and a macOS guest design exist | Native Windows recovery is unverified; macOS guest containment is not implemented or proven |

Source paths above are under `accounts-manager/runner/internal/runner`, `backend/internal/session_manager`, `backend/internal/service/chat` and the agent launch adapters. Reuse the public HTTP/CLI/UI controls already implemented and reviewed; extend only missing contracts.

### Evidence baseline, not new test results

[Published checkpoint](PUBLISH-CHECKPOINT.md) and `/tmp/pr-5769-publish-79.p3jxPQ/verification.json` record frontend tests, typechecks, Linux desktop packaging, backend/runner build and vet, full runner race, generated drift and the protected-path audit passing. The frontend result was 5,432 passing tests and seven skips.

Full backend race failed in five packages: persistenthost, tmux, daemon, activity observer and terminal. Full lint reported 86 backend findings and 27 runner findings. Current-main integration has six recorded conflicts. These are open gates, not a green release baseline. The HTTP shutdown fixture separately reproduced on unchanged control code; retain that diagnosis rather than changing unrelated shutdown behavior.

No implementation tests were rerun to write this plan. All 91 protected-file hashes were rechecked successfully. Historical bounded review clearances remain valid only for their exact tested scope and snapshot.

## 2. Architecture decisions

### Account choice and execution isolation

Use one supervised proxy with separate session capabilities and separate session controllers:

```text
Session A, chosen Account 1 -> controller A -> scoped route A -> Account 1
Session B, chosen Account 2 -> controller B -> scoped route B -> Account 2
Unrelated native session   -> unchanged native controller and login
```

The durable binding is the authority. Keep session ID, provider, explicit mode, account ID and binding revision distinct from controller generation, credential generation and operation ID. Check the applicable current identities at authorization, readiness and every destructive action. Never derive ownership from an account label, email, reusable process slot or PID alone.

If a managed account uses provider-owned native execution instead of the proxy, model that as an explicit execution strategy with its own capability checks and non-secret credential reference. Do not overload the existing native binding with an account ID or treat a managed profile as the device-global login. Its stop/admission obligations still belong to the selected managed account.

| Authentication/execution path | Isolation decision |
| --- | --- |
| Supported API-key/custom-provider proxy | Provider secret stays in the encrypted runner store. Each child receives only its restricted route capability and explicit provider configuration. Test that ambient device credentials cannot override it. |
| Provider-owned native sign-in | Use a dedicated managed execution context only through a supported native flow. AO may track a non-secret profile reference, not silently copy the device's credential store. It must remain separate from existing device-global native switching. |
| Managed app-server Chat | Use a separately owned process per session controller. Do not assume different conversation/thread IDs isolate a process-global authentication object. Add a managed-only adapter/composition path outside the protected native package. |
| Session configuration/history home | Allocate an AO-owned session home where shared configuration, refresh or history state would otherwise collide. Keep conversation data stable across controller generations; isolate writable credential state and serialize any legitimate same-account refresh. No writable shared `auth.json`, global environment mutation, or broad home copy. |

The current custom-provider authentication mechanism is supported by the official configuration contract. File-based login state lives under `CODEX_HOME`, while credential-store selection may instead use an OS store. Therefore changing a directory alone is not proof of credential isolation. Treat ephemeral/private credential-store support and administrator policy as version-specific requirements. [Authentication and storage](https://learn.chatgpt.com/docs/auth), [custom providers and state locations](https://learn.chatgpt.com/docs/config-file/config-advanced).

These are account-selection and lifecycle boundaries, not a claim that ordinary same-user processes form a hostile-code security sandbox. Tests must cover accidental ambient credential precedence; any stronger cross-process secret isolation claim requires its own enforced boundary.

### Setup-token and supported authentication contract

Do not implement setup-token support by relabelling every bearer token as an API key or enabling quota unconditionally. The documented setup-token flow grants model-request capability; it does not establish account-metadata or quota access. The provider's published credential-use restrictions also distinguish user sign-in to its unmodified native binary from a third-party application collecting or proxying subscription tokens. [Token capabilities](https://code.claude.com/docs/en/authentication#generate-a-long-lived-token), [credential-use boundary](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use).

Decision: keep supported API-key proxying, and resolve subscription support through provider-owned isolated native execution or an explicitly permitted integration. Do not expand subscription-token collection while that support gate is unresolved. Keep the existing user's stored data intact; do not silently delete, convert, enable or export it. A support decision must explain the migration/reconnect path before changing existing credential behavior.

Represent credential kind, verification outcome, expiry/refresh capability and usage capability separately. A prefix is a format hint, not authentication or identity proof. A valid model credential with unavailable usage must remain distinguishable from an invalid credential. A failed non-generating metadata probe must not become a false invalid-key verdict for a model-only token.

### Non-negotiable invariants

1. No first-account selection, round-robin, automatic fallback or silent managed-to-native conversion. Only a user choice or previously explicit saved default can bind a new session.
2. No provider request before the chosen binding and launch admission are durable. Stale routes and stale readiness acknowledgements cannot authorize or complete a newer generation.
3. Preserve queued work until delivered once or explicitly cancelled. Cancellation is allowed only before the durable irreversible boundary; an ambiguous response requires reading the operation, not guessing success.
4. Removal completes only after all positively associated executions and authorization workers are stopped/revoked. Runtime absence, parent death and missing ownership files are not interchangeable proofs.
5. Preserve Subscriptions and the 91 native-switching paths. Keep loopback listeners, private credential channels, thin HTTP clients and secret-safe diagnostics. No provider secret, route token or private runtime handle reaches public DTOs, telemetry or argv.

## 3. Delivery sequence

Each slice starts with an acceptance ledger and a failed-first regression for a substantiated defect. After the first green path: self-review, correct findings, rerun affected checks, freeze the exact source and evidence, then obtain independent review before expanding a dependent safety gate.

### P0. Integrate the base and establish the support matrix

**Entry:** preserve the published checkpoint, working data, previous red logs and review seals.

- Integrate the actual current `main` with a normal merge, not a force-push or a history rewrite. Resolve source conflicts in daemon construction, HTTP registration, session manager and migration checks. Regenerate OpenAPI/types and SQL artifacts from their sources. Never edit merged migrations or use blanket ours/theirs resolution.
- Record pre/post integration SHAs and classify every existing test/lint failure on the same toolchain. Reproduce tmux, observer and terminal failures with test-owned sockets, deterministic shell configuration and an untouched-main control. Do not edit the user's shell configuration or shared runtime services.
- Preserve original protected manifests. If upstream changed a protected file, separately record the upstream delta and prove this feature adds no protected-path edits. An unexplained hash change cannot be accepted as a new baseline.
- Record the supported matrix for both provider families, terminal/Chat, API-key/native-sign-in, OS and installed versions. Mark unsupported cells explicitly. Confirm the subscription-token boundary before implementing credential migration or native-profile launch.

**Exit:** reviewed integration baseline, explicit failure inventory and authentication matrix. No runtime or platform gate closes merely because the merge succeeds. This stage moves base reconciliation earlier than the old checkpoint order so fixes target the integrated code, while retaining the old snapshot as a comparison control.

### P1. Complete explicit A/B session creation and isolated execution

**Primary surfaces:** spawn service/DTO/CLI and desktop create-session flow; account service/binding store; child launch/Chat service; runner authorization. Protected native adapter files remain out of scope.

1. Add an explicit connection choice to new-session requests: managed account, native mode, or an explicitly saved default. Validate provider/mode and account generation before any provider work. Commit session creation and selection together, or persist a launch-blocking preparation state until selection commits. A post-spawn switch is not initial selection.
2. Pass that choice through every session-creation path, including programmatic/child sessions. Prevent native-login preflight from rejecting a valid managed selection. Omitted fields preserve existing behavior only where the user previously selected that default; missing choice in the managed workflow returns an actionable error.
3. Audit effective child configuration against conflicting inherited keys, alternative provider flags, auth helpers, login caches and keychain state. Normalize only the managed child's settings. Fail explicitly when enforced policy prevents the requested account. Apply the session-home decision above and verify resume/history lookup rather than copying global state.
4. Add the managed app-server process boundary and route support through a new adapter or existing public extension port. Keep native Chat unchanged. If the protected package exposes insufficient extension points, document the exact constraint and obtain a bounded design decision before editing it; do not quietly remove Chat from the completion scope.
5. Prove concurrent A/B traffic against two distinct controlled upstream identities through the production factory and actual runner. Include A-to-B switching while Session B continues, restart, stale capability rejection and unavailable selected account. A new account or changed default must not alter either existing binding.

**Exit:** initial choice is durable before first request; each enabled mode routes only to its selected account. Synthetic success is a local integration milestone. Two real provider identities remain a separate release check.

### P2. Fix switching readiness and recovery

**Primary surfaces:** daemon production execution tests, session manager switch/recovery, Chat handoff, runtime handle resolution and journal store.

1. Reproduce `TestAccountsManagerControlProductionExecution` for managed and native targets. Trace the actual readiness observation and generation acknowledgement. Determine whether the fault is fixture output, wiring or production detection before fixing it. Do not set readiness unconditionally, increase timeouts to hide a missing acknowledgement, or bypass the real adapter.
2. Bind ready acknowledgement to the exact operation, target generation, committed binding and usable controller. Old source output, a spawned PID or an inventory update is insufficient. Persist the proven empty-conversation decision before source stop; require an explicit new-conversation choice when history cannot resume.
3. Reproduce the cancellation 409/202 interleaving deterministically. Test cancel-wins and stop-wins separately. Reject stale retry admission after cancellation; release only that run's fences even when the durable commit succeeded but returning the response failed.
4. Retain the reviewed recovery defenses: reserved versus retired target identities, failed launch before metadata publication, no interrupt replay after durable stopping, exact ownership before every input/destroy retry, last tmux session absence and retained supervised-pane residue.
5. Assert actual queue and turn state through Manager-to-Chat handoff, startup cancellation and SQLite reopen. Stop-now must not require the expired source account to authenticate, interrupt a replacement, replay a completed task or lose pending input.

**Exit:** readiness, retry and both cancellation outcomes pass repeatedly through production construction, direct PTY and fallback runtime paths. Independent review clears switching before coordinated deletion is treated as safe.

### P3. Close exact deletion, containment and platform recovery

**Primary surfaces:** managed persistent host and platform owner adapters, runtime ports, deletion coordinator/store and runner admission/refresh lifecycle.

First preserve and rerun `TestProviderOwnerEscapedDescendantBlocksRetirement` in both exact-retirement and account-deletion modes. The fix must prevent a live descendant from coexisting with a successful retirement receipt, including a child forked after census that leaves the initial group. Another process scan alone is not a containment proof.

| Platform | Implementation and proof work | Exit requirement |
| --- | --- | --- |
| Linux | First feasibility candidate: a per-launch cgroup-v2 boundary with pre-execution membership and a supervisor/control boundary the provider cannot use to migrate out or rejoin after retirement. Prove permissions, namespaces, exposed broker endpoints and supported desktop availability before adopting it. Preserve stable identity and teardown authority across supervisor death. | Fork/group/session escape, attempted membership escape, parent death, replacement publication/shutdown and SQLite reopen all retire exactly the original execution. Kernel membership-empty proof and closed admission precede the stopped receipt. No broad PID/group fallback. |
| macOS arm64 and x64 | Continue the reviewed per-launch guest design. Implement only G1/G4 feasibility first: immutable pre-start identity, interrupted publication and same-boot keeper-death cleanup. Then add typed stop receipts/tombstones, all provider/tool execution inside the guest, file transport, native packaging/signing and desktop compatibility. | Both physical architectures prove exact guest termination after keeper death. Host-specific tools, worktree edits/history and signed package flows work. Design approval, helper disappearance and cross-compile do not satisfy this gate. |
| Windows | Preserve creation-time Job Object containment and strict ownership checks. Run the direct kernel contracts, all publication crash cuts, exact shutdown, reconnect and coordinator recovery on native Windows. Correct only failures supported by native evidence. | Original provider and descendants stop; foreign/replacement/native processes survive. Invalid handles fail the negative controls. Wine results cannot substitute for the native run. |

The Linux kernel documents tree-wide killing that handles concurrent forks, plus permission rules for containment. That is a primitive to validate, not proof that AO's current same-user launch is contained. If migration, host execution escape or deployment permissions cannot be constrained, reject this candidate and review a stronger boundary before product wiring. [Kernel containment and kill semantics](https://docs.kernel.org/admin-guide/cgroup-v2.html).

Preserve the [macOS design](MACOS-GUEST-CONTAINMENT.md) and [Windows runtime diagnosis](WINDOWS-RUNTIME-BLOCKER.md) as historical evidence. No native runners are currently established by this plan. Start host provisioning/coordination early; finish independent local slices while these release gates remain open.

Deletion must retain the existing saga and strengthen its acknowledgements:

1. Compute impact from every positively matched managed binding and recorded execution, including inactive/dormant sessions. Preserve native `account_id=''` records and unrelated accounts; never infer a match from a label or device login.
2. Validate the exact impact revision and confirmation. Commit a durable tombstone, block new admissions and preserve affected queues. Cancellation before the irreversible phase releases only its own fence; stale cancelled/superseded work does nothing.
3. Stop each exact execution and drain/revoke associated authorization leases and workers. Recheck durable admission immediately before irreversible steps. Preserve replacements even when they take a reusable slot after a failed stop. Do not turn a mismatched current slot into proof that all descendants of the original launch stopped.
4. After all stop/revoke receipts, remove the encrypted credential idempotently. Since runtime, vault and SQLite are separate resources, use the tombstone/journal across crash cuts; only final binding/default cleanup is a single database transaction. A missing credential or lost response cannot bypass outstanding stop obligations or choose a fallback account.
5. Reopen and retry after every phase boundary, including combined Chat-parent death and database reopen. Restore fences before accepting launches. Test corrupt, duplicate, incomplete, truncated, oversized, inaccessible and missing proof; interrupted starting/active/stopped writes; PID/group/session reuse; reboot versus sleep; stale snapshots; unanchored owners and late descendants.

Verify the existing joined automatic-refresh shutdown rather than rebuilding it. A blocked refresh, synchronous persistence and reconnect must join or be fenced before vault closure/removal; late callbacks, watchers and SDK persistence cannot resurrect the credential. Revocation must define ongoing-stream handling separately from new-request admission.

Do not relabel an old process as newly contained. Upgrade/adoption must discharge its original ownership obligation with valid proof or positively established reboot before admitting a replacement. Missing legacy proof is a visible recovery blocker, never a fabricated stopped receipt.

**Exit:** independently reviewed Linux correction and native platform receipts. A fail-closed unavailable state is safe behavior but does not by itself finish the requested platform feature.

### P4. Finish credential, usage, Harness and session controls

This slice can progress independently of native platform availability after the authentication contract is reviewed. Destructive controls stay capability-gated until their backend safety gate is clear.

| Boundary | Required work and failed-first coverage |
| --- | --- |
| Add/login/setup-token | Distinct supported input methods and typed credential capability. Cover invalid, expired, revoked, wrong-provider and insufficient-scope input. Validate without paid generation; unsupported non-generating validation returns a truthful limitation. Preserve identity on reconnect and reject stale callbacks. Browser-open errors must expose a safe provider URL/copy recovery, not an apparently successful no-op. Keep callback ownership and loopback redirect rules unchanged. |
| Existing misclassified tokens | Explicit, generation-checked migration/reconnect flow. Do not bulk reinterpret unknown strings, merge by email, read native credentials automatically or delete existing data. Unsupported subscription storage/transport stays blocked with an actionable native sign-in path. |
| Usage | Keep supported provider-reported windows, reset time and observation timestamp. Separate unsupported scope, invalid auth, permission, rate limit, offline and malformed response. No invented zero, unlimited state or quota derived from local token counters. Recheck account/generation/cache causality after replacement. Refresh only reads usage and never changes choice or native login. |
| Harness and initial picker | Show native login, managed-account availability, selected session account, runtime/interface support and provider installation as separate facts. A native signed-out indicator cannot falsely imply that a usable managed session is signed out, and adding a managed account cannot claim native login. Prefer a new managed-status component next to the existing controls. New-session choice must agree with P1, not apply a later cosmetic label. |
| Switch/removal presentation | Retain committed versus requested account, policy/timing, revision, operation/request IDs, pending/recovery/error state, retry/cancel and impact confirmation. Preserve unresolved operation IDs after transport failure. No optimistic success, no bodyless cancellation acknowledgement rendered as observed terminal state, and no local removal described as provider-wide revocation. |

Extend the existing service/port boundaries and generated public contracts. Preserve strict decoded JSON member names, duplicate rejection, explicit revision-zero presence, exact session/operation ownership, body limits and request-ID propagation. Keep the CLI a thin HTTP client. Localize all new text and cover keyboard/focus/screen-reader behavior.

**Exit:** focused UI/API/CLI tests and independent review, including stale inventory at large revisions. Users can distinguish account choice, actual authentication, unsupported usage and pending work without inspecting logs.

### P5. Prove the integrated product and prepare final review

Use three separate evidence levels: controlled protocol tests, actual local processes through production dependencies, and real provider/desktop runs. None substitutes for another.

| Scenario | Required evidence |
| --- | --- |
| Simultaneous A/B | Two distinct selected identities make requests concurrently, with a third unrelated native session. Identify actual upstream account use in a private test oracle, not merely by UI labels. Repeat for each supported provider and terminal/Chat mode. |
| Switch, retry and cancel | Idle/busy/expired/exhausted source, explicit drain or interrupt, failed target start, old readiness response and cancel/stop race. Verify conversation continuity or explicit new-conversation consent and preserved queue order without duplicate dispatch. |
| Recovery and revocation | Desktop reconnect, daemon/runner restart, SQLite reopen, controller death and delayed refresh. Old capabilities and deleted accounts cannot authorize or relaunch; unrelated account/native sessions continue. Include stale binding lease and stream-drain boundaries. |
| In-use removal | Multiple active and inactive sessions, partial stop failure, concurrent switch/delete, generation replacement, missing credential and lost completion response. No completed removal while any original owned execution can survive. |
| Real desktop | Follow the desktop development skill and preview/browser guides. Use an isolated Electron checkout/data root, real provider catalog and only authorized test credentials. Exercise add/select/switch/retry/cancel/remove/reopen, inspect screenshots and a short recording, and keep secrets/private labels out of shared evidence. |

Real account usage may incur charges or consume subscription quota. Use a minimal approved prompt only with an authorized test identity. Do not borrow unrelated sessions' credentials or claim live A/B verification from one account. Missing credentials/native runners remain named external gaps.

Retain the original specification's responsiveness targets rather than inventing a new definition of fast:

| Measurement | Required p95 |
| --- | --- |
| Action feedback; cached switcher usable | <=100 ms; <=200 ms |
| Local label/default persistence; row update | <=300 ms; <=500 ms |
| Already-authenticated route preparation; additional controlled proxy latency | <=100 ms; <=25 ms |
| Eligible no-restart warm switch; local restart/reconnect stage | <=300 ms; <=2 seconds |

Use the [specification's reference machine and sample protocol](../2026-09-26-accounts-manager-spec.md#5-performance-requirements): 50 accounts, 20 bindings, at least 100 warm samples and 30 cold starts. Report p50/p95, failures and full end-to-end times, including remote authorization, current-turn drain and guest startup separately. Mark an inapplicable fast path as such; do not exclude slow required work to claim the overall switch meets it. A containment design that misses the product target needs an explicit tradeoff review.

## 4. Verification and review protocol

Commands below are planned checks, not results from this planning task. Use the integrated branch's pinned toolchains and actual workflow environment; the published frontend baseline used Node 24.21.0 and backend Go 1.27.1. The desktop artifact workflow currently differs in Node major, so test its declared environment too rather than treating the local package as that job's result.

| Scope | Planned check |
| --- | --- |
| Defect reproduction | Preserve the failed-first log and exact source hash. Use ordinary Go/Vitest tests with deterministic barriers, not sleeps as the concurrency oracle. Assert real test selection and no skips. |
| Switching | From `backend`: `go test -v -race -count=3 -timeout=180s ./internal/daemon -run 'TestAccountsManagerControlProduction(Execution\|SwitchCancellation)'`, then affected session-manager/Chat/service/store packages and actual-process suites. |
| Deletion | From `backend`: `go test -v -race -count=3 -timeout=180s ./internal/adapters/chatdriver/persistenthost -run TestProviderOwnerEscapedDescendantBlocksRetirement`, then the complete host/coordinator and platform matrices. |
| Full backend | `go build ./...`, `go vet ./...`, `go test ./...`, and workflow-equivalent `go test -race -count=1 -timeout=20m ./...`; run required tagged process/CLI checks. Serial diagnostic runs do not replace CI scheduling coverage. |
| Runner and SDK | From runner: build, vet, full tests and full race; callback SDK package race; other embedded-engine checks required by the changed scope/workflows. Record upstream-only failures against an untouched engine control. |
| Lint | Complete pinned backend and runner lint, plus relevant frontend/locale checks. Resolve introduced findings, classify inherited ones, and rerun full lint. No broad suppressions or redefining success as changed-scope-only. |
| Contracts | `npm run api`, `npm run sqlc`, HTTP spec/parity tests and zero generated drift; preserve hand-mirrored CLI DTO compatibility and migration upgrade paths. |
| Frontend/desktop | Full frontend test suite, typecheck, E2E typecheck, required renderer E2E and actual Electron build/package. Classify skips explicitly; package success is not workflow success. |
| Preservation/security | Protected-path inventory, old seal integrity, native coexistence suites, secret scan, loopback/LAN negative controls, and public errors/telemetry redaction. |
| Supported platforms | Cross-build/vet as early diagnostics; native Windows and both native Mac architectures execute their ownership, recovery, compatibility and signed-artifact acceptance matrices. |

Resolve the five known failing backend packages and the full lint inventory against evidence, with no hidden test filtering. If an unchanged-main failure prevents a complete check, preserve the control and report the gate blocked; do not label the full check passed. Obtain independent review of the previously unconfirmed wiring/execution and credential/usage deltas as well as each new safety slice.

Every review handoff records the base/head, changed-file manifest and hashes, red/green logs, commands/toolchains, protected-path audit, actual-process evidence and open release gaps. An edit after freezing invalidates affected results and requires a new freeze. The final integrated review covers interactions between slices, not just the sum of earlier approvals.

## 5. Dependencies, handoff and completion

```text
P0 integrated baseline + authentication contract
  -> P1 initial selection / isolated A/B execution
  -> P2 readiness / switch recovery [independent review]
  -> P3 coordinated deletion + platform containment [independent reviews]

P0 -> P4 credential and UI corrections, coordinated with P1 contracts
P1 + P2 + P3 + P4 -> P5 complete production / desktop / platform proof
```

Open native-host availability and macOS feasibility early. They must not prevent independent local corrections, but they do prevent a full release-complete claim. Coordinated deletion cannot advance past a held switching or ownership review. Routine checks stay local and serial where they share runtime resources. Cross-session reviews must use AO coordination and exact frozen artifacts.

| User requirement | Delivery and release gate |
| --- | --- |
| Session A/Account 1 and Session B/Account 2 | P1 isolation and initial selection; P5 actual A/B proof |
| Switching readiness and deletion safety | P2 readiness/recovery; P3 exact retirement and saga |
| Setup-token, usage, Harness and initial choice | P0 authentication contract; P1 creation; P4 credential/UI boundaries |
| macOS containment and native Windows | P3 feasibility, implementation and native evidence |
| Restart, revocation and queue preservation | P2/P3 deterministic crash schedules; P5 production repetition |
| Main conflicts, tests, lint and final review | P0 merge/control baseline; complete checks and P5 independent review |

Completion requires every gate in [NEXT-IMPLEMENTATION-GATES.md](NEXT-IMPLEMENTATION-GATES.md), including native and live-provider gates. No numerical completion percentage or calendar promise is justified before the containment feasibility and native-host availability are established.

Plan self-review corrected five likely scope errors: rebuilding routing that exists; treating homes as sufficient isolation; omitting managed Chat and pre-launch choice; confusing runtime/vault/database cleanup with one atomic transaction; and calling a platform compile or bounded review full acceptance. This review evaluates the plan only. No implementation gate is cleared here.

Next implementation action: preserve the integration baseline and reproduce the two named production readiness and escaped-descendant failures unchanged. New publication requires a separately announced, explicitly approved action and refreshed real desktop evidence.
