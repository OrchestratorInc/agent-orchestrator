# Accounts Manager production-readiness specification

Date: 2026-09-29. Baseline: `d23bdaee42640da7d7fe70fd0b9af52843a3ca61`. PR: 5769.

Status: specification for the five remaining release areas. All five product gates remain OPEN. This document does not implement a correction, certify a platform, authorize publication or replace historical review evidence.

## Objective and scope

A user can run Session A with Account A and Session B with Account B concurrently, change either selection explicitly, and manage credentials without changing unrelated sessions or the device login. Successful switching means the requested account is ready for input. Successful removal means the account can no longer authorize local managed execution and all execution owned by it has been stopped.

This specification refines the [original product specification](../../2026-09-26-accounts-manager-spec.md) and the [completion plan](../parallel-session-delivery/PLAN.md). It groups the remaining work into the five areas requested by the user. Existing implementations and reviewed contracts are retained; a second proxy, replacement account manager or redesign of working controls is out of scope.

| Area | Required outcome | Current release status |
| --- | --- | --- |
| R1: safe deletion and crash recovery | Exact execution retirement, durable revocation and recoverable credential removal | OPEN: five positive recovery cases across three test groups remain failing |
| R2: switching and restart correctness | Recoverable pending switches, correct model state and preserved queued work | OPEN: observed pending-switch and post-restart display defects |
| R3: account isolation and credentials | Complete managed interfaces, isolated profiles and explicit migration/reconnect | OPEN: managed Codex Chat, isolated profiles and migration remain incomplete |
| R4: native platform acceptance | Verified Linux, Windows, macOS arm64 and macOS x64 behaviour | OPEN: Linux recovery is incomplete; native Windows/Mac acceptance is missing |
| R5: integrated release verification | One reviewed, reproducible, compatible and measured release candidate | OPEN: full-suite failures, main conflicts and missing final evidence |

### Evidence already available

The [live-account report](../parallel-session-delivery/user-flow/LIVE-RESULTS.md) records overlapping A/B replies, selected terminal/Chat switches, pre-stop cancellation, a preserved Chat queue, positive quota requests and daemon reattachment. It does not prove permanent removal, upstream billing attribution, permanent provider revocation or a cold runner/vault restart. Earlier terminal checks required an explicit model workaround.

The [picker repair](../ACCOUNT-PICKER-REPAIR.md) records explicit account/model/effort selection and a subsequent real Chat response without that workaround. Its final frontend run passed 5,632 tests with seven skipped; four affected backend packages passed their complete race suites. These results do not certify the still-failing persistent-host package. [Testing-handoff evidence](../testing-handoff/EVIDENCE.md) records the same source snapshot and its preservation checks.

Earlier results remain evidence for their exact scenarios and source revisions. Every release gate below requires final integrated evidence.

## Shared contract

1. Users choose the account, managed or native mode, switch timing and any new-conversation decision. No ranking, automatic fallback, first-account selection or substitution of a different model. An explicitly saved default applies only to future sessions that elect to use it.
2. Keep account ID, session ID, binding revision, credential generation, controller generation, operation ID and immutable launch identity distinct. A display label, email, PID or reusable runtime slot is not sufficient execution ownership proof.
3. Persist account selection before provider work. Validate current authorization on every logical request, including retries, tool continuations and streaming admission. An unavailable account must not borrow another account or native login.
4. Preserve the 91 protected paths, Subscriptions, native account switching, native credential homes, existing sessions and worktrees. Native bindings with an empty account ID are unrelated unless an explicit association exists. Do not infer one from provider or email.
5. Keep UI and CLI behind the public daemon contracts; keep coordination behind existing services and ports. The primary listener remains loopback-only. Public responses, telemetry, logs and argv must not expose credentials, route secrets, private endpoints or runtime handles.
6. Preserve encrypted storage, secure file access, generation-checked writes and joined refresh shutdown. No plaintext fallback. Add migrations where required; do not rewrite merged migrations or hand-edit generated contracts.
7. An unsupported capability is visible and blocks that action before execution. It is not evidence that the requested capability is complete. Missing native hosts or credentials leave gates OPEN rather than skipped-to-pass.

## R1. Safe deletion and crash recovery

### User-visible contract

