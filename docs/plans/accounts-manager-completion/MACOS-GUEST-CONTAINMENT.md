# Per-launch macOS guest containment: design and feasibility

Status: DESIGN REVIEW REQUESTED. Runtime/product gate: HOLD. No guest implementation is authorized by this document. The existing escaped-descendant regression remains unchanged and failing. The bounded F2/F3 archive remains separate, with no new certification of Windows recovery.

## Decision

Evaluate one architecture-matched Linux VM for each managed controller launch, controlled by a small signed macOS helper using Virtualization.framework. Keep the helper alive independently of the desktop and daemon. Put provider executables, their tools, terminal commands and automatic project scripts inside that VM. A process changing its group, session or parent stays inside the guest boundary.

This is a candidate with explicit feasibility gates, not a completed containment proof. Apple documents Linux guests on both Intel and Apple silicon, and an asynchronous destructive stop operation. The inspected documentation does not establish a durable, externally reopenable VM identity or the complete abnormal-helper-death cleanup contract. That gap must be resolved before this backend can acknowledge cold retirement. Missing helper files, an absent launchd label, EOF, or a disappeared PID are never sufficient evidence. [Linux guest setup](https://developer.apple.com/documentation/virtualization/creating-and-running-a-linux-virtual-machine), [VM stop completion](https://developer.apple.com/documentation/virtualization/vzvirtualmachine/stop(completionhandler:)).

Do not substitute a native process-group wrapper, a polling descendant watcher, a shared Docker Desktop VM, or a remote cloud account. Do not make existing native sessions use a guest implicitly. The user chooses the account for every session; no account fallback is introduced.

## Existing code: reuse and limits

Source locations below refer to the current session79 worktree. The implementation is ahead of some repository overview documents; inspect the listed code, not only those overviews.

| Existing boundary | Reuse | Required difference |
| --- | --- | --- |
| `cloud/internal/sandbox/provider.go:88` | Explicit create/get/start/stop/delete vocabulary and typed absence | It has no generation-bound joined-retirement receipt. `FindBySession` cannot identify an old launch. It is a separate module's `internal` package, not importable by the local backend. |
| `cloud/internal/sandboxresolve/resolver.go:35` | Explicit provider selection | Its implemented providers are remote service, Docker and Coder. No local Mac hypervisor adapter exists here. Do not add a cloud dependency to local account management. |
| `cloud/internal/sandbox/createos/client.go:227` | Snapshot/restore concept | `Stop` delegates to memory/disk pause; `Delete` acceptance is asynchronous. Neither is exact terminal retirement. |
| `cloud/internal/sandbox/coder/client.go:395` | Requested versus observed lifecycle | Stop/delete create builds. A successful HTTP response is not proof that compute is gone. |
| `cloud/internal/sandbox/docker/client.go:360` | Owned-resource lookup and separate storage | Container lifecycle is not one local Mac guest per launch. Deletion also removes workspace storage, which account removal must preserve. |
| `cloud/internal/reconcile/reconciler.go:769` | Do not confuse delete acceptance with confirmation | Do not reuse the deadline branch at line735: it completes deletion while logging an unreclaimed environment. A credential-removal fence cannot expire into success. |
| `cloud/cmd/ao-worker/checkpoint.go:23` | Transcript and dirty-work preservation format ideas | It is best-effort, skips scratch preservation and pushes a ref at line159. Local account deletion must neither lose scratch files nor publish work. It is not a guest-lifetime journal. |
| `backend/pkg/agentruntime/process_unix.go:11` | Command policy can execute within a Linux guest | Process-group termination is not containment. Do not use it as host-side retirement proof. |
| `cloud/internal/workertransport/workspace.go:30`, `stream.go:69` | Root-confined file operations, bounded terminal frames and epoch rejection | These are internal cloud implementations. Reuse designs/tests or narrowly extract a reviewed neutral piece; do not copy cloud authentication and persistence into the daemon. |
| `backend/internal/session_manager/accounts_manager_removal.go:130` and `backend/internal/service/accountsmanager/removals.go:25` | Durable deletion admission, queue fences, revoked bindings, stop acknowledgements, final cleanup | Each managed launch needs an exact guest-retirement receipt before the existing stop acknowledgement. Preserve the current cancellation and no-fallback rules. |
| `backend/internal/ports/accounts_manager_removal.go:32` | Non-secret host identity bound to controller generation | Persist a typed guest owner and enumerate every old/inactive/dormant launch, not only the current descriptor. Add a new migration if implementation proceeds. |
| `backend/internal/adapters/runtime/runtimeselect/hybrid.go:50` | Preserve native behavior | Its direct-to-terminal fallback must never service a guest-required launch. Select a distinct managed guest adapter before this router; do not change native fallback semantics. |
| `frontend/forge.config.ts:63`, `frontend/scripts/build-update-helper.mjs:9` | Resource packaging and architecture-specific Swift helper build pattern | No VM helper, guest kernel, root filesystem or virtualization entitlement is currently packaged. The updater helper itself must not become a VM service. |

## Trust and execution boundary

The guest contains provider code, extensions, protocol adapters that execute provider tools, build commands, project hooks, package installation, Git operations influenced by repository configuration, session side shells, and automated browser tooling. Chat and terminal modes use the same guest identity contract. No host `exec` endpoint, host shell fallback, SSH agent socket, Docker socket, host credential directory, host package manager or host MCP command bridge is exposed to guest code.

The host runs trusted orchestration only: SQLite, encrypted credential custody, route admission/revocation, framed transport, the VM helper and desktop presentation. A trusted hypervisor/framework defect or a separately malicious host administrator is outside this process-lifecycle guarantee. Ordinary provider code with guest root access must still be contained; a guest PID, cgroup, init response or guest assertion cannot attest its own destruction.

Use a VM-specific virtio socket transport. VSOCK supplies connectivity, not authentication or encryption. Use a pinned, mutually authenticated encrypted channel with a fresh launch nonce; validate the launch identity and controller generation on every stream. Provider credentials never enter that channel, the VM image, a command line, an environment dump, or logs. The guest receives only a launch-scoped revocable request capability, delivered over the encrypted channel and held in volatile guest storage. The host runner still checks durable account admission for every logical request. [Apple socket device contract](https://developer.apple.com/documentation/virtualization/sockets).

No general guest network device in the initial feasibility configuration. Outbound traffic uses a bounded gateway with explicit destinations and no access to host loopback, the daemon API, management ports, metadata endpoints or other guests. DNS rebinding, redirects, IPv6 and proxy tunnelling need negative tests. The fixed account route is separate from general development egress. Arbitrary project network compatibility remains a release gate; a gateway refusal never triggers native execution. Guest-triggered remote execution is not part of local descendant teardown and must not be silently offered as an escape hatch.

## Durable ownership and admission

Proposed non-secret `GuestOwnerV1` fields: installation ID, session ID, immutable random launch ID, controller generation, account binding revision, schema version, host boot UUID, guest architecture, helper/protocol version and digest, kernel/rootfs digests, configuration digest, unique storage IDs, state revision and retirement-operation ID. The record contains no bearer, provider token or decryption key.

The host launch ID identifies one execution lifetime, not a reusable session slot, PID, path, VM display name, MAC address or guest-provided machine ID. A replacement always receives another launch ID and private socket/storage paths. A guest cannot write the ownership ledger, helper executable, tombstones or registrations. Guest disk snapshots exclude all host ownership state.

1. Reserve the account binding/controller generation and create the host record under the existing private data root. Commit and sync it before registering a helper or constructing/starting any VM. Strict schema, duplicate-key, bounded-size and interrupted-publication checks apply.
2. Register a unique foreground keeper for that launch under an explicit user bootstrap domain. Persist and verify its exact registration/configuration identity. Launchd provides availability and lookup, not descendant containment. No provider command appears in the registration. No `KeepAlive` or login rule may reboot a consumed launch.
3. The keeper serializes admission and retirement on one queue. It durably consumes a one-use start permit before constructing the VM. Account deletion can race with this step, but the reserved owner is already in deletion's enumeration. The runner denies tombstoned bindings before releasing a provider request. An ambiguous consumed permit is retired, never reissued.
4. Start a clean guest into a non-provider bootstrap service. Check image/protocol/launch identity, then revalidate the durable account/controller admission before releasing provider execution. Publish readiness only for that exact generation. A stale readiness response cannot bind or relaunch a session.
5. Keep the VM helper independent of desktop quit and daemon replacement. Daemon reconnect adopts the existing authenticated helper and guest; it must not create another VM under the old identity. The guest supervisor cannot restart a provider after its launch is retired.

All state lives beneath `AO_DATA_DIR` (normally the existing user data root). No default OS application-data directory. Paths are confined, private and validated; labels do not authorize a destructive operation. Proposed implementation must serialize start, stop, deletion and replacement across processes, not just under an in-memory mutex. Define and test the selected macOS file-sync, atomic-rename and directory-sync behavior before claiming power-loss durability; a successful buffered write is not that proof.

## Retirement and crash recovery

```text
reserved -> registered -> start-consumed -> bootstrap -> active
     \             any admitted state                 /
      +----------> retiring -> guest-stopped -> retired
                         \-> recovery-required
```

Retirement is monotonic after `retiring` commits. Cancellation is allowed only before the existing durable deletion stop boundary. A cancellation that wins releases only its own queue/input fences, and stale retries cannot enter retirement. Once retirement starts, repeat requests resume the same operation; they never start the VM or select another account.

The keeper first persists a launch tombstone. Close new request admission and revoke all routes for that account, preserve queues and user files, then request graceful guest quiescence for a bounded interval. Quiescence is only a data-preservation opportunity. It cannot prove that every descendant stopped. Force-stop the exact VM if necessary; do not wait forever for cooperative guest shutdown.

For a live keeper, success requires completion of the exact VM's destructive stop with no error and a stopped state on its serialized queue, plus joined transport/gateway workers. Paused, disconnected, guest-init exit and a returned stop request are not sufficient. Persist a complete stopped receipt before releasing the VM object. Retire the exact registration only after the receipt is durable. Registration cleanup can retry without targeting another launch. [Destructive stop contract](https://developer.apple.com/documentation/virtualization/vzvirtualmachine/stop(completionhandler:)).

The receipt binds launch ID, controller generation, boot UUID, configuration digest, retirement ID and monotonic state revision to the observed termination result. SQLite stores this receipt reference transactionally with the corresponding stop acknowledgement. Missing receipt after a successful stop is a recovery cut, not permission to invent an acknowledgement. Preserve retired identities/tombstones indefinitely in this first design; garbage collection needs a separate proof covering every retained snapshot and retry.

| Recovery observation | Required action |
| --- | --- |
| Daemon/desktop died, exact keeper lives | Authenticate its full owner tuple; retry retirement or reconnect without provider restart. |
| Original Chat frontend/controller died; replacement appears and disappears | Retire the original guest by immutable launch ID. Neither replacement publication nor removal changes the original obligation. |
| Keeper died before permit consumption | A validated durable unconsumed state plus proof no concurrent starter can proceed allows retirement without VM creation. Missing/corrupt state does not. |
| Keeper died after permit consumption, including before active publication | **Unresolved native proof gate:** obtain authoritative termination of every framework execution resource associated with that launch. No PID/label/file-absence shortcut. No automatic launch under the old ID. |
| Stop completed but receipt persistence/response failed | Repeat exact observation through the still-owning keeper. If it also died, use the native proof above; otherwise remain recovery-required. |
| Owner record or stop receipt corrupt, duplicate, truncated, oversized, inaccessible or replaced | Refuse acknowledgement, preserve deletion fence, perform no destructive lookup by a guessed identity. |
| OS reboot is positively established | No previous-boot execution is live, but first restore account tombstones and deny old permits/snapshot resume. A timeout, sleep/wake or changed helper PID is not a reboot. Unknown boot evidence stays blocked. |
| Replacement is live, wrong identity answers or old endpoint is reused | Preserve it. Resolve the original obligation independently; no signal, input, terminate, launch or revision rotation through the replacement. |
| Snapshot/old database restore is requested | Never restore execution state or a consumed launch ID. Reconcile against the non-guest tombstone ledger and current vault generation. Mismatch blocks startup. |

Native feasibility must establish the abnormal-helper-death row on the same boot, without requiring the user to reboot or terminate an orphan manually. A keeper which merely refuses forever does not satisfy the product contract. The reviewed Apple APIs expose a live VM object's lifecycle; this design has not found a documented cold reattachment/lookup API by application launch UUID. An externally surviving helper is useful for daemon death but alone only moves the crash cut. If the framework cannot prove its abnormal cleanup or provide an exact recoverable execution handle, reject this backend and retain HOLD. A lower-level per-process hypervisor backend is a separate candidate, not an automatic fallback. [Hypervisor resource mapping](https://developer.apple.com/documentation/hypervisor).

## Workspace, history and snapshot policy

Use a guest-private writable workspace/data disk and a verified read-only base image. Account deletion destroys execution, not the user's work or queued messages. Keep work/history storage detachable and never execute a retained disk under its old launch ID. New user-selected-account launch uses a fresh execution identity and fresh capability.

Initial import is explicit, local and non-publishing: copy a bounded snapshot of the selected worktree and needed repository objects into the guest without running hooks, filters, credential helpers or project code on the host. The guest gets an independent Git metadata directory, not a writable mount of the shared host `.git` directory. Preserve dirty, ignored and scratch content according to the existing workspace contract. No automatic `push` from the cloud checkpointer is reused.

Prefer typed file/diff transport for desktop inspection and an explicit conflict-checked export operation for host edits. No automatic bidirectional sync into directories the host may execute. Export must confine paths, reject traversal/symlink/hardlink escape and executable metadata injection, detect concurrent edits and avoid `.git`, user data and credential stores. This changes how local editors see live guest changes, so editor/file-watcher compatibility is an open product gate, not an implementation detail to hide.

A read-only virtiofs staging directory is an optional import optimization after confinement tests. Never share the host home, daemon state, credentials, sockets or broad writable source tree. Read-only sharing does not prevent data disclosure; only an explicitly chosen staging set is exposed. [Shared-directory contract](https://developer.apple.com/documentation/virtualization/vzshareddirectory).

No RAM snapshots, suspended-provider warm pools or execution-state restoration in this candidate. Immutable caches contain no account capability or host ownership record. Guest capability files use volatile storage with swap/hibernation disabled, but guest code can copy a capability into its own files. Therefore retirement must make every copied capability unusable at the host, including after disk restoration; do not claim secure erasure of user-retained disks. Snapshot rollback must never roll back deletion intent. Full host data restoration requires an explicit recovery reconciliation against current credential generations before any launch; restoring mutually stale vault and ownership backups cannot be described as rollback-proof.

Existing native managed launches cannot be relabelled as guest-contained. Inventory them by their original owner kind, keep their current obligations and do not manufacture a guest receipt for them. Moving a session into this backend requires retiring its prior execution and an explicit compatible workspace/history transfer. An unprovable legacy orphan remains a release/recovery blocker; this design does not make the inherited escaped-descendant regression pass by changing its scope.

## Packaging and compatibility

| Surface | Required design / unresolved acceptance |
| --- | --- |
| arm64 and x64 | Ship matching Linux kernels, rootfs, guest worker and provider binaries for each architecture. No Intel guest OS on arm64 as a compatibility assumption. Prove both on physical supported Mac hosts. Existing CI declares `darwin-arm64` and `darwin-x64` in `.github/workflows/build-artifacts.yml:42`. |
| Native helper | New dedicated Swift executable, built on macOS, outside the archive containing renderer code. Reuse only the existing helper build/resource-validation pattern. Version and retain helpers used by live guests across updates. No provider-controlled dynamic plugin loading in the helper. |
| Signing | Sign the helper with `com.apple.security.virtualization`; validate support/configuration at runtime. Give only the helper the entitlement. Preserve existing nested-runtime signing rules, notarization, zip/dmg updater outputs and canonical release-conductor flow. Validate the installed signed artifact with the repository's Mac artifact verifier. No publication in this slice. |
| OS baseline | Establish availability of stop, sockets and any sharing API against the repository's supported desktop baseline before selecting a minimum. The updater helper's macOS11 build target is not proof that this VM feature works there. Do not silently drop older desktop support. Unsupported capability blocks managed guest launch, never falls back to host execution. |
| Images and supply chain | Reproducible per-architecture kernel/rootfs builds; signed digest manifest, licenses/SBOM, corruption and version checks, atomic download/install if not bundled, offline/unavailable behavior and disk-space admission. Existing container layers are useful inventory, not bootable VM images. No private cloud checkout, hosted subscription or production credential is required. |
| Provider installation | Validate the chosen provider and adapter inside the guest. Host installation/readiness is not guest readiness. Resolve Linux distribution rights and version pinning; never copy a Mach-O host executable and declare success. Synthetic providers do not establish real sign-in, streaming, resume or tool compatibility. |
| Chat and terminal | Preserve conversation identity, queued work, approvals, input, output replay and resize across desktop/daemon reconnect. Chat/terminal handoff stays inside the same guest lifetime where appropriate; account switching retires old execution before fresh account admission. Test through production dependency construction, not only concrete adapters. |
| Session tools | Provider-facing shell, reviewer tools, browser automation, preview servers, installers and repository scripts execute inside the guest. Host presentation is only a framed consumer. Existing host shell/browser bridges must not be reachable by a guest-required session. User-operated unrelated native sessions remain unchanged. |
| Host-specific workflows | macOS SDK builds, Keychain/host tools, host MCP commands, broad writable folder sharing and external-editor live sync do not automatically work in Linux. These are compatibility blockers until a reviewed solution exists. A macOS guest is not a cross-architecture substitute: Apple's documented macOS guest path is Apple-silicon-specific. |
| Desktop integration | Keep existing native account switching and Subscriptions byte-identical. Add a managed-only runtime/workspace selection boundary only after review. Existing local workspace/router assumptions require audited integration; no global replacement of the native router. Actual desktop evidence must cover coexistence, lifecycle errors, file views, reconnect and update behavior. |

Platform sources: [Linux architecture requirements](https://developer.apple.com/documentation/virtualization/creating-and-running-a-linux-virtual-machine), [virtualization entitlement](https://developer.apple.com/documentation/virtualization/adding-the-virtualization-entitlement-to-your-project), [runtime support validation](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.virtualization), [macOS guest scope](https://developer.apple.com/documentation/virtualization/running-macos-in-a-virtual-machine-on-apple-silicon).

Performance has no measured claim yet. Cache signed immutable images and installation metadata, and measure clean-image cold boot, repeat boot, account switch, deletion, daemon adoption, first provider response, disk use and memory per concurrent session. Report distributions and worst-case failures on arm64/x64 with two accounts and unrelated sessions. Do not keep a retired guest paused to improve a benchmark. No responsiveness threshold should be invented from compile or emulator checks.

## Acceptance experiments after independent design review

All failure schedules use in-package test hooks/barriers and ordinary test invocations. Native process actions belong inside the owned test harness. No debugger or external shell signal choreography. Initial fixtures use synthetic providers; live-provider and desktop acceptance are separate gates.

| ID | Deterministic schedule | Required observation |
| --- | --- | --- |
| G1 | Crash before/after identity sync, registration, permit consumption, VM construction/start and readiness | No untracked execution. Either exact retirement is recoverable or admission stays fenced. Uncertain start never creates a second VM under the same ID. |
| G2 | Provider leader forks after census barrier; child calls `setsid`/moves group; leader exits | Retirement cannot complete while the child executes. Exact VM stop removes both tracked and previously unobserved guest descendants. Preserve the existing native failed-first test. |
| G3 | Kill original Chat/controller parent; publish a replacement; stop replacement; old descendant continues; reopen SQLite and retry deletion | Delete only the original launch after proving guest termination. Replacement disappearance cannot acknowledge the old owner. Repeat with replacement still live and an unrelated native session. |
| G4 | Kill VM keeper during bootstrap, active execution and stop completion; daemon also restarts | Same-boot exact cleanup/recovery completes with no reusable PID/group kill and no surviving guest execution. This is the first native feasibility blocker to resolve. |
| G5 | Corrupt/missing/duplicate/oversized records; interrupted write/rename/sync; reused PID/domain/label/socket | No stop acknowledgement, broad cleanup, guest launch or foreign-owner disruption. Valid positive controls still retire. |
| G6 | Cancellation commits while retry holds stale state; stop-wins control; both reopened from SQLite | Cancel-wins produces zero later input/interrupt/start/stop/revision changes. Stop-wins completes the existing operation and preserves queues. |
| G7 | Concurrent switch/delete, active/inactive/dormant launches, partial stop failure, lost credential-removal response | Every positively matched launch must retire. Admission fence persists; native empty-account bindings and unrelated account sessions remain live. No account fallback or stale refresh resurrection. |
| G8 | Reboot, sleep/wake, old disk image, old SQLite backup, old helper protocol and replayed start permit | Only positive reboot evidence can retire prior-boot execution. Tombstones and credential generations veto restart. Sleep, missing files and stale restore cannot. |
| G9 | Guest attempts host exec/API access, shared-path escape, socket access, malicious Git hooks, redirects and foreign guest control | All commands stay inside its VM; no host execution or cross-guest input/termination. |
| G10 | Both signed architecture packages, two accounts, Chat/terminal/file/preview flows, desktop quit/update/reopen | Correct account isolation and recovery through real application paths; no native/Subscriptions regression. Live provider compatibility recorded separately. |

G4 must include evidence independent of guest self-report and a stopped heartbeat: inspect the selected framework's actual ownership/termination guarantee, demonstrate the exact execution resource lifetime, and exercise delayed callbacks/retained framework workers. If only helper exit or silence is observable, it fails. Framework absence must not be inferred from `Get` on an unrelated replacement or from an application registry that forgot the original.

Self-review decisions: reject cloud timeout-to-success and automatic checkpoint publication; do not infer guest readiness from the host provider installation; preserve every legacy launch obligation; assume copied route capabilities can exist in retained guest data; keep cold helper-death proof distinct from normal stop completion. Each prevents a false completion claim without changing the descendant contract.

## Feasibility verdict and staged work

The repository supplies lifecycle concepts, guest-usable command policy, transport examples, and packaging patterns. It does not supply a local per-launch Mac VM manager, a durable VM death receipt, signed guest assets, or the required cold ownership proof. Therefore this is a new bounded runtime integration, not a library toggle on the current process host.

The current workspace is Linux with no `xcrun`/`swiftc` available. No physical Mac runtime was accessed. Compile, Wine and existing Linux process results cannot close the Mac crash/death or signed desktop gates. Repository inspection proves the missing integration; it does not prove Virtualization.framework fundamentally cannot implement the contract.

1. Reviewer82 decides on this design and the G4 feasibility experiment before any implementation. F1 and the broader lifecycle milestone remain HOLD.
2. If accepted, implement only the kernel-lifetime/keeper experiment with no provider credentials and no public controls. Prove both architectures and all G1/G4 cuts before selecting the backend for product wiring. If the abnormal-death proof cannot be established, stop this candidate and return the exact failing schedule for a different boundary decision.
3. Only after that review, add durable typed owners, tombstones and the deletion adapter; test G2/G3/G5-G8 through SQLite recovery and actual runtime wiring.
4. Then close guest execution, file/network and packaging compatibility, followed by real desktop/provider and responsiveness evidence. Public API/CLI/UI remains held until the lifecycle milestone is independently CLEAR.

No production source, protected path, old freeze manifest or existing test is changed by this design slice. Native support, end-to-end compatibility and the full user request are not complete.

Fun fact: a VM snapshot can preserve a process that has already lost its original parent, which is why account retirement must also forbid restoring old execution state.
