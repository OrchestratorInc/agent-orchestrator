# Linux containment feasibility and review boundary

Base: `1bd8dce35d8f1104e069ca57d98ab8e0e339b7ce`. Status: external synthetic experiment completed; production lifecycle and deletion gates remain HOLD. No lifecycle source or frozen Mac design was changed.

## Observed evidence

The current group census cannot prove retirement of a child born after observation that leaves the group/session. A new execution boundary must prevent that loss of ownership from birth. Existing uncontained processes cannot be relabeled as contained.

The diagnostic uses ordinary Go tests, private synthetic helpers, uniquely named user-manager units and fresh fixture cgroups. No provider credentials, network request, real session process, broad signal, privileged configuration or host-service restart is involved. Only the test-owned init process kills itself in the crash control. All transient units and empty fixture groups were absent after cleanup.

Evidence root: `/var/tmp/pr-5769-next-79.wos4rh`.

| Experiment | Observed result |
| --- | --- |
| Direct creation-time cgroup placement from this worker | Permission denied, three final repetitions. A delegated user-manager directory does not give this session the required common-ancestor migration permission. |
| User-manager birth placement without a namespace boundary | Child is born in the intended unit, then successfully moves into a test-owned sibling. The isolation assertion fails in all three final repetitions. Bare user-manager cgroups are insufficient. |
| User-manager birth plus private user/PID/cgroup/mount/network namespaces | Sibling migration rejects in all three race repetitions. The fixture sees only its namespace cgroup root, not the host control files. |
| Late child after census, new session/group, original parent exit, namespace-init normal exit | All three race repetitions prove the late child existed outside the initial census and no recorded original execution survives. Unrelated native control exits normally afterward. |
| Same schedule with namespace-init self-kill | All three race repetitions preserve the native control and retire the late child. No broad group or reusable PID kill is used. |

Final positive command, from the external `containment-probe` directory:

```text
go test -v -race -count=3 -timeout=90s . -run '^Test(NamespacedUserManagerSiblingEscape|NamespaceInitDeathRetiresMovedDescendant)$'
```

`linux-containment-late-fork-final1.log` exits 0, with nine executed positive leaf cases and no skips. The two negative controls are run separately on the same final test source:

```text
go test -v -race -count=3 -timeout=90s . -run '^Test(DirectCreationTimeCgroupPlacement|UserManagerBirthAndSiblingEscape)$'
```

`linux-containment-controls-final1.log` exits 1 as required by the preserved failure assertions, six executed negative cases. This is diagnostic red evidence, not a passing package suite. `go vet .` exits 0. Commands use Go 1.27.1, GO111MODULE=off, GOMAXPROCS=2, the credential-stripping wrapper and Fish. Installed namespace tool version: 0.11.0. The first command omitted an explicit user-namespace flag and failed before execution; that log is retained separately and is not proof of a kernel limitation.

Exact SHA256 values:

- `containment-probe/containment_test.go`: `dbd6e84dc73d94b4924428f846920445676d333fb5513209106c71004b7d4a6c`
- `linux-containment-controls-final1.log`: `25d7ec09513d83179c645bc7ae15d866e9f9e50b51accae61ed59a0eafdacec9`
- `linux-containment-late-fork-final1.log`: `14fd387fe0c4a49ef37eac56ffbdb71d8841eec38d387774391a74737811bdef`
- `linux-containment-probe-vet.log`: `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`

## Candidate decision, not product adoption

Investigate a trusted per-launch namespace init with creation-time broker placement, closed host access and a durable start permit. Do not add another process census or adopt bare cgroup naming as death evidence. The kernel destroys a PID namespace's contained execution when init dies, and cgroup termination handles concurrent forks; membership restrictions and host-execution access still need their own proof. [PID namespace contract](https://www.man7.org/linux/man-pages/man7/pid_namespaces.7.html), [cgroup contract](https://docs.kernel.org/admin-guide/cgroup-v2.html).

