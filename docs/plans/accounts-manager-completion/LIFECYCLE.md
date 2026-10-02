# Lifecycle portability and shutdown closure

Status: implementation in progress, public controls held. D2 has independent CLEAR in `/tmp/pr-5769-deletion-d2-review-82.txt`. Its archives and manifests remain immutable.

Midpoint status: HOLD from `/tmp/pr-5769-lifecycle-midpoint-review-82.txt`. The macOS candidate is not accepted. Windows creation-time containment is implemented locally, with failed-first compatibility evidence, but native acceptance remains open. The later instruction supersedes the bounded F2/F3 review freeze: the entire Windows matrix must pass before any replacement freeze or review routing. The previous archive is unchanged diagnostic evidence, and its review request has been withdrawn. Current gates: `LIFECYCLE-WINDOWS-RECOVERY-GATES.md`; execution blocker: `WINDOWS-RUNTIME-BLOCKER.md`.

## Bounded design decision

1. Own automatic refresh in the runner lifecycle. Reuse the existing single-flight, bounded refresh operation and wait for its complete persistence pipeline before closing the vault. Cancelling an upstream scheduler without joining workers is insufficient. Do not edit the embedded engine for this change.
2. Add platform-specific exact host retirement for supported desktop platforms. Prefer kernel object identity over reusable process numbers. A platform that cannot retire surviving or group-escaping descendants remains incomplete, even if it safely refuses recovery. Do not weaken this acceptance contract to a group-only guarantee. Never introduce a broad process-tree or native fallback.
3. Harden durable ownership evidence independently of reusable host descriptors. Malformed, incomplete, inaccessible, ambiguous or unanchored evidence cannot acknowledge retirement. Test interrupted publication and numeric identity reuse without weakening the cleared Linux census invariant.

## Work sequence and review points

1. Preserve failed-first automatic-refresh shutdown and bounded-reader regressions. Implement shutdown closure, then review cancellation, late persistence and restart schedules.
2. Inspect supported platform primitives and record the concrete platform contract before platform edits. Implement the safest supported mechanism and negative controls. Review platform identity, original-host death and replacement preservation separately.
3. Complete ownership-evidence recovery tables and crash-cut tests. Run bounded serial race, build, vet and supported-target compile checks. Compile-only evidence is not native execution evidence.
4. Resolve the complete Windows matrix before freezing Windows/evidence corrections. Verify source, correction, generated and protected manifests relative to D2, including all 91 protected paths and historical R4 hashes. Route a replacement snapshot to reviewer82 only when every required Windows check passes. Full lifecycle acceptance still requires the unresolved macOS design, moved-descendant contract and native platform checks.

## Scope boundaries

Only runner refresh lifecycle, persistent-host ownership/teardown, their tests and this milestone's evidence are in scope. Preserve native switching, Subscriptions, public controls and the existing credential transport boundary. No real account operations, external publication, commits or PR changes.

Linux remains the execution host. Native macOS and Windows runs, live providers, desktop controls and complete release verification remain separate gates unless actual execution evidence becomes available.

## Design review before edits

The runner already joins explicit refresh calls but starts a separate upstream automatic scheduler whose stop method only cancels. Tracking just the executor call would miss later persistence. The owned runner operation spans both execution and persistence and is the intended join boundary.

The owner reader currently limits decoding to one MiB without distinguishing physical EOF from the limit. A valid prefix can conceal additional data. The first evidence regression will require rejection of an oversized record even when the prefix is valid JSON.

Platform teardown must positively identify the recorded launch. Missing or replacement descriptors do not prove the original workload stopped. Any platform-specific success path must establish retirement independently of those reusable files.

## Platform contract, revised after midpoint findings

The shipping matrix is Linux x64, macOS arm64/x64 and Windows x64 (`.github/workflows/build-artifacts.yml`).

- Windows: use one non-inheritable global job object per launch identity, with kill-on-last-handle-close and no breakaway. Pass the job through `PROC_THREAD_ATTRIBUTE_JOB_LIST` to `CreateProcessW`, together with an explicit standard-stream handle list. A successfully created child is already contained. Keep its original process and primary-thread handles, publish the durable ownership record while it is suspended, then resume that exact thread. There is no post-creation assignment, thread enumeration, PID kill or `taskkill` fallback. Host-local stopping and cold deletion use the job handle. Zero active job members or an absent previously published job proves retirement. Denied queries, collisions, unsupported creation attributes, starting records and legacy records remain inconclusive. A replacement receives a different job name.
- macOS: persist boot UUID, group, session and process birth timestamps. Use authenticated exact-host shutdown, followed by kernel-confirmed group absence and census checks for moved recorded members. The currently available Go primitives expose numeric signal targets, not a Linux-style pidfd signal operation. Cold recovery therefore does not signal an orphan by PID or process group: a live or ambiguous orphan remains blocked until confirmed absent. This is an explicit capability limit, not a claim of full orphan teardown parity. A boot change can establish retirement.
- Linux: preserve the cleared D2 group-absence and pidfd proof. Evidence validation may tighten before entry; do not replace the cleared census algorithm.

