# Parallel session accounts: completion plan

Date: 2026-09-29. PR: 5769. Inspected HEAD: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`, plus the preserved, uncommitted retirement correction.

This is the current remaining-work plan. It consolidates the original specification, subsequent implementation records and the latest safety review. Older reports and seals remain historical evidence and must not be rewritten. Writing this plan does not complete an implementation gate or authorize publication.

## 1. Deliverable

A user chooses Account 1 for Session A and Account 2 for Session B. Both run concurrently, including terminal and Chat interfaces once their support gates pass. Changing, reconnecting or removing Account 1 must not change Session B, the device login or an unrelated native session.

- Users choose accounts, authentication mode, switch timing and whether to start a new conversation. AO never selects a replacement account automatically.
- Account creation, verification, refresh, usage, switching and removal have accurate pending, committed, unavailable and recovery states.
- No deletion success while execution owned by the deleted account can survive. No old capability, delayed refresh or recovered queue can resurrect access.
- Preserve Subscriptions, existing native switching, user credentials, worktrees and all 91 protected paths.
- Build on the embedded library and existing service boundaries. Do not build a second proxy or reimplement already working public controls.

The [acceptance ledger](GATES.md) is the completion checklist. All final product gates remain open until evidence for the integrated tree exists.

## 2. Current state, with evidence limits

| Area | Already implemented or demonstrated | Work still required |
| --- | --- | --- |
| Session-aware routing | Durable session/account/revision bindings, separate route capabilities and exact-account selection exist. | Concurrent upstream identity proof through real production launches, all supported interfaces and restart. |
| Initial selection | Atomic pre-launch selection is wired through creation, prepared sessions, CLI and desktop. | Current independent review and positive desktop/provider execution. Audit every background/programmatic creation path. |
| Codex terminal isolation | Incomplete managed routes reject before native fallback; explicit private route and process-memory credential configuration. Installed-binary synthetic tests pass. | Ambient keys/helpers, enforced policy, keyring, history/resume, version compatibility and actual simultaneous requests. |
| Managed Chat | One provider has a routed Chat path. Managed Codex Chat is deliberately rejected. | Add a separately owned managed process/transport with exact lifecycle identity. Do not enable it by removing the rejection alone. |
| Switching | Production wiring, journals, queue fences and cancellation corrections exist; focused production race checks pass three times. | Current independent review, integrated readiness/recovery matrix and real concurrent A/B behavior. |
| Removal | Tombstone, multi-session coordination, restart journal and final cleanup exist. | Production containment and positive exact-retirement recovery. The latest safety guard prevents unsafe success but leaves three full-package test groups failing. |
| Credentials and usage | Encrypted storage, browser/device flows, verification and refresh exist. Wrong-form token input is rejected; legacy display, quota eligibility and Harness scope were corrected. Usage summaries are already in the session dropdown. | Supported isolated sign-in profiles, explicit legacy migration/reconnect, real browser flow, positive quota evidence and integrated cache/error behavior. |
| Public controls | API, thin CLI and desktop picker/status/retry/cancel/removal controls have bounded review history. | Missing capability integration and complete positive desktop flows against the final runtime. |
| Integration and lint | A normal main merge, migration collision repair and full pinned lint correction have recorded passing checks. | Recheck current base/remote state before final integration; run full final-tree suites and independent review. Do not repeat old conflict counts as current facts. |
| Platforms and speed | Linux containment has a synthetic feasibility experiment. Windows Job Objects are implemented. Mac guest design is feasibility-approved only. Runner timing is measured. | Production Linux containment, native Windows acceptance, Mac guest implementation/native proof, complete desktop/controller latency. |

Primary baseline records: [execution record](../NEXT-EXECUTION.md), [route isolation](../NEXT-CODEX-ROUTE-ISOLATION.md), [credential experience](../NEXT-CREDENTIAL-EXPERIENCE.md), [latest retirement review](../RETIREMENT-CORRECTION-REVIEW.md). Their passing checks apply only to the recorded snapshots and scopes.

The nine-file uncommitted correction is preserved in `/var/tmp/pr-5769-retirement-fix-79.x3Z1f1/final/snapshot.sha256`, manifest SHA256 `419a81dee0f0dd371e70d124d8143975a253e9e8672a15771e04d4247257f861`. Planning-time hash checks matched this snapshot, the current 91 protected files, 33 generated files and the five-entry guest-design package. No implementation suites were rerun to write this document.

### The immediate blocker

The latest correction refuses to trust original-process-group absence or a cached same-boot group-only receipt. This closes an unsafe acknowledgement path. It does not supply the missing containment mechanism.

Retain these unchanged positive completion tests until real containment makes them pass:

- `TestRemovalHostExactShutdownSurvivesReplacementAndReattach`
- `TestRemovalHostCrashReplacementCannotHideOriginalProvider`, both replacement modes
- `TestRemovalHostCrashLateForkSQLiteRecovery`, both replacement modes

The latest full persistent-host race run records 48 passing top-level tests and these three failing groups, containing five failing cases. Build, vet, selected coordinator/adapter races and platform compilation pass on that seal. Those passes do not override the failed package.

The preserved escaped-descendant and cached-receipt negative checks now pass. Do not reproduce them as if still unfixed, weaken the three positive tests, or call refusal to delete complete recovery support.

## 3. Architecture and support contract

```text
Session A -> controller A -> session-scoped authorization -> chosen Account 1
Session B -> controller B -> session-scoped authorization -> chosen Account 2
Native C  -> existing native controller and device login, unchanged
```

The durable binding is authoritative. Keep session ID, account ID, provider, explicit mode and binding revision distinct from credential generation, controller generation, operation ID and exact launch identity. A label, PID or reusable slot is not ownership evidence.

1. Persist selection before any provider request. Changing a saved default affects only future sessions that explicitly use that default, never existing bindings.
2. Managed proxy children receive restricted route capabilities, not stored provider secrets. Admission checks the current binding, revision, credential generation and deletion state on every logical request, including streaming/retry paths that can bypass ordinary SDK persistence handling.
3. Use a separately owned process for each managed Chat controller. A thread ID inside a shared authenticated process is not account isolation.
4. Use private configuration/history or credential storage only where required by the provider contract. A different directory alone cannot defeat an OS credential store, helper or enforced policy. Never copy the entire personal home or mutate global login state.
5. A managed account using provider-owned native execution remains a managed execution strategy with account obligations. Do not overload unrelated native bindings whose `account_id` is empty.
6. Managed execution requiring exact deletion must be contained from birth. A replacement's proof must never discharge the original execution's unresolved descendants.

Before adding new authentication behavior, record the supported provider/version/credential/interface/platform matrix. Verify version-sensitive behavior against installed binaries and current official contracts. Keep a reason and recovery instruction for each unsupported cell; do not silently remove a requested cell from the release gate.

| Authentication path | Required decision and boundary |
| --- | --- |
| API key through the proxy | Provider-backed validity check, correct endpoint, exact selected identity, no credential in argv/public responses/logs. |
| Browser/device sign-in | Provider-supported integration, callback ownership and credential lifecycle verified end to end. |
| Setup token or subscription sign-in | Establish an explicitly supported integration or isolated provider-owned sign-in path. A token prefix proves neither validity, quota scope nor permission to proxy it. |
| Existing misclassified record | Preserve secret bytes, identity and current state until an explicit migration/reconnect succeeds. Do not silently relabel it into a supported execution path. |

If a supported path cannot be established, keep that product requirement on HOLD with the concrete limitation. Do not equate a truthful unsupported message with implementing the requested account execution feature.

## 4. Delivery sequence

Each work package starts with explicit acceptance cases. For a substantiated defect, preserve a deterministic failed-first assertion, then implement the narrow correction. Self-review after the first complete interaction or lifecycle path; fix findings before expansion. Freeze exact files and obtain independent review before dependent safety work proceeds.

| Work package | Dependency and review boundary |
| --- | --- |
| W0: baseline and support decisions | First. Start native-host and authorized test-account coordination immediately. |
| W1: concurrent terminal account proof | After W0; can progress while containment is reviewed. No new managed Chat launch yet. |
| W2: exact containment and platform lifecycle | Review the existing safety guard and containment contract before runtime adoption. Native platform experiments start early. |
| W3: managed Chat and isolated sign-in execution | W0 support contract and W2 exact launch/retirement capability. |
| W4: credential lifecycle and usage | Independent bounded corrections after W0; profile migration depends on W3. |
| W5: integrated switching, removal and recovery | W1-W4 relevant capabilities, plus independent switching and ownership CLEAR. |
| W6: public controls and real desktop | Independent display checks may run earlier; complete positive flows require W5. |
| W7: performance, full verification and final review | All required runtime paths implemented; native and live-provider evidence are mandatory. |

### W0. Preserve the baseline and settle support decisions

- Verify the current dirty correction, earlier archives, 91-path protection manifest, generated manifest and guest-design package before edits. Preserve the latest retirement seal separately from any follow-up correction.
- Inventory current base/head, migrations, required workflows and outstanding review verdicts. Request current independent review of integration, initial selection, switching, route configuration and credential deltas where delivery was recorded but no verdict was observed.
- Use a normal merge if another base integration is needed. Never rewrite shared history or merged migrations. Retain legacy account migration remapping, transaction rollback and schema-proof negative controls.
- Classify failures on matching toolchains and fixtures. The inherited HTTP shutdown fixture diagnosis stays documentation-only; do not change shared shutdown behavior merely to obtain a green command. Preserve the earlier unclassified ownership-test timeout for final repeated checks.
- Record the support matrix above and secure native Linux/Mac/Windows testing resources. Lack of a native runner does not block unrelated local work, but leaves the corresponding release gate open.

Exit: reproducible baseline, approved scope/support contract, explicit evidence gaps and no discarded user data.

### W1. Prove Session A/Account 1 and Session B/Account 2

Primary source: runner `route_bindings.go`, `route_capability.go`, `route_handler.go`; account-service `bindings.go`; session-manager `accounts_manager_selection.go` and `accounts_manager_routing.go`; existing daemon production tests.

1. Extend the production-construction harness with two synthetic upstream identities and a third native control. Use barriers to keep both A and B streams active simultaneously. Assert actual upstream credential identity, not labels or merely sequential requests.
2. Exercise creation, prepared promotion, CLI and programmatic creation. Missing, removed, wrong-provider, stale or unavailable choices must cause zero provider execution. Never insert a cosmetic binding after a native process has already started.
3. Deliberately conflict inherited keys, helper configuration, auth caches and provider flags. Inspect effective managed configuration before admission. Unsupported/enforced policy rejects without fallback. Keep native control state byte-for-byte intact.
4. Cover same-account parallel sessions too, refresh single-flight, model discovery, streaming, tool continuations and retries. Unauthorized/stale/expired/exhausted A must never use B, another saved account or the device login.
5. Reopen daemon/runner/database state and resume both sessions with the same explicit accounts. Old route tokens and stale inventory fail; no global account selection is introduced.

Exit: repeated controlled and actual-local-process A/B proof for terminal mode, with exact request counts and unchanged native state. Live-provider acceptance is separate in W6/W7.

### W2. Finish contained launch and exact retirement

Primary source: `backend/internal/adapters/chatdriver/persistenthost`, runtime ownership ports and adapters, deletion coordinator/store. Preserve [Linux feasibility evidence](../NEXT-LINUX-CONTAINMENT-FEASIBILITY.md), [Mac design](../MACOS-GUEST-CONTAINMENT.md) and [Windows diagnosis](../WINDOWS-RUNTIME-BLOCKER.md).

Common contract:

- Publish immutable non-secret launch identity and durable start permission before provider execution. Containment must apply at creation, not after an escapable child has already started.
- Bind stop authority to the exact original boundary across daemon/host/helper death. Join all owned execution before recording retirement. Close admission with a durable tombstone so a stale keeper cannot restart it.
- Keep provider/tools away from host execution brokers, inherited control sockets and mutable containment authority. Provide narrow workspace, network and proxy access without reopening escape.
- Cover post-census forks, changed process group/session, moved descendants, original host-parent death, replacement publication/shutdown and simultaneous SQLite reopen. Replacement and unrelated native processes must survive.
- Reject incomplete, corrupt, duplicate-field, truncated, oversized, missing/inaccessible and foreign proof. Cover interrupted starting/active/stopped writes, PID/group/session reuse, unanchored owners, reboot versus same-boot sleep and cached receipts.
- Legacy uncontained execution retains its original obligation. Do not attach it to a new boundary or treat missing files as retirement proof. Document valid boot-change recovery and truthful unresolved recovery without destructive guesswork.

| Platform | Implementation and acceptance |
| --- | --- |
| Linux | Turn the reviewed creation-time boundary candidate into a durable production launcher. The external namespace experiment has no usable workspace/network and is not a runtime. Prove start-permit crash cuts, independent helper identity, closed migration/host-execution paths, exact empty-boundary acknowledgement, desktop-compatible tools and packaged prerequisites. A bare user-manager group was shown insufficient; do not adopt it as the final boundary. |
| macOS arm64 and x64 | Run the accepted G1/G4 per-launch guest feasibility experiment first. Establish exact same-boot retirement after keeper death, interrupted publication and replacement preservation before production integration. Then implement all provider commands inside the guest, durable permits/tombstones, safe file/network transport, signed helper/image packaging, updates, both architectures and desktop compatibility. No native process-group fallback. |
| Windows | Preserve creation-time Job Object containment, global launch identity, no breakaway and strict kernel proof. Execute the direct contract and complete publication/resume/shutdown/recovery matrix on native Windows. Preserve the compatibility-runtime red logs; do not weaken correct ownership checks to satisfy that runtime. |

Midpoint review: start identity, escape prevention and cold stop proof before coordinator integration. Exit: the three retained positive recovery test groups and all negative controls pass on production boundaries; each advertised platform has native runtime evidence. Safe refusal alone leaves this package incomplete.

### W3. Complete managed Chat and isolated profile execution

Primary source: Chat service/controller, new managed-only adapter composition, existing account launch router and exact persistent transport ports. Keep protected native account-switch and Chat adapter behavior unchanged.

1. Add a dedicated managed Codex app-server process per controller, using explicit effective provider/configuration and a scoped route. Bind readiness, protocol transport, stop and resume to its immutable launch identity.
2. Reject missing capability, stale generation or incompatible provider version before a thread/model request. Validate real app-server readiness; process existence or an open port is insufficient.
3. Preserve session conversation/history separately from controller generation. Verify reuse/resume under the selected account. If account changes cannot safely resume a conversation, require explicit new-conversation choice before stopping the source.
4. Implement provider-owned isolated sign-in profiles only after the W0 support decision. Keep private writable auth state separate across accounts; serialize legitimate refresh for shared-account sessions. Do not import ambient login state automatically.
5. Reuse the W1 overlapping A/B harness for Chat, terminal-to-Chat transitions and back. A failed launch must not reuse another account's process or transport.

Exit: both interfaces have real separately owned execution, exact teardown, restart/resume and configuration-precedence evidence. Enable public capability flags only for completed paths.

### W4. Finish credentials, sign-in, usage and migration

Primary source: runner `credential_*` and OAuth coordinator, management client/capabilities, account service, `AccountUsage.tsx`, account hooks and settings controls. Extend existing implementations rather than replacing the vault or login coordinator.

- Browser sign-in: exercise the real Electron open-external action, correct authorization URL, listener ownership and exact provider redirect on loopback. Handle occupied ports, opener failure, manual-link fallback, pending/expired operations, duplicate callback, cancellation and restart. Never place credentials in callback logs or public diagnostics.
- API-key input: keep wrong-form rejection and add real valid/invalid/insufficient-scope controls. Provider verification must not accept arbitrary text or confuse unavailable metadata with an invalid model credential. Use non-generating checks where supported; expose inconclusive results honestly.
- Base URL: distinguish the public upstream endpoint from AO's private local proxy URL. Standard provider forms use their supported default. Validate explicitly supported custom endpoints and TLS/redirect behavior before transmitting a secret; never ask users to paste an internal route token or callback address.
- Legacy setup-token records: preview the affected identity/sessions, obtain explicit reconnect or migration intent, compare verified identity and generation, preserve the previous credential on failure, and fence concurrent refresh/switch/delete. Keep an unresolved operation recoverable after a lost response. No silent secret copying, enablement or conversion.
- Refresh and storage: reverify encryption, secure file permissions/atomic writes, per-account single-flight, reconnect compare-and-swap, watcher/SDK write admission, restart recovery and joined automatic refresh shutdown before vault closure. Delayed callbacks cannot publish after cancellation/removal. Unsupported secure storage must not fall back to plaintext.
- Usage: keep credential validity separate from quota eligibility. Show available quota/reset windows plus observation time, or specific unsupported/permission/rate-limit/offline states. Unknown is not zero. Cancel stale account/generation/format requests and keep A/B cache keys separate. Dropdown reads use cached/coalesced snapshots, not an unbounded provider request per row. Deduplicate checks per account and cap independent provider checks at four, as specified.
- Harness: continue displaying device sign-in separately from managed-account availability and interface capability. A usable managed account must not falsely imply device login; a device logout must not reject an independently supported managed route.

Exit: invalid input cannot become usable, the supported sign-in path works, existing accounts have a safe migration/reconnect path, and live quota success is demonstrated only for credentials that actually support it.

### W5. Close switching, deletion and recovery as one product

Primary source: `accounts_manager_switch.go`, `accounts_manager_recovery.go`, `accounts_manager_removal.go`, Chat handoff/removal services, SQLite journals and runner authorization.

Switching acceptance:

- Preserve committed account while showing requested target. Require explicit target, expected revision, timing and conversation decision. Retry the same durable operation without creating a second switch.
- Check exact-owner proof immediately before input/interrupt and every destructive retry. A takeover after a failed stop permits no further input or destruction of the replacement. Missing, unknown or mismatched proof fails closed.
- Keep reserved and retired controller generations distinct across failed launch/publication. Stale readiness cannot commit; preserve empty-conversation proof across retries and last-session terminal-server absence controls.
- Preserve Manager-to-Chat queues through startup cancellation and cold restart. Winning pre-stop cancellation releases only its own fences and prevents every later restart, interrupt, launch or revision rotation. Do not replay input after the durable stopping boundary.
- Cover expired/exhausted source accounts, busy drain/stop-now, target failure and valid same-owner controls. Stopping must not require the failed source account to authenticate again.

Removal acceptance:

1. Preview all positively matched managed bindings and executions, active, inactive and dormant. Empty-account native bindings and unrelated sessions are excluded without identity inference.
2. Require exact impact revision and confirmation. Commit the blocking tombstone, reserve coordinator ownership and preserve affected queues before irreversible steps.
3. At each irreversible boundary, compare-and-swap the durable removal generation. A stale waiter after cancellation performs zero stop, revocation, journal advancement or replacement completion.
4. Stop/join every exact execution, revoke routes and drain associated in-flight authorization/refresh workers. A different owner in the current slot does not prove the old owner's descendants are gone.
5. Remove the encrypted credential idempotently only after all acknowledgements. Runtime, vault and database cannot share one transaction: use the journal across them, then atomically clear bindings/defaults in the final database transaction. Never choose another account.
6. Recover before admitting new work after restart. Include credential already missing, lost commit/cancel response, partial stop failure, retry repetition, concurrent switch/delete and generation changes at every phase.

Explicitly define ongoing-stream revocation separately from rejecting new requests. Queued work remains paused after removal until the user rebinds it or cancels it. Prove no automatic dispatch under another account and no duplicated delivery.

Exactly-once assertions apply to local queue adoption and dispatch ownership. When a crash makes upstream receipt ambiguous and the protocol has no idempotency mechanism, preserve an uncertain state and do not automatically replay it. Do not promise exactly-once remote execution without provider support.

Exit: reviewed production A/B/native schedules, combined host-crash plus SQLite reopen, all crash cuts and full affected race/process suites pass. Independent switching and ownership HOLDs must be cleared before coordinated deletion is accepted.

### W6. Complete the public and real desktop workflows

Primary source: existing account controllers/DTOs, thin CLI commands, generated API contract, `SessionAccountControl`, `InitialAccountPicker`, `AccountRemovalControl`, `AccountUsage`, query hooks and locales.

- Close missing behavior only. Preserve exact JSON member/tag validation, duplicate rejection including Unicode aliases, explicit revision-zero presence, body limits, session/operation ownership, request IDs, safe DTOs and loopback/LAN control separation.
- Keep CLI usage errors distinct from daemon/runtime errors. A bodyless cancellation response acknowledges a request; it does not prove terminal cancelled state or revoke a credential.
- Show capability/unavailable states, committed and requested account, revision, policy, operation/request IDs, retry/cancel and exact-revision removal impact. No optimistic switch/remove success or silent fallback.
- Retain large-int64 request/cache causality tests: reject a stale pre-mutation inventory even when numbers round equally, while accepting a later valid snapshot. Preserve unresolved operation IDs after transport failure.
- Localize new messages and test keyboard/focus/screen-reader behavior. Defaults and explicit native choice remain visible and user-controlled.
- Follow the desktop-development skill plus preview/browser guides. Launch the real Electron app in an isolated checkout with scratch data, a real provider catalog and its own installed dependencies. Never use a reconstruction or the user's active data as evidence.
- Exercise add, browser open, verification, initial A/B choice, simultaneous work, usage, switch, retry, both cancellation outcomes, removal impact/recovery and desktop/daemon restart. Inspect screenshots and a short workflow recording. Capture negative paths and real positive paths separately.

Live runs require two authorized distinct test identities per provider/authentication mode under test, a bounded prompt/spend policy and secret-safe identity observation. Synthetic identities establish protocol behavior, not live account support. Missing credentials remain an explicit external gap.

Exit: the user can perform the complete flow without terminal workarounds; actual identities and preserved native state agree with the UI. Independent UI/integration review follows the exact seal.

### W7. Measure, verify and obtain final review

Keep the [original performance boundaries](../../2026-09-26-accounts-manager-spec.md#5-performance-requirements): release desktop build, recorded machine with at least 4 cores/16 GB RAM/SSD, 50 accounts, 20 bindings, 100 warm samples and 30 cold starts.

| Measurement | Required p95 |
| --- | --- |
| Input feedback | <=100 ms |
| Cached switcher and warm Accounts list usable | <=200 ms |
| Confirmed local label/default persistence | <=300 ms |
| Accepted snapshot to painted row | <=500 ms |
| Complete authenticated route preparation, excluding process start/remote refresh | <=100 ms |
| Eligible no-restart warm switch to committed/input-ready | <=300 ms |
| Additional first-response gateway latency | <=25 ms |
| Local controller restart/reconnect stage | <=2 seconds |

Also require the cold Accounts shell within 300 ms and a usable local snapshot within 1 second of daemon readiness. Status checks use the specified 10-second deadline without freezing the page. Report full end-to-end time, p50/p95, failure counts, current-turn drain, provider refresh and guest startup. Do not hide those costs to claim a fast switch. Existing runner-only measurements do not clear this gate.

Run the checks in section 5 against one final snapshot. Resolve introduced failures, preserve unchanged-control diagnoses and identify every skip. A failing required full suite remains a release gap even if a focused selection passes. Then request independent integrated review, including interactions among all previously reviewed slices.

## 5. Verification protocol

These are planned commands and acceptance obligations, not tests run while writing this plan. Use pinned toolchains and each workflow's environment. Current records use Go 1.27.1 and Node 24.21.0; the artifact workflow declares Node 22, so its environment also needs coverage.

| Scope | Required checks |
| --- | --- |
| Every defect | Failing assertion on pre-fix code, deterministic barriers/negative controls, narrow race count 3, then the whole affected package. Verify selected tests actually ran and were not skipped. |
| Switching | Backend production execution/cancellation tests, all affected session-manager/Chat/store races and tagged direct/fallback actual-process matrix. |
| Retirement | Escaped-descendant/cached-proof controls, the three retained positive groups, complete persistent-host races, real supervisor/process matrix and each native platform contract. |
| Backend | From `backend`: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race -count=1 -timeout=20m ./...`; required tagged process/CLI suites, including `go test -tags e2e -v ./internal/cli/...`. |
| Runner and SDK | From `accounts-manager/runner`: build, vet, complete tests and complete race suite. Run callback SDK/auth and affected embedded-engine tests; preserve untouched-engine comparisons for inherited failures. |
| Lint | Root `npm run lint`; full runner pinned lint and applicable frontend/locale checks. Use golangci-lint v2.13.2. Changed-scope lint is an early check, not final full-lint evidence. |
| Contracts and data | Root `npm run api` and `npm run sqlc`; HTTP route/spec parity and CLI wire compatibility; no generated drift; empty/existing/legacy database upgrades, all journal crash cutoffs and failure-atomic migration controls. |
| Frontend | From `frontend`: `npx vitest run`, `npm run typecheck`, `npm run typecheck:e2e`, `CI=true npm run test:e2e:renderer`, `npm run build` and workflow-equivalent desktop packaging. Run shared-package checks required by the current frontend workflow. |
| Security and compatibility | Secret/telemetry/error/argv scans, loopback and LAN negative controls, credential-at-rest permissions, malformed evidence, stale capabilities, 91 protected hashes and unchanged native/Subscriptions behavior. |
| Native artifacts | Native Windows lifecycle suite and both Mac architectures; signed Mac artifact verification using the repository verifier; terminal/Chat/files/tools/network and update/restart compatibility. Cross-compile/vet are diagnostics only. |
| Workflow parity | Review all applicable jobs in `go.yml`, `frontend.yml`, `cli-e2e.yml`, artifact and secret-scan workflows, plus any path-triggered contracts. Local containers are session-labelled. No publishing/deployment as a test. |