Removal is local credential removal. It must not claim to revoke the provider's login, erase conversation history or cancel work already accepted remotely. Provider-side revocation, where supported, is a separate explicit action with its own observed result.

The user first sees an impact preview of every positively matched active, inactive and dormant managed binding. Confirmation is tied to the exact impact revision. A changed impact requires a new preview and confirmation. While removal is running, the account remains visible with its operation ID, progress, request ID on errors and recovery action. No optimistic disappearance or automatic rebinding.

### Required lifecycle

Use the existing `requested`, `stopping`, `revoked`, `recovery_required`, `cancelled` and `complete` phases. Preserve these durable boundaries:

| Boundary | Required action and proof |
| --- | --- |
| Admission | Atomically validate impact, reserve the operation and install a durable account tombstone. Block new bindings, launches, authorizations, reconnect publication and refresh publication for that account. |
| Pre-stop | Fence and preserve affected session queues. Cancellation is allowed only while durable proof says irreversible work has not started. |
| Stop boundary | Compare-and-swap the current removal generation before irreversible work. Persist stop intent before signalling or destroying anything. |
| Exact retirement | Stop and join every recorded original launch and all of its owned descendants. A replacement occupying or vacating the same slot cannot discharge the original obligation. |
| Revocation | Revoke route capabilities and authorization leases, cancel and join local in-flight request workers, and join refresh/reconnect writers. Persist each acknowledgement. |
| Credential removal | Delete only the selected encrypted credential after all retirement/revocation acknowledgements. Already-missing credential storage is handled idempotently, not as evidence that execution stopped. |
| Finalization | Atomically clear matched bindings and defaults and commit completion in the database. Keep session history and queued messages. Require a new explicit account choice before dispatching them. |

Runtime teardown, vault deletion and database cleanup cannot share one transaction. Use the existing durable journal across these resources; retry resumes unresolved obligations rather than repeating completed destructive actions. Preserve enough tombstone and operation evidence to reject delayed workers after completion. Do not garbage-collect unresolved obligations.

Admission blocks new work immediately. Existing local work may continue only until the durable stop boundary wins; cancellation before that boundary restores the original permitted queue flow. At stopping, cancel outstanding local upstream requests and join the owned workers. A remote service may already have accepted work: retain that uncertain outcome without claiming remote cancellation or replaying it. Timeout or inconclusive retirement enters `recovery_required`, retains the account fence and exposes a retry or concrete recovery reason instead of an endless success-looking spinner.

### Exact ownership requirements

- Contain execution from birth. Provider processes and their commands must not escape through a changed group/session, late fork, host execution broker or inherited control socket.
- Persist non-secret immutable launch identity and start permission before provider execution. Shutdown must prove that exact boundary stopped across host/helper death and replacement publication or shutdown.
- Revalidate exact ownership immediately before each input, interrupt and destructive retry. A fresh foreign, unknown, errored or mismatched probe forbids action against that slot. A mismatch may retire a slot obligation only when separate durable proof also discharges the original execution and descendants.
- Reject corrupt, duplicate-key, truncated, oversized, incomplete, missing or inaccessible ownership evidence. Missing descriptors/locks or process-group absence alone never prove original execution retirement.
- Cover interrupted start/active/stopped publication, cached receipts, PID/group/session reuse, unanchored owners and moved descendants. Distinguish a proven boot change from same-boot restart or sleep.
- Legacy uncontained launches retain their unresolved obligations. Do not retrofit a new boundary and pretend it contains old descendants. Offer truthful recovery instructions; never guess at process ownership.

### Acceptance cases

| ID | Required test and decisive result |
| --- | --- |
| R1-A | Retain `TestRemovalHostExactShutdownSurvivesReplacementAndReattach`, both `TestRemovalHostCrashReplacementCannotHideOriginalProvider` cases and both `TestRemovalHostCrashLateForkSQLiteRecovery` cases. All five complete safely using production containment; their assertions are not weakened. |
| R1-B | Deterministic late-fork and moved-descendant schedules keep removal incomplete while any original owned execution survives, including after replacement shutdown removes current ownership files. |
| R1-C | Multi-session deletion includes active, inactive and dormant matched records. Every required owner stops; unrelated B/native sessions, replacement processes and queues remain intact. |
| R1-D | Crash or lost-response injection at every lifecycle boundary, followed by actual SQLite reopen and daemon/runner reconstruction, converges to cancellation or completion when the required proof/resources are recoverable. Missing proof stays fenced and actionable; no unsafe completion, fallback or duplicate teardown. |
| R1-E | Cancellation racing a cached retry performs zero later stop, input, revocation or journal advance for the cancelled generation. Concurrent switch/delete, generation replacement and repeated retry preserve the winning durable intent. |
| R1-F | After completion, stale capabilities, recovered queues, delayed refresh/login callbacks, watchers and reconnect workers cannot authorize, recreate the account or relaunch a controller. |