The probe has no usable workspace or external network. It demonstrates a narrow lifetime property, not a working managed provider environment. Direct placement is unavailable from this worker's current cgroup; the user-manager bridge is a candidate, not a new universal Linux dependency silently added to the product. No native-host or release acceptance is inferred.

## Required implementation contract before adoption

1. Create a unique launch identity and persist a starting record before any provider code executes. A trusted namespace-init handshake supplies exact kernel identity while the provider is still blocked. Persist that identity and sync the owner record before consuming a one-use execution permit. Crash before permit consumption cannot run provider code; crash after it must remain exactly recoverable.
2. Keep every provider/tool subprocess in the launch's PID lifetime boundary. Do not expose host cgroup controls, namespace handles, process descriptors, privileged devices, user-manager/session-bus sockets, the daemon's execution API or other host execution brokers. Close inherited descriptors and environment inputs. A host network or writable host socket mount cannot be added as a convenience fallback.
3. Define usable workspace and networking separately. The current empty/read-only fixture is insufficient. Validate staged project files, Git/worktree behavior, tools, terminal/Chat streams, preview and permitted external networking without a route back to host execution. Managed route access needs a narrow authenticated transport that preserves per-request account/revision admission; no new network-facing listener or change to the primary daemon listener.
4. Persist a typed identity containing the boot and namespace-init ownership proof before readiness. Unit names, missing descriptors, empty scans and generic service status are not retirement proof. Recovery must validate immutable kernel ownership before any signal. A replacement in a reusable slot must survive. Any inability to establish exact identity remains recovery-required, never a broad kill.
5. Establish cold recovery after both host-parent and keeper death, independently of guest self-report. The positive experiment proves controlled namespace-init death only. It does not implement durable identity publication, same-boot reopen, interrupted handshakes, stale receipts, namespace/PID reuse, replaced unit generations, cancellation or deletion integration. Native ownership and tombstones remain authoritative across those cuts.
6. Close admission durably before teardown. Drain/revoke route leases and refresh workers; stop exact contained execution; acknowledge only after kernel-backed proof. No helper restart, unit restart or old start permit may resurrect a retired launch. Only then may the existing deletion saga remove the credential and finalize bindings/defaults.
7. Preserve old ownership obligations. A legacy uncontained group with uncertain descendants cannot acquire a stopped receipt from a newly contained replacement. Upgrade/recovery needs positive old-owner retirement or boot-change proof. Neither absent ownership files nor an unrelated replacement's exit is sufficient.
8. Define packaging and availability. Verify shipped helper/version, namespace/kernel/user-manager capabilities and platform support before managed launch. Unsupported prerequisites fail closed with a truthful capability state, without changing native execution or making that unavailable state count as completed platform support.

## Next acceptance gates

- [ ] L1: independent review accepts the bounded kernel experiment and the closed-access/start-permit contract.
  EVIDENCE: requested; no new verdict observed.
- [ ] L2: durable owner publication and permit crash cuts pass before/after every start boundary, including daemon and keeper loss, reused identities and unrelated replacement/native survival.
  EVIDENCE: not implemented by this experiment.
- [ ] L3: production launch and exact-retirement adapters make the preserved escaped-descendant regression and coordinator SQLite recovery pass without weakening old-owner obligations.
  EVIDENCE: blocked on L1/L2; current production regression remains red.
- [ ] L4: workspace/network/tool/terminal/Chat compatibility, packaging, real desktop/provider behavior and latency pass without reopening host execution access.
  EVIDENCE: not established; separate Mac and Windows gates remain open.

Reviewer request: inspect the exact external source and both positive/negative logs, especially the post-census fork, parent exit, self-kill, kernel identity observations and cleanup. Return a feasibility verdict only. Do not clear coordinated deletion, managed Chat or release readiness from these experiments. All earlier source/evidence archives remain immutable; no publication occurred.