Use test-owned data, sockets and process identities, stripped credential environments and matching fixture shells. Run resource-heavy checks serially when disk/runtime resources require it. Do not kill shared services. Retain output before stopping an owned diagnostic command.

At each freeze record base/head, complete changed-file manifest, source hashes, commands/toolchains, selected test names/counts, red/green logs, preservation audit and remaining gaps. An edit invalidates affected pre-edit results. Earlier immutable archives remain intact; a new correction gets its own seal.

## 6. Critical path and external requirements

```text
W0 baseline/support -> W1 overlapping terminal proof -------------------+
W0 -> W2 contained launch/native proof -> W3 managed Chat/profiles -----+-> W5 recovery
W0 -> W4 credentials/usage, with profile migration after W3 ------------+       |
                                                                         W6 desktop
                                                                              |
                                                                     W7 full acceptance
```

- Locally actionable: support inventory, production A/B fixtures, Linux production containment, managed Chat construction, credential migration mechanics, lifecycle regressions, controls and integrated checks. These are implementation work, not merely missing external evidence.
- External dependencies: native Mac arm64 and x64 hosts/SDK/signing context, native Windows runner, authorized pairs of provider identities with suitable usage access, and a confirmed independent reviewer. Arrange these early; do not wait idle where local work is independent.
- Mac same-boot keeper-death proof is an architecture feasibility gate, not a packaging chore. If the accepted experiment cannot establish it, preserve the failing evidence and review a stronger boundary. Do not substitute process absence or narrow the product promise silently.
- Publication is separate: finish technical acceptance, capture current real-app evidence for visible changes, read the repository PR-description skill, compute the final net change header, announce the exact push/PR action and obtain explicit approval. Do not publish automatically from this plan.