Exit evidence: deterministic failed-first regressions for new defects; affected races repeated three times; complete persistent-host and coordinator suites; real process plus combined host-crash/database-reopen evidence; independent safety review. Native-platform acceptance is additionally required by R4.

## R2. Switching and restart correctness

### State and user experience

Keep the committed account separate from the requested target. Show the durable operation and its actual stage: requested/waiting, stopping, committed/starting, ready, cancelled or recovery required. A changed binding is not proof of readiness.

- Require explicit account/mode, expected revision and timing for programmatic changes. Interactive idle selection may use the existing two-action flow; busy sessions explicitly choose finish-current-response or stop-now.
- Preserve provider, model, effort, permission mode and conversation choice as distinct settings. Derive visible choices from the committed configuration and authoritative runtime state. If they disagree after restart, show reconciliation/error state and block inconsistent execution rather than silently selecting a different setting.
- Validate the target model against that account's advertised catalog. Changing account invalidates incompatible cached choices. Credential/quota uncertainty is not permission to switch accounts.
- Validate whether continuation is supported before stopping the source. Persist proven empty-history decisions. If a new conversation is required, obtain the user's explicit choice while preserving the old history.
- `ready` requires the exact reserved controller generation and functional protocol readiness, not process existence. A late acknowledgement from a retired generation cannot complete a switch.

### Recovery and cancellation contract

1. Daemon shutdown is not a user cancellation or permanent switch failure. Preserve recoverable intent and its original operation ID. Restart reconstructs fences and resumes only authorized steps.
2. A requested/waiting operation with no durable stop intent remains cancellable after restart. Once stopping won, cancellation returns a conflict with authoritative status. Do not infer the boundary from an in-memory worker flag.
3. A winning cancellation invalidates stale retry admission and releases only its own fences, even if the cancellation response is lost. No later launch, interrupt, destroy or revision change may come from that operation.
4. Retrying a failed launch preserves reserved and retired target identities. Reconcile both before any teardown. Do not replay a stop-now input after durable stopping already began.
5. Maintain the last committed binding after failures. Retry is idempotent and generation-checked. Changing the target after commitment is a new explicit operation, not rollback to an unchosen account.

Provide an explicit repair/retry path for existing operations incorrectly marked failed during shutdown. It may restore pre-stop recovery only when the journal proves stopping never began and account/revision ownership is still valid. Do not reclassify every terminal failure or cancelled operation. Include an upgrade fixture containing the already-stuck operation shape from the live report.

### Queues and authorization

Messages acknowledged as queued remain durable through drain, cancellation, interface transitions and restart. Bind queue adoption to one controller generation; preserve order and prevent duplicate local dispatch. If a new message is rejected during handoff, return an explicit conflict rather than acknowledging and losing it.

Before source execution stops, an active response stays on its existing authorization. Once the stop boundary wins, old authorization cannot begin another request. Newly admitted work uses only the committed account and revision. For a remote request with ambiguous receipt after a crash, preserve an uncertain outcome and require a supported reconciliation or explicit user action. Do not promise exactly-once remote execution or replay a possibly billed/tool-performing request without protocol support.

### Acceptance cases

