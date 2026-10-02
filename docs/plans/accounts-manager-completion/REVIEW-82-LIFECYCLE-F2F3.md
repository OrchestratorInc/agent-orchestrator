# Bounded F2/F3 review freeze

Status: ready for independent review of the twelve-file correction. Full lifecycle acceptance remains HOLD. Work is local only; no public controls, commits, pushes or PR changes.

The latest instruction authorizes this bounded freeze before further implementation. It does not waive macOS orphan recovery, escaped descendants, native Windows checks or any release requirement. The acceptance checklist keeps those gates open: five bounded gates met, two unmet, zero abandoned.

Workspace: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
Branch: `ao/agent-orchestrator-79/accounts-manager`.
HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`, plus the preserved working tree.
Evidence root: `/tmp/pr-5769-life-f2f3-79.CYTx36`.
Addressed review: `/tmp/pr-5769-lifecycle-midpoint-review-82.txt`.

## Explicit review request

Reviewer82: review this exact frozen correction for F2 and F3, including the additional PID-range guard found during lint. Verify the final manifests before and after review. Recheck creation-time containment, original process/thread handle lifetime, pipe cleanup, failure cuts and absence of a broad termination fallback. Recheck canonical duplicate rejection and complete-proof size/range boundaries at both retirement entry points. Report a bounded CLEAR or exact findings; do not infer native platform or full lifecycle acceptance from Wine or compilation.

Retain F1, moved descendants and native recovery as HOLD. No further source edits will occur while this bounded snapshot is under review. The full integrated archive includes held code and the failing escaped-descendant regression for context, not as accepted functionality.

## Exact snapshot

Every manifest below is in the evidence root. The source manifest is unchanged from `candidate2-source.sha256`, the snapshot used by all final checks. `final-audit.json` contains the complete path lists. The final source inventory was independently re-enumerated, with no missing or unexpected files.

| Manifest | Files | SHA256 |
| --- | ---: | --- |
| `final-source.sha256` | 1821 | `5c2eaa27f21f9cde64323e626ef32a0a058e9199441853b9878d6bf9967be035` |
| `final-correction.sha256` | 12 | `45146f8077efe78c90f2a721428e92d916922cf8961007231f99312b485be812` |
| `final-lifecycle.sha256` | 19 | `bb80947b72e5884e99c1866e96c686fe8af8e02b314e06c49da17510379ec1f9` |
| `final-deletion-union.sha256` | 69 | `c23c10df21c9253126afe36a91c6f391b1454bc3415da58602fa212a8babb298` |
| `final-protected.sha256` | 91 | `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358` |
| `final-generated.sha256` | 32 | `f059364a26a4b299aeb69689cc9190e7664aa7ce240aebcd9af3cf4e1915f37f` |
| `final-r4.sha256` | 4 | `57e158ce52f9d536dd5362e21292fb9163d850a62392567b5f0e8c9cfa5ff9c5` |
| `final-d2-delta.sha256` | 3 | `c593f71b99f3e257f5ac00687bb26b5ec615499427d229b95d0761936d961e1c` |

Source archive: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-frozen-source.tar.gz`, SHA256 `4f18fc9eb588cde679e460100819746adf62d723473d9891af7bab70c9b8e67f`. All 1,821 archived file contents were checked against the source manifest. This preserves backend/runner Go and SQL scope and the existing auth extension scope, not an entire desktop distribution.

Review package: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-review-package.tar.gz`. Its exact hash, per-member verification, evidence inventory and this handoff's hash are in `/tmp/pr-5769-life-f2f3-79.CYTx36/final-seal.json`. `final-evidence.sha256` hashes both final and diagnostic logs, preserved failed-first artifacts and the exact final Windows executable. Archives and final manifests are read-only.

The 69-file manifest is the current integrated deletion/lifecycle union, not a 69-file correction. The seven lifecycle-context files outside the bounded correction are the five earlier runner-refresh files plus the held macOS draft and escaped-descendant regression. Runner refresh is not newly certified by this freeze.

All 91 protected files match both the historical inventory and Git baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`. Four R4 source files, three D2 correction files, 32 generated files and ten D2 archive/manifest references remain unchanged. The D2 seal itself still hashes to `eb53e642a36c87477b15e449f0240f9469bc27a12d6c7ffd40fdeaf4688abb72`. Existing native switching and Subscriptions paths are unchanged.

