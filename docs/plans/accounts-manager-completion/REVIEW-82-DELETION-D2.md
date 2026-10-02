# D2 Linux deletion proof: reviewer 82 handoff

Status: source frozen, bounded verification PASS, ready for independent reviewer 82. HOLD until independent review. No publication, public controls/UI, non-Linux implementation or refresh-drain implementation is included in this freeze.

## Finding and failed-first evidence

Review source: `/tmp/pr-5769-deletion-d1-preliminary-review-82.txt`, with integrity data in `/tmp/pr-5769-deletion-d1-static-integrity-82.json`.

A process-directory census can list the provider leader, miss its later child, then skip the leader after it exits. The old zero-member result incorrectly persisted `stopped`. The ordinary Go regression installed a nil-by-default in-package observation hook immediately after enumeration. Channels held inspection while the recorded leader forked an unlisted same-group child and exited. No debugger was executed. The only pre-fix production edit was the observation hook.

Failed-first log: `/tmp/pr-5769-deletion-recheck-79.UWxSzb/late-fork-unit-red.log`, exit 1, 0.027s. It reports all three failures: successful teardown while the child lives, durable `stopped`, and successful replay of that false receipt.

## Correction and self-review

The census is advisory. Both cold teardown and normal completion now require a fresh kernel group-existence probe returning ESRCH, or a changed boot identity, before persisting stopped. Signal 0 is non-destructive. If a live child was missed, group absence cannot be established; a later census without a surviving recorded birth identity retains recovery and does not signal the group. Repeated scans alone never establish the stopped transition.

Destructive calls still require the existing durable birth identity, group/session checks and a fresh pidfd identity recheck. The correction introduces no destructive group-number operation, account fallback, secret transport, engine edit or protected native change. The test hook is atomic, nil outside tests, and scoped by owner identity in each fixture.

Midpoint review checked both zero-member consumers, the durable stopped transition, unknown/unanchored ownership, cold replay, and unaffected replacement/native groups. A retained zombie can conservatively delay absence until reaped; it cannot authorize completion. This boundary proves the recorded process group, not arbitrary processes that intentionally escape it.

## Regression coverage

`late-fork-composed-race-v3.log` passed in 54.310s under race. Four leaf cases ran three times each, with no skip:

- Late fork after enumeration during cold stop.
- The same schedule during normal owner completion.
- Actual Chat host parent death, live replacement, late child, durable coordinator failure, SQLite close/reopen and repeated cold retry.
- The same composed sequence after the replacement cleanly shuts down and removes its reusable descriptor and lock.

The composed cases use the production Chat service, exact host teardown, coordinator and SQLite store. A finalization spy proves that the credential-removal boundary is never reached while the child lives. They assert no stopped acknowledgement, retained input fencing, replacement/native process survival, and completion only after the fixture's original group is confirmed absent. The final blocked account selection remains unchanged. They do not exercise an actual provider credential or runner vault in that combined fixture.

The first composed candidate misused the manager's shutdown drain as a reusable wait; it closed further admission. Cold retries now construct fresh managers, matching the tested restart scenario. Its failing log is retained. A replacement shutdown timeout in that candidate is also retained, not reclassified as a product pass. The three-repeat run and the frozen package run are separate evidence.

## Frozen scope and bounded verification

Evidence directory: `/tmp/pr-5769-deletion-recheck-79.UWxSzb`.

The D2 delta relative to the v3 manifest contains three files:

- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_linux.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_late_fork_linux_test.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_recovery_linux_test.go`

The combined correction relative to the original deletion freeze has 14 files. The combined deletion slice has 54 files. The selected integrated Go/SQL/module inventory has 1,810 files; generated-artifact inventory has 32. The worktree remains intentionally dirty and preserves prior session work.

Bounded final commands, with the reviewed isolated environment wrapper:

1. `go test -mod=readonly -p=1 -race -count=1 -timeout=240s ./internal/adapters/chatdriver/persistenthost`: PASS, 54.684s, `d2-host-full-race.log`.
2. `go test -mod=readonly -p=1 -race -count=3 -timeout=300s ./internal/session_manager -run '^TestAccountsManagerRemoval' -v`: PASS, 38.516s, `d2-coordinator-recovery-race.log`. All 25 leaf scenarios ran three times (75 executions), no skip or race report. Cancellation-related dispatcher log messages occur during fixture shutdown; the suite exits 0.
3. Backend and runner changed-scope lint, pinned v2.13.2: both exit 0, each reports `0 issues.` Logs: `d2-backend-changed-lint.log` and `d2-runner-changed-lint.log`. Patches cover the combined correction/deletion scope, not an unfiltered whole-branch lint claim.
4. All 91 protected paths compared byte-for-byte with baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`: zero differences. All 1,810 source files still match the pre-check snapshot. All 32 generated artifacts are unchanged, formatting and `git diff --check` pass. This is a hash comparison, not a new API/SQL regeneration run.

Exact commands, environment isolation, start/end timestamps and exit statuses: `d2-verification.json`. Source and protected audit: `d2-integrity.json` and `d2-verify-audit.json`. All files below are under `/tmp/pr-5769-deletion-recheck-79.UWxSzb`.

| Manifest | Files | SHA256 |
| --- | ---: | --- |
| `d2-delta.sha256` (D2 only) | 3 | `c593f71b99f3e257f5ac00687bb26b5ec615499427d229b95d0761936d961e1c` |
| `d2-correction.sha256` (since original deletion freeze) | 14 | `2d6629324159d63578a73ba87571f560109a7fb6357396e8fcada3cc9349de80` |
| `d2-deletion.sha256` (deletion union) | 54 | `cde60c3f097b24a98b8ee734091bed4d4da4255bbf554808159365ae657f61c6` |
| `d2-source.sha256` | 1810 | `8aac28208cfd65dcb5778584f11c06136fad1d70f7368c8788532f22f52d4489` |
| `d2-generated.sha256` | 32 | `f059364a26a4b299aeb69689cc9190e7664aa7ce240aebcd9af3cf4e1915f37f` |
| `d2-protected.sha256` | 91 | `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358` |

The immutable source and evidence archive paths and digests are recorded in `d2-seal.json`. That seal hashes this handoff after finalization, avoiding a self-referential digest.

## Diagnostic cancellation and remaining gates

The former v3 full-race run cannot certify D2. Its partial `accepted-full-race-final.log` is preserved: session manager 187.292s, Chat 191.536s, account service 14.844s and account supervisor 6.064s completed before cancellation. The owned command was stopped during SQLite testing after read-only PID/cwd validation. No unrelated process or shared service was stopped. The earlier full-suite script was terminated so it could not start later checks. These results are diagnostic only.

Remaining gates:

- Independent D2 CLEAR, and independent dynamic verification of the earlier runner ownership correction.
- Full final-snapshot backend/runner build, vet and race checklist, the complete 105-scenario terminal process matrix, and API/SQL regeneration. Bounded D2 checks do not substitute for that checklist.
- Non-Linux exact managed-host deletion is unimplemented and fails closed, including ordinary nonempty-identity deletion. Earlier compilation is not native runtime evidence.
- Automatic refresh is cancelled, but the SDK does not join automatic refresh workers. Blocked-refresh/shutdown drain proof and any needed correction remain open.
- Dedicated corruption, inaccessible/missing evidence, PID/group/session reuse and every owner-record crash-cut schedule remain incomplete. Unsupported pidfd kernels fail closed; compatibility is not proven.
- Real desktop, public session API/CLI/UI controls, native coexistence, supported-platform execution, live sign-in/provider workflows and measured responsiveness remain release gates. No screenshots are relevant to this backend-only correction.

## Independent review request

Please review this exact D2 freeze locally now. Confirm that the forced late-fork census never persists stopped or advances coordinator finalization while the child survives, including normal completion and SQLite reopen. Recheck original parent-crash controls, replacement/native preservation and conservative identity handling. Retain HOLD for any substantiated defect. A bounded Linux CLEAR must not be interpreted as completion of the overall product request. Source edits are stopped pending that review.