| ID | Required test and decisive result |
| --- | --- |
| R2-A | Reproduce the observed waiting-switch shutdown defect, then prove graceful restart preserves retry/cancel according to durable stop intent instead of converting shutdown into terminal failure. Repair an existing affected journal without reopening genuinely cancelled or irreversibly failed operations. |
| R2-B | Restart an explicitly configured Chat session. Saved state, composer model/effort/permission and actual next execution agree; unsupported settings produce an explicit recovery state. |
| R2-C | Crash before/after stop, generation reservation/publication, binding commitment and readiness. Reopen storage and recover without touching a foreign replacement or accepting stale readiness. |
| R2-D | Run cancel-wins and stop-wins schedules in direct/fallback terminal and Chat paths. Cancel-wins makes zero later destructive/input calls; stop-wins retains a recoverable operation. |
| R2-E | Preserve an acknowledged queued message across cancelled drain, controller transition and cold recovery. No duplicate dispatch; unrelated B continues. Ambiguous remote receipt does not auto-replay. |
| R2-F | Repeat real A/B switches in both supported interfaces with conversation markers, expired/exhausted source and target-launch failure controls. Exercise daemon reattachment and full runner/vault cold restart separately. |

Exit evidence: the two observed defects have red-to-green regressions, all existing ownership/cancellation controls remain green, full affected races/process tests pass, and real restart evidence plus independent review cover the final integrated source.

## R3. Complete account isolation and credential handling

### Supported execution matrix

Maintain a versioned matrix of provider, credential method, terminal/Chat interface, platform and verified capability. A supported label requires the corresponding tests; a visible unsupported reason does not close a missing product requirement.

| Path | Required implementation boundary |
| --- | --- |
| Existing managed terminal and Chat integration | Retain account-scoped routes and independent controllers; repeat overlapping A/B proof on the final runtime. |
| Managed Codex terminal | Prove effective configuration precedence against conflicting ambient keys, helpers, settings and native login. Never start native execution before adding a managed binding. |
| Managed Codex Chat | Provide a dedicated managed app-server process/transport per controller, using the selected route. Keep its current rejection until creation, readiness, resume and exact teardown are implemented. |
| Isolated native credential profiles | Add a managed-only execution strategy with private writable account state. Do not call or modify the protected global native-switch path. A different home directory is insufficient unless effective keyring/helper/policy isolation is proven. |
| Terminal/Chat transitions | Preserve explicit selection and supported history. An unsupported target interface offers a supported path or explicit native choice, never an automatic conversion. |

For both distinct-account and same-account concurrent sessions, preserve independent request ownership. Same-account credential refresh is single-flight; a shared authenticated process is not a substitute for per-controller isolation.

### Credential and migration contract

- Browser/device login must open the real external authorization flow, retain the exact loopback callback contract and handle occupied ports, opener failure, expiry, cancellation and repeated callbacks. Provide a safe manual-link option when opening fails. Late callbacks cannot publish after cancellation/removal.
- API-key and setup-token inputs are different methods. Do not accept arbitrary text as verified or infer support, identity or quota permission from a prefix. Use supported non-generating verification where available; timeouts and insufficient scopes remain explicit inconclusive/limited states.
- Standard provider forms use their documented upstream default. Only supported custom endpoint modes expose an editable base URL. Never ask for AO's internal proxy URL; reject unsafe endpoint/redirect behaviour before transmitting a credential.
- Existing misclassified setup-token records require an explicit migration/reconnect preview showing identity, affected sessions and consequences. Do not silently rewrite the method, copy a personal profile or replace account identity based on matching email.
- Verify the intended identity and compare credential generation before replacement. Preserve the old credential on unsuccessful replacement unless deletion/revocation independently won. A different verified identity requires a separate user-approved account choice.
- Journal migration/reconnect so crashes and lost responses do not duplicate accounts or strand bindings. Serialize credential publication with refresh and deletion. A late successful refresh cannot resurrect a deleted credential.
- Managed proxy children receive scoped route authorization, not the stored provider credential. Isolated native profiles use the approved private storage boundary with platform access controls. No secret enters public DTOs, logs, argv, captures or diagnostic exports.

### Identity, quota and status

Show a verified email or safe label where the provider supplies one; otherwise show a stable safe identifier rather than inventing an identity. Managed account availability and device Harness login are separate states.

Authentication, generation readiness, quota eligibility and observation freshness are independent. Show real percentages/windows/reset timestamps and observation time. Unknown is not zero; generation unavailability must not hide a valid quota response. Revalidation must distinguish exhausted capacity, authentication rejection and transient errors before clearing or retaining an error state.

Do not label conversational token-budget answers as account usage. Exact remaining-token counts and integration with a provider's native `/usage` command are not promised when the provider contract does not expose them.