## Correction files

All twelve files are under `backend/internal/adapters/chatdriver/persistenthost/`:

- `provider_child_windows.go`: creation-time job and explicit stream-handle attributes, original process/thread handles, publication before resume, owned cleanup.
- `provider_owner_windows.go`: exact job retirement, global name and flag checks, range-checked ownership probe.
- `child_windows.go`: removal of unused broad managed-host termination fallback.
- `host.go`, `provider_child_unix.go`: platform-owned wait/stop boundary and pipe cleanup; Unix stop behavior retained.
- `host_race_test.go`: existing Unix-shell race fixture explicitly excluded from Windows compilation; no Windows coverage claim for that fixture.
- `provider_owner_other.go`: unsupported platform boundary excludes the new explicit Windows implementation.
- `provider_owner.go`, `provider_owner_json.go`: bounded regular-file read, canonical unique fields, complete state proof and PID range validation.
- `provider_owner_duplicates_linux_test.go`, `provider_owner_evidence_test.go`: live ambiguous-proof controls, complete exact-limit proof and malformed/range tests at both retirement entry points.
- `provider_owner_windows_test.go`: birth/publication cuts, replacement/native preservation, collision, strict job proof and same-owner probe controls.

## Failed-first evidence and correction review

F2: `/tmp/pr-5769-lifecycle-79.tRGYkc/windows-birth-red-wine-v2.log` records the deterministic suspended orphan after host death at the former create-before-assign boundary. Its source archive remains `windows-birth-red-source.tar.gz`, SHA256 `1d62b612ee7ef2756d5841e038235903c4280e8c432deaf238f37affb6cd7a70`; the original red executable is preserved. An in-package hook exits the test host at the cut. No debugger or external signal orchestration is used.

`PROC_THREAD_ATTRIBUTE_JOB_LIST` now joins containment to `CreateProcessW`. The job is global, per-launch, non-inheritable, kill-on-close and non-breakaway. The only inherited handles are copied standard streams. The original process/thread handles survive active-record publication and exact-thread resume. Unsupported creation attributes return an error without an uncontained fallback. Preexisting job collisions do not alter the original owner. Birth and collision controls pass three times on the final executable under Wine.

F3: `/tmp/pr-5769-lifecycle-79.tRGYkc/owner-duplicate-red.log` contains five failed-first cases with a matching live workload. Final coverage has eight cases, including escaped key spelling and repeated group/member-array fields. The reader rejects duplicate canonical fields and case aliases before constructing authoritative state. Both retirement entry points refuse ambiguity, and the test-owned provider and child remain alive.

The exact-size fixture now uses a complete stopped proof. Reader, exact shutdown and unbound retirement all accept exactly one MiB. All three refuse one extra whitespace byte, extra data or a second object. The original size red log predates structural validation and is historical evidence, not a substitute for this repaired fixture.

Lint exposed an unchecked DWORD conversion. `pid-range-red.log` records both out-of-range complete proofs being accepted by the reader and both retirement entry points before correction. The source archive is `pid-range-red-source.tar.gz`, SHA256 `6efec793dc88c1860677b9482932b50fc3257516604fad7827e6353adf76d741`. Proof validation and the native probe now reject out-of-range IDs before conversion. The portable table passes three times under race and Wine.

The earlier negative-only `pid-range-red-wine.log` unexpectedly passed before correction. It is diagnostic, not red evidence: an unrelated permission refusal masked the unchecked conversion. The new same-owner positive control exposes that refusal and remains failing under Wine. Its assertion has not been removed or relaxed.