Windows job semantics are documented at <https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects>. The job-list creation attribute requires Windows 10 or newer: <https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute>. The launch uses `ProcessInformation` handles directly, so no process/thread reopening is needed before execution. macOS non-destructive group probing follows <https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/kill.2.html>.

### Recovery proof matrix

| Platform | Durable identity and descendants | Host crash and restart | Replacement and reboot | Unsupported behavior |
| --- | --- | --- | --- | --- |
| Linux | Existing boot UUID, group, session, PID/start identities; descendants remaining in that process group | Existing pidfd-targeted member retirement with a recorded birth anchor and affirmative kernel group absence | Different births cannot authorize a signal; different boot UUID proves the prior kernel group retired | Unanchored, moved, unreadable or pidfd-unsupported cases stay blocked; no numeric destructive fallback |
| macOS | Boot UUID, group, session, leader birth time; census checks known members, kernel group absence is the completion oracle | Authenticated original host can stop its still-owned group; cold recovery can confirm absence but cannot signal a surviving orphan safely | A replacement token cannot authorize a request; an occupied or reused group stays blocked; different boot UUID proves retirement | No cold PID/group kill; missing/inaccessible/incomplete evidence or unavailable boot/process APIs remain inconclusive |
| Windows | `windows-job-v1`, globally named launch-specific job, original process PID and creation time; children inherit non-breakaway job membership | Containment is atomic with process creation; active publication precedes resume; host death closes its job handle; recovery opens only the recorded job | Global object namespace avoids a different login session masquerading as absence; each host gets a new identity; object absence after reboot proves the published launch retired | Refuse preexisting collisions, creation/resume/query errors and records without published containment evidence; no PID fallback |

All platforms retain the launch record when acknowledgement cannot be proven. Starting records and interrupted active publication do not authorize deletion. Direct descendants that escape an initial group remain part of the requested teardown guarantee. The Linux and macOS group-only mechanisms do not currently satisfy that expanded guarantee. External service launches require a separate ownership contract and are not inferred from numeric process relationships.

## Refresh sub-slice review

Failed-first production evidence: `auto-refresh-red-v2.log` records early runner return and early vault lock release on both starts. The earlier 100 ms observation was too short because the embedded service has a startup delay; it is diagnostic only. The first race rerun exposed upstream Start/Stop initialization races when the fixture cancelled before readiness. The fixture now waits for the actual health endpoint before exercising shutdown; upstream initialization remains outside this correction.

The runner owns one automatic scheduler and admits all refresh flights under the same mutex as closure. The wait group spans scheduler, executor and persistence. Manual checks share the same flight. A blocked store-write test verifies the join extends past executor return. Three-repeat focused race checks passed in 8.364 s (`auto-refresh-race-v2.log`). Remaining review: scheduler policy compatibility, full runner suite and platform teardown.

The midpoint rerun preserves this boundary: `runner-refresh-midpoint-race.log` passes automatic/manual refresh and generation-fencing tests three times under race in 8.305 s. No runner source changed during the F2/F3 correction.

## Midpoint correction routing

1. F1 and moved descendants: `owner-escaped-descendant-red.log` is a deterministic failed-first test. A census barrier releases a leader to fork a child into a different session/group, then waits for the leader to exit. Exact teardown returns success and the SQLite-backed coordinator reaches `complete` while the child lives. Both tests fail. This is outside D2's previously bounded same-group claim but blocks the expanded lifecycle acceptance contract. Do not narrow that contract to make the test pass.
2. F2 Windows: `windows-birth-red-wine-v2.log` reproduces the suspended orphan after host death. The pre-correction source and binary are preserved separately. `PROC_THREAD_ATTRIBUTE_JOB_LIST` now closes that creation cut; `windows-birth-green-wine.log` passes the same assertion three times. Keep the global namespace, non-inheritable handles, no-breakaway limits, durable-before-resume publication and collision checks. Wine evidence does not establish native acceptance.
3. F3 evidence: `owner-duplicate-red.log` records matching live launches whose duplicate root/member fields are accepted as stopped. The reader now requires canonical field spelling and unique keys at each schema object before constructing authoritative evidence. The size fixture has a complete proof plus exact-limit and one-byte-over controls. `owner-duplicate-evidence-race-v2.log` passes the duplicate, size and incomplete-proof cases three times under race in 2.207 s.