No reliable completion percentage or calendar date follows from the number of existing files or old passing tests. The critical path includes unfinished runtime containment and native proof, not just UI polish.

## 7. Plan self-review and handoff

Reviewed this plan against the user requirements and current code/evidence:

- Session-scoped routing already exists, so W1 extends its proof instead of rebuilding a proxy. The overlapping-stream oracle prevents a sequential-only test from claiming concurrency.
- Initial choice, dropdown usage and most controls already exist. Remaining work is integration, capability support and positive verification, not another control implementation.
- The latest refusal-to-retire correction is preserved as a bounded safety improvement. Positive recovery remains explicitly red, and Linux implementation is not disguised as a native-runner-only gap.
- Setup-token input protection is distinguished from supported sign-in execution and migration. Usage permission is not inferred from credential validity or a token prefix.
- Exact identity, cancellation admission, queued work and multi-resource crash recovery are covered together. No single transaction or replacement slot is claimed to prove all cleanup.
- Queue ownership assertions are distinguished from ambiguous remote receipt. Recovery must not silently replay a potentially accepted provider request.
- Every requested platform and interface retains a release gate. Native compilation, synthetic tests, design approval and old bounded review are not interchangeable with product evidence.

Crosswalk: W0 covers earlier G01/G02; W1/W3 cover G03/G04; W5 covers G05/G06/G08; W2 covers G07/G08/G11/G12; W4/W6 cover G09/G10/G14; W7 covers G13/G15/G16. The latest retirement S4/R1/R2 remain open under W2 and final review. Old ledgers are evidence references, not silently edited approvals.

Next implementation checkpoint: independently review the preserved safety guard and Linux containment contract, while extending the existing terminal harness to overlapping A/B requests. Do not enable new managed execution until its ownership contract is cleared.