Self-review after the correction checked the exact birth boundary, publication ordering, handle lifetimes, canonical field handling, complete-proof controls and numeric conversion. Required unsafe calls have individual ABI/lifetime explanations; no broad lint suppression was added. The unrelated platform, filesystem and descendant gaps below remain open.

## Final-snapshot checks

| Check | Observed result | Log in evidence root |
| --- | --- | --- |
| Linux ownership/evidence, exact host, crash/replacement, SQLite recovery, ACP and reconnect, race x3 | PASS, 70.512 s | `final-linux-host-race.log` |
| Entire Linux backend build and vet | PASS, both exit 0 | `final-linux-build.log`, `final-linux-vet.log` |
| Entire Windows x64 backend cross-build | PASS, exit 0 | `final-windows-build.log` |
| Windows x64 persistent-host test compile and package vet | PASS, both exit 0 | `final-windows-compile.log`, `final-windows-vet.log` |
| Pinned changed-scope lint, Linux and Windows targets | 0 issues each | `final-linux-lint.log`, `final-windows-lint.log` |
| Wine birth, collision and portable evidence controls x3 | PASS, exit 0 | `final-windows-birth-evidence-wine.log` |
| Wine publication, job-proof and same-owner controls x3 | FAIL, exit 1; retained HOLD | `final-windows-recovery-hold-wine.log` |
| Complete source inventory, formatting, diff checks and protected baseline | PASS | `final-audit.json`, `final-seal.json` |

The exact commands, isolation paths, observed exit codes and superseded results are recorded in `final-verification.json`. All checks are serial. No real credentials were used. Earlier checks started before the final range guard and lint correction are diagnostic only. No full module or complete persistent-host suite is reported green on this snapshot.

Focused Linux command, from `backend/`:

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=240s ./internal/adapters/chatdriver/persistenthost -run '^Test(ProviderOwnerEvidence|RemovalHost|ACPHost|HostReconnectsSameProviderAndReplaysDetachedOutput)' -v
```

The linter was built from the cached repository-pinned v2.13.2 source with Go 1.27.1. The earlier Go 1.26-built tool refused the module version and is not counted as a passing check. Final lint patches are byte-identical to the candidate2 patch.

## Remaining release gates, unchanged

1. Native Windows: all publication/restart cuts, recovery holding an extra job handle, job absence versus denied query, grandchildren and breakaway, identity reuse, cross-login lookup, reboot and power-loss durability. Starting receipts currently refuse retirement and still need a recoverable completion design. Windows directory sync is not certified.
2. Wine compatibility does not establish native recovery. Its extended job-limit query returns success with zero fields, and its membership query requires a stronger right than the documented Windows API. Production checks remain strict. [Wine limit-query implementation](https://github.com/wine-mirror/wine/blob/wine-11.3/dlls/ntdll/unix/sync.c#L1544-L1552), [Wine membership implementation](https://github.com/wine-mirror/wine/blob/wine-11.3/server/process.c#L1778-L1795), [Microsoft membership contract](https://learn.microsoft.com/en-us/windows/win32/api/jobapi/nf-jobapi-isprocessinjob).
3. F1: recoverable macOS supervision after host death, plus a kernel-backed escaped-descendant contract. A launchd label or group census alone is insufficient. No native macOS capability or completion claim is made.
4. Escaped descendants: `owner-escaped-descendant-red.log` still records successful retirement and SQLite deletion while an unrecorded escaped child lives. Its regression remains unchanged and failing. D2's bounded same-group CLEAR is preserved but does not close this expanded contract.
5. Remaining corrupt/inaccessible/interrupted-write/reuse cases, refresh admission and policy compatibility, full affected suites, and independent lifecycle CLEAR.
6. Public API/CLI/UI, actual desktop coexistence, live-provider flows and responsiveness evidence remain downstream. This backend-only correction has no new visual behavior; it does not satisfy those later desktop gates.

Next action: reviewer82 re-review of this immutable bounded slice, then route concrete findings before further implementation. Nothing was published.

Fun fact: canonical JSON keys prevent escaped spelling from hiding duplicate fields.