Cache by account, credential generation and observation format. Keep the existing request-causality fence for large rounded revisions: a stale pre-mutation response cannot overwrite new state, while a later valid rounded-equal snapshot must remain usable. Coalesce checks per account and cap independent provider checks at four.

### Acceptance cases

| ID | Required test and decisive result |
| --- | --- |
| R3-A | Production-launched A/B streams overlap in terminal and Chat with a native control. A controlled upstream records the exact chosen credential for every request; foreign ambient credentials and fallback candidates receive zero requests. |
| R3-B | Managed Codex Chat creates, resumes, switches and retires its own transport. Same-account parallel sessions do not share controller ownership or duplicate refresh. |
| R3-C | Isolated profiles survive restart with private file permissions and correct effective authentication. Global home/keyring/native controls remain unchanged; unsupported enforced policies fail before provider execution. |
| R3-D | Valid, invalid, wrong-method, expired and insufficient-scope credentials produce truthful states. Browser opening/callback and endpoint negative controls are exercised, without exposing secrets. |
| R3-E | Migration/reconnect succeeds for the same verified identity; mismatch, competing refresh/delete, crash and lost response preserve the correct generation and recoverable intent. |
| R3-F | Actual eligible credentials show positive quota. Exhausted/unavailable, unsupported, stale and offline cases remain distinct. Account switches and late cache responses cannot show another account's identity, model or usage. |

Exit evidence: completed capability matrix, deterministic and actual-process isolation tests, real authorized account pairs for supported live paths, preserved native state, and independent review. Lack of suitable live credentials remains an explicit external gate.

## R4. Native platform acceptance

No platform may claim production account management from compilation, a compatibility runtime, synthetic-only tests or a design review. R1's ownership contract applies on every advertised platform.

| Platform | Required mechanism and native evidence |
| --- | --- |
| Linux | Integrate the reviewed creation-time containment candidate into the production launcher. Prove exact retirement with direct PTY/tmux fallback, host/helper death, late/moved descendants, replacement preservation and SQLite reopen. The existing namespace experiment alone is not a usable runtime. |
| Windows | Preserve creation-time Job Object containment, immutable launch identity and strict ownership proof. Execute direct kernel-contract, publication/resume crash cuts, descendant cleanup and ConPTY/Chat recovery on native Windows. Do not weaken guards to satisfy a compatibility runtime. |
| macOS arm64 | Run the accepted per-launch guest feasibility experiment on native hardware. Prove exact same-boot retirement after keeper death before production adoption. Provider code and commands stay inside the guest; tombstones block restart. |
| macOS x64 | Independently prove the same contract on an Intel machine with its actual packaged dependencies. Translation on another architecture is not this acceptance result. |

The macOS gate includes interrupted identity/publication, guest/helper replacement, escaped-descendant controls, reboot semantics, registration cleanup, bounded workspace sharing, provider networking, terminal/Chat I/O and usable tools. No process-group-only or broad native teardown fallback. If feasibility fails, retain the failed evidence and HOLD the platform instead of reducing the ownership promise.

For every platform, record OS/architecture, provider and runtime versions, source and package hashes, test selection and discovered count. Exercise browser sign-in, explicit selection, simultaneous A/B work, switch/retry/cancel, in-use removal/recovery, sleep/restart/reboot and unrelated native coexistence with disposable data.

Distribution acceptance includes a real install, first launch, upgrade preserving bindings/vault/operations, helper/image compatibility and interrupted-update recovery. On macOS, use the repository's [artifact verifier](../../../../frontend/scripts/verify-mac-artifact.sh) and the established signing/notarization workflow. Do not mutate production signing or release state as a test. Unsupported downgrades must refuse unsafe state access with recovery instructions, not start a second writer against incompatible data.

### Acceptance cases

- R4-A: Linux production runtime passes the complete R1/R2 process and recovery matrices.
- R4-B: native Windows kernel and desktop matrices pass without substituting compatibility-runtime or compile results.
- R4-C: native macOS arm64 guest containment, desktop and packaging matrices pass.
- R4-D: native macOS x64 independently passes the same matrices.
- R4-E: each release artifact passes installation/update, protected-native coexistence and secret-safe diagnostics checks, with exact evidence and independent review.

Native resources may be arranged while local work proceeds. A missing host, SDK, signing context or account leaves only the corresponding execution gate blocked; it does not justify stopping unrelated implementation or marking the product complete.

