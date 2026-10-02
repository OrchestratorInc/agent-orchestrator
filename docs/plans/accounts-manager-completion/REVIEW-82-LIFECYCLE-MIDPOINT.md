# Lifecycle midpoint correction handoff

Status: HOLD. This requests a bounded midpoint re-review, not a freeze or completion decision. All work is local. Public controls and publication remain held.

Historical checkpoint: the later instruction authorizes a bounded F2/F3 freeze, tracked in `LIFECYCLE-WINDOWS-EVIDENCE-GATES.md`. This document and its earlier results do not certify the final freeze. F1, moved descendants and native Windows acceptance remain on HOLD.

Delivery: `ao report --needs-input` accepted this handoff and both integrity artifacts (exit 0). Direct steering to session59 returned `CHAT_CONTROLLER_NOT_READY`, request `Arch/xQO0CDwDLg-002214`, delivery handle `lifecycle-midpoint-79-f2f3-rereview`. Independent review has been requested durably, not acknowledged or completed.

Source: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`, branch `ao/agent-orchestrator-79/accounts-manager`, HEAD `048a59775999b60f8276a1f5d1107dbef57f5483` plus the preserved working tree.

Evidence directory: `/tmp/pr-5769-lifecycle-79.tRGYkc`.
Review being addressed: `/tmp/pr-5769-lifecycle-midpoint-review-82.txt`.

## Requested independent review

1. Re-review F2's creation-time containment and original-handle lifetime in `provider_child_windows.go`, its publication cuts, and the common host wait/pipe boundary. Confirm there is no create-before-contain interval or destructive PID fallback. Native Windows verification remains required.
2. Re-review F3's canonical duplicate-key rejection in `provider_owner_json.go` and the repaired valid-prefix size fixture. Confirm ambiguous evidence cannot reach either retirement acknowledgement entry point.
3. Keep F1 and the escaped-descendant acceptance gate on HOLD. Review the explicit platform constraint in `LIFECYCLE.md` before any further macOS implementation. A broader privileged supervisor or isolated runtime needs a separate design decision and native execution resources. Do not accept launchd group cleanup or periodic process enumeration as arbitrary descendant containment.

## F1: failed-first reproduction, unresolved design

`provider_owner_escape_linux_test.go` uses an in-package census barrier and channels. After the census, the recorded leader creates a child in another session/group and exits. `owner-escaped-descendant-red.log` records both exact retirement and SQLite-backed account deletion succeeding while the escaped child lives. The assertion remains failing, not skipped or weakened. The SQLite reopen/retry branch only executes once refusal is corrected; it is not currently passing coverage.

The macOS draft cannot retire a surviving orphan after host death. Apple's documented launchd cleanup covers a process group, and its historical kqueue descendant-tracking flags are unsupported. No proven mechanism within the current same-user runtime boundary closes the group-escape schedule across supervisor death. The draft is not accepted and has no native or completion claim. D2's separately cleared bounded Linux algorithm remains unchanged.

## F2: Windows birth containment correction

Failed-first artifact: `windows-birth-red-wine-v2.log`, assertion `host death left an uncontained suspended child alive`. The fixture exits the host at the post-create/pre-assignment hook using ordinary test code. Pre-correction source is preserved in `windows-birth-red-source.tar.gz` (SHA256 `1d62b612ee7ef2756d5841e038235903c4280e8c432deaf238f37affb6cd7a70`). The original red executable remains `persistenthost-windows-red.test.exe`.

The launch now supplies `PROC_THREAD_ATTRIBUTE_JOB_LIST` during `CreateProcessW`. Standard streams are the only inheritable handles. The provider remains suspended while its ownership record is published, then its original primary-thread handle is resumed. Job assignment and thread enumeration after process creation are removed. The unused broad Windows termination fallback is removed from the managed-host package. Unsupported attributes fail without starting an uncontained fallback.

Tests cover birth, before-publication, after-publication and after-resume cuts, with simultaneously running replacement and unrelated processes. Starting records still refuse retirement. Active receipt recovery and normal host controls remain native verification gates.

Windows x64 compile and vet pass. The original birth assertion passes three times (`windows-birth-green-wine.log`). The final birth/collision matrix also passes three times (`windows-birth-collision-midpoint-wine.log`). This is Wine 11.3 compatibility evidence, not native Windows evidence.

The expanded matrix fails its published-record recovery controls under Wine. `windows-proof-diagnostic-wine.log` shows the owned job's extended-limit query returns success with flags zero. Wine's versioned implementation explicitly zero-fills this result: <https://github.com/wine-mirror/wine/blob/wine-11.3/dlls/ntdll/unix/sync.c#L1544-L1552>. The production check remains strict; tests have not been skipped or changed to accept those flags. Preserve `windows-crash-cuts-controls-wine.log` as failed evidence.

Still open: native crash/restart/reboot proof; recovery holding an extra job handle; denied queries; descendants and breakaway; PID reuse; different login sessions; power-loss record durability; recovery of starting publication cuts. The Windows directory-sync omission is explicitly not certified as power-loss safety.

## F3: duplicate evidence and size fixture

`owner-duplicate-red.log` contains five failed-first cases with a matching live workload: repeated or case-aliased state, PID and start fields. The fixed reader checks unique canonical keys in the root and each member object before decoding authoritative evidence. Ambiguity returns inconclusive at read, exact shutdown and unbound retirement, while the real test-owned provider and child remain alive.

The oversized fixture now contains a complete valid stopped proof. Exactly one MiB succeeds; one extra whitespace byte, extra data and a second JSON object are rejected. Removing the size check would admit the valid whitespace case. Structural stopped-proof tests remain passing.

## Observed checks

| Check | Result | Log |
| --- | --- | --- |
| Linux duplicate, size and incomplete-proof cases, race, three repetitions | PASS, 2.207 s | `owner-duplicate-evidence-race-v2.log` |
| Linux evidence plus existing exact-host, original-host-crash, replacement and SQLite-recovery controls, race, three repetitions | PASS, 59.641 s | `linux-evidence-host-midpoint-race.log` |
| Automatic/manual refresh shutdown, single-flight and generation fences, race, three repetitions | PASS, 8.305 s | `runner-refresh-midpoint-race.log` |
| Linux persistent-host package build and vet | PASS | `linux-host-midpoint-build-vet.log` |
| Windows x64 persistent-host test compilation and vet | PASS | `windows-midpoint-compile.log` |
| Windows birth and collision controls, three repetitions under Wine | PASS | `windows-birth-collision-midpoint-wine.log` |
| Expanded Windows recovery and normal-host controls under Wine | FAIL, strict job-limit query refusal | `windows-crash-cuts-controls-wine.log`, `windows-proof-diagnostic-wine.log` |
| Expanded escaped-descendant retirement/deletion contract | FAIL, implementation unresolved | `owner-escaped-descendant-red.log` |

Focused Linux command: `go test -mod=readonly -p=1 -race -count=3 -timeout=150s ./internal/adapters/chatdriver/persistenthost -run '^Test(ProviderOwnerEvidence|RemovalHost)' -v`.

Focused runner command: `GOWORK=off go test -mod=readonly -p=1 -race -count=3 -timeout=120s ./internal/runner -run '^TestCredential(AutoRefresh|Refresh)' -v`.

Commands used the existing isolated environment wrapper and session-owned temporary paths. No real credentials were used. Full package/module suites are not represented as passing while the escaped-descendant regression remains red.

## Scope and preserved evidence

`midpoint-source-scope.json`: eight changed and eleven new Go source/test files relative to D2, confined to runner credential lifecycle and persistent-host packages. The Windows-only launch change requires the common host to wait through its platform-owned child; Unix stopping remains unchanged. The pre-existing shell-only race test is tagged non-Windows instead of claiming Windows coverage.

`midpoint-protected-audit.json`: 91 protected files, four R4 files, three D2 correction files, ten D2 archive/manifest references and 32 generated files all match their recorded hashes. No subscription/native-switching files changed. No D2 archives or manifests were overwritten. No source freeze or new platform certificate was produced.

Remaining lifecycle work also includes the complete filesystem/interrupted-write/reuse evidence matrix, expanded refresh admission and eligibility compatibility, full affected checks/lint, and independent CLEAR. Desktop, public controls, live-provider flows and release performance remain downstream gates.

Fun fact: process-creation attributes can bind job membership before a new Windows thread runs.