### Windows correction review and remaining evidence

The creation wrapper retains original kernel handles through publication and resume. Only copied standard-stream handles are inheritable. The former post-start job assignment and thread census are removed. Failure cleanup terminates the owned job, waits for its original child handle and closes its handles. Common host code now waits through the platform-owned child and closes both pipe endpoints on return. Unix stopping remains unchanged.

The expanded matrix includes birth, before-publication, after-publication and after-resume cuts, with simultaneous replacement and unrelated processes. The post-publication recovery and normal-host controls currently fail under Wine because its `JobObjectExtendedLimitInformation` query returns success with all fields zero. `windows-proof-diagnostic-wine.log` isolates that result on the newly created job. Wine 11.3 implements that query as a zero-filled stub: <https://github.com/wine-mirror/wine/blob/wine-11.3/dlls/ntdll/unix/sync.c#L1544-L1552>. Do not relax kill-on-close validation, skip the tests, or call the full Windows matrix green.

Native Windows requirements remain: the complete crash-cut matrix; extra recovery handle during host death; collision and denied-query controls; grandchildren and attempted breakaway; stale or reused birth identity; different login session; reboot; and durable record replacement under power loss. Windows directory sync is still skipped, so file sync plus rename is not certified as power-loss durability. Starting publication cuts intentionally refuse retirement and still need a recoverable completion design.

The bounded freeze also retains a same-owner positive control for the wrapped-PID probe. Under Wine 11.3 that control fails with access denied: its server requires `PROCESS_QUERY_INFORMATION`, whereas the documented Windows contract also accepts the production `PROCESS_QUERY_LIMITED_INFORMATION` right. Keep the test and the minimal production right unchanged; do not treat a negative probe's refusal as proof of correct identity matching. References: <https://github.com/wine-mirror/wine/blob/wine-11.3/server/process.c#L1778-L1795> and <https://learn.microsoft.com/en-us/windows/win32/api/jobapi/nf-jobapi-isprocessinjob>.

The portable stopped-proof range regression independently reproduced acceptance of out-of-range Windows process identifiers at the reader and both retirement entry points (`pid-range-red.log`). Proof validation and the native probe now reject values outside the DWORD range before conversion. The expanded duplicate table covers escaped key spellings, group and member-array duplicates. Exact-size controls now exercise both retirement entry points, not just the reader. Current evidence is under `/tmp/pr-5769-life-f2f3-79.CYTx36`; the bounded freeze handoff supersedes the historical midpoint results below for its twelve-file correction.

Final bounded midpoint checks: Windows x64 test compilation and package vet pass (`windows-midpoint-compile.log`); birth-crash plus collision preservation pass three times under Wine (`windows-birth-collision-midpoint-wine.log`); Linux evidence and existing host-crash/replacement/SQLite-recovery controls pass three times under race in 59.641 s (`linux-evidence-host-midpoint-race.log`); Linux package build/vet pass. The expanded Windows recovery suite still has the recorded compatibility failures. No full-suite or platform-completeness claim is made.

`midpoint-protected-audit.json` verifies all 91 protected files, all four historical R4 files, the three D2 correction files, ten D2 archive/manifest references and 32 generated files without differences. `midpoint-source-scope.json` identifies eight changed plus eleven new source files relative to D2, all in the two authorized packages. These are mutable midpoint observations, not a freeze.

### macOS design constraint requiring re-review

An ordinary launchd registration does not close the moved-descendant counterexample. Its documented cleanup targets the job's process group: <https://github.com/apple-oss-distributions/launchd/blob/main/man/launchd.plist.5>. The current XNU `event.h` explicitly states that `NOTE_TRACK`, `NOTE_TRACKERR` and `NOTE_CHILD` are unsupported since 10.5: <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/event.h>. Therefore a kqueue fork watcher cannot be assumed to provide durable, lossless descendant tracking. The inspected XNU `setsid` implementation has no MAC policy check that would justify claiming a sandbox profile can simply deny that operation: <https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_prot.c>.

No proven same-user macOS mechanism has yet been identified here that both survives supervisor death and owns group-escaping descendants. A privileged process monitor or stronger isolation would expand the current runtime/distribution boundary and requires an explicit design decision plus native validation. Keep F1 and the moved-descendant gate on HOLD; do not ship a polling watcher or launchd label as equivalent containment. Continue independent evidence/refresh and Windows corrections without claiming macOS completeness.