## R5. Integrated release verification

### Candidate and compatibility requirements

Use one final integrated source snapshot. Resolve current main conflicts without discarding protected behaviour or rewriting shared history. Reconcile migration numbers with the actual base and test empty, existing, legacy and interrupted upgrades. Freeze source, generated contracts, protected paths and evidence manifests before final validation.

Retain public API/CLI/UI boundary coverage: operation/session ownership, exact member-name and duplicate validation, revision-zero presence, stale revision conflict, nonempty retry/cancel body rejection, body limits, request IDs and secret redaction. A bodyless login-cancellation response acknowledges a request; it must not invent terminal cancellation or credential revocation.

Do not modify unrelated server shutdown behaviour to hide the documented inherited fixture issue. Preserve same-toolchain untouched-control evidence. A failing required job is still a failed job; any exclusion needs explicit review and a corrected verification environment, not an undocumented green label.

### Required verification layers

These are future release checks, not commands executed while writing this document. Use pinned versions from the final applicable workflows and verify that selected tests actually run.

| Layer | Required check |
| --- | --- |
| Each corrected defect | Deterministic failed-first assertion, negative control and positive recovery control; focused race tests repeated three times, then complete affected packages. |
| Backend | From `backend`: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race -count=1 -timeout=20m ./...`. Run the separately tagged actual-process and CLI suites on applicable platforms. |
| Runner/embedded engine | Complete runner build, vet, ordinary and race suites; callback/auth and affected embedded-engine suites; full engine verification required by the final CI scope. Keep untouched controls for inherited failures. |
| Frontend/shared UI | From `frontend`: `npm test`, `npm run typecheck`, `npm run typecheck:e2e`, `npm run test:e2e:renderer` in the workflow CI environment, and `npm run build`. Run root `npm run product-ui:check` and other affected shared-package jobs. |
| Contracts/lint | Root `npm run api`, `npm run sqlc`, `npm run lint`; full runner lint and applicable locale checks. Generated output must match the frozen candidate. HTTP route/spec and CLI wire compatibility must pass. |
| Security/compatibility | Secret scan, telemetry/error/argv redaction, at-rest permissions, loopback/LAN negative controls, generation/lease revocation, all 91 protected-path hashes and unchanged native/Subscriptions behaviour. |
| Native/distribution | R4 suites plus applicable build-artifact, CLI-install and update jobs. Compile-only jobs remain labelled compile-only. Never execute publishing/deployment as validation. |

The final CI inventory comes from the candidate's workflows, including [backend](../../../../.github/workflows/go.yml), [frontend](../../../../.github/workflows/frontend.yml), [CLI](../../../../.github/workflows/cli-e2e.yml), [artifacts](../../../../.github/workflows/build-artifacts.yml) and [secret scanning](../../../../.github/workflows/gitleaks.yml). A static list here cannot waive a newly applicable job.

### Live workflow acceptance

Use an isolated real Electron checkout, disposable AO state, installed dependencies and a real provider catalog. Use authorized distinct test accounts and a bounded prompt/spend plan. Permanent removal/revocation tests use disposable credentials only after R1 safety review clears the relevant platform.

Run add/sign-in, invalid input, initial A/B choice, overlapping responses, account-scoped model/effort, positive quota, switch both directions, retry, cancel on both sides of the stop boundary, removal preview/confirmation/recovery, and restart. Keep a third native or unrelated managed session active as a control. Verify both user-visible state and actual execution outcomes.

Separate window restart, daemon reattachment, runner/vault cold restart and OS reboot in the report. Observe saved settings, readiness, queues and authorization after each. A UI label or prompt echo is not an execution identity oracle. Controlled upstream tests must establish exact routing; live observations must state whether account attribution is independently visible or inferred from bindings and disable controls.

Inspect real screenshots and a short workflow recording, with personal information and private connection details excluded. Cover keyboard operation, focus restoration, narrow-window removal actions, unavailable-service states and recovery controls. The previously observed Settings clipping remains a tracked usability defect until corrected or proven unrelated and explicitly dispositioned.

### Performance requirements

Retain the original measurement boundaries: release desktop build; recorded reference machine with at least four cores, 16 GB RAM and SSD; 50 saved accounts and 20 bindings; at least 100 warm samples and 30 cold starts. Synthetic accounts are acceptable for scale measurements but do not count as live-provider acceptance. Record p50, p95, failures and raw samples.

| Measurement | Required target |
| --- | --- |
| Input to visible feedback | p95 <= 100 ms |
| Cached switcher or warm Accounts list usable | p95 <= 200 ms |
| Persisted local label/default rendered | p95 <= 300 ms |
| Accepted snapshot to row painted | p95 <= 500 ms |
| Authenticated route preparation, excluding process start/remote refresh | p95 <= 100 ms |
| Eligible warm idle switch without restart/remote refresh, through input-ready | p95 <= 300 ms |
| Additional gateway time to first event against controlled streaming upstream | p95 <= 25 ms |
| Local controller restart/reconnect stage | p95 <= 2 seconds |
| Cold Accounts shell / usable snapshot | <= 300 ms from navigation / <= 1 second after daemon readiness |

Report full switch latency separately, including drain, provider refresh and guest startup. A path requiring restart cannot claim the no-restart target. Slow/unavailable providers must not block interaction; status checks have a ten-second deadline. Do not hide failures or external waits when calculating the user-visible result.

### Release acceptance

- R5-A: the candidate integrates the actual base, preserves protected paths and passes migration/contract compatibility checks.
- R5-B: all required integrated suites and remote jobs have observed results on the exact candidate; failures and skips have explicit, reviewed dispositions. Required acceptance cases cannot pass by skipping.
- R5-C: live desktop workflows, actual-account isolation and every required restart/removal/revocation path have evidence with clearly stated limits.
- R5-D: performance and accessibility/usability requirements pass on the recorded release build; unresolved responsiveness or clipped critical controls remain blockers.
- R5-E: an independent reviewer clears safety, credential isolation, recovery and integrated behaviour on the exact final manifest. Publication remains a separate explicitly approved action.

## Delivery and review order

| Round | Deliverable and review boundary |
| --- | --- |
| 1 | Preserve the current seals and failing tests. Review the R1 containment/retirement contract, then implement Linux recovery. Obtain independent safety review before real deletion tests. |
| 2 | Correct R2 pending-switch restart and model reconciliation, including cold recovery and queue/authorization tests. Review after the first complete recovery path and before broad integration. |
| 3 | Complete R3 managed Chat, isolated profiles and migration. Design review precedes new execution paths; midpoint review follows the first overlapping A/B path. Enable capabilities only after their platform safety prerequisites pass. |
| 4 | Complete native R4 proofs and packaging. Arrange native resources early; safe independent experiments can run alongside rounds 1-3. No platform claim before its execution gate passes. |
| 5 | Run R5 full integrated acceptance, fix substantiated findings, refreeze and obtain final independent review. No merge/release claim while R1-R5 remain open. |

For each round, preserve red logs for substantiated defects, self-review after initial green, rerun affected tests after every correction, and provide the exact changed-file manifest to the reviewer. An edit invalidates affected pre-edit evidence. Do not modify an old seal to match new source.

Every acceptance record must identify requirement ID, source/package hash, OS/architecture/toolchain, command or manual procedure, discovered/pass/fail/skip counts, decisive oracle, negative control and reviewer verdict. Secret-free reviewer-accessible evidence accompanies release handoff; private local logs alone are not a published review package.

## Traceability and completion decision

| This specification | Existing completion-plan work and gates |
| --- | --- |
| R1 | W2/W5; R01, R02, D01, D02 |
| R2 | W5/W6; S01, U01, E01 |
| R3 | W0/W1/W3/W4/W6; A01-A03, C01-C03, U01 |
| R4 | W2/W7; R03, R04 and native portions of E01/V01 |
| R5 | W0/W6/W7; B01, E01, P01, V01, V02 |

The [existing product ledger](../parallel-session-delivery/GATES.md) retains its history. Future implementation evidence must reference these R1-R5 acceptance IDs as well. The separate [document ledger](GATES.md) only records completion of this specification.

Production readiness requires all five product gates to pass on one integrated release candidate. No percentage estimate, UI success, safety refusal, compile pass or earlier bounded review substitutes for an unmet gate. Until then, testing is limited to the scope and cautions in the [cross-platform testing guide](../../../testing/accounts-manager.md).
