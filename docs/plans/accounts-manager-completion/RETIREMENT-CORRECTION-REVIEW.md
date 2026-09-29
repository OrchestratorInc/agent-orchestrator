# Retirement correction self-review

Status: bounded safety correction verified and prepared for independent review. Full F1/F2 recovery remains HOLD. Nothing published.

## Review decisions

- Exact acknowledgement now revalidates cached `stopped` evidence as well as fresh observations. A cached group-only receipt cannot bypass the same-boot containment requirement.
- The Unix group census can locate known processes for identity-bound cleanup, but cannot establish complete descendant retirement. An empty original group is no longer accepted as proof. Positively different boot evidence remains accepted.
- Ordinary host exit must not become an exact-retirement receipt. The host preserves the unresolved ownership record while suppressing only the dedicated containment-required result from ordinary exit. Other proof and runtime errors still propagate. Direct and ACP native shutdown plus unrelated replacement attachment pass three race repetitions.
- The Windows PID-range fixture previously used a synthetic Windows receipt as successful Linux retirement proof. Its positive control now checks structural validity separately. Windows still requires successful job retirement; other platforms reject that foreign-platform receipt. Out-of-range rejection assertions remain intact.
- Platform audit found the symmetric fixture assumption in the generic Unix size/incomplete-record controls when compiled for Windows. The same helper now requires native proof success and foreign-proof refusal through both exact and unbound entry points. Structural validity is checked separately on every platform. This is not native execution evidence.
- The full same-boot completion assertions are retained. They expose unfinished containment and recovery, not failures to be removed from acceptance.

## Failed-first and focused evidence

Root: `/var/tmp/pr-5769-retirement-fix-79.x3Z1f1`.

- `safety-red.log`: exit 1 on the original exact-retirement and account-deletion escaped-child cases and all three new same-boot cached-receipt entry points. The three previous-boot controls pass.
- `safety-green.log`: exit 0, race count 3, original escaped-child cases including SQLite reopen/retry and cached-receipt checks.
- `safety-native-race.log`: exit 1 from the new fixture using the test context during cleanup. The test framework cancelled that context before cleanup began. The fixture now owns a separate context through authenticated shutdown and joining. No production behavior was changed for this failure.
- `safety-native-final-race.log`: exit 0, race count 3, escaped-child, cache, strict evidence and native/replacement checks. Native cleanup is explicit and bounded.
- `host-full-race.log`: diagnostic full package exit 1. Same-boot exact retirement, host-crash replacement and final SQLite recovery cannot complete with the new safe guard. The cross-platform synthetic proof control also failed here, before its correction described above. This run does not certify the final source.

The first serial validation stopped at its source-hash guard after the cross-platform fixture correction. Its completed logs remain at the root and are superseded by the final snapshot results below. No unrelated process or service was stopped.

## Final snapshot verification

All following logs are under `/var/tmp/pr-5769-retirement-fix-79.x3Z1f1/final`. Commands and exit codes are recorded in `verification.json` (SHA256 `fc7aed7018da4bd1eddc94171e3370feaffcaed9ac0cbbe8a403247ffce598c0`). Go 1.27.1, credential-stripped environment, GOMAXPROCS=2, GOWORK=off, serial execution and test-owned data were used. Source hashes were checked before each command and at completion.

| Check | Result | Log |
| --- | --- | --- |
| Escaped child, cached receipts, native/replacement and strict evidence, race count 3 | PASS, 21 top-level executions | `safety-final-race.log` |
| Complete persistent-host package, race count 1 | FAIL, 48 top-level passes and 3 failed groups | `host-final-full-race.log` |
| Complete deletion coordinator selection with e2e tag, race count 3 | PASS, 30 top-level executions, including 18 actual direct/fallback runtime cases | `coordinator-final-race.log` |
| Complete ACP adapter package, race count 1 | PASS, 98 top-level tests | `adapter-final-race.log` |
| Chat removal/queue/owner service selection, race count 3 | PASS, 9 top-level executions | `chat-removal-final-race.log` |
| Entire backend build and vet, readonly modules, p=1 | PASS | `backend-final-build.log`, `backend-final-vet.log` |
| Persistent-host test cross-compile and package vet, Mac arm64/x64 and Windows x64 | PASS, compilation only | `darwin-arm64-*`, `darwin-amd64-*`, `windows-amd64-*` logs |
| Changed-scope lint on Linux and Mac arm64, v2.13.2 | PASS, zero issues | `linux-lint.log`, `darwin-lint.log` |
| Runnable acceptance gates re-executed on final source | S1/S2 PASS; remaining gates stay visible | `gate-reverify.log` |

No tests skipped in the five race logs. The three failing persistent-host groups contain five failing cases:

- `TestRemovalHostExactShutdownSurvivesReplacementAndReattach`
- `TestRemovalHostCrashReplacementCannotHideOriginalProvider`, both replacement modes
- `TestRemovalHostCrashLateForkSQLiteRecovery`, both replacement modes

These completion paths previously passed by trusting incomplete process-group evidence. The guard intentionally makes them refuse completion. This is an availability limitation introduced by the safety correction, not an inherited passing baseline or completed recovery feature. Their assertions were not changed or skipped. The full package and product acceptance remain red until contained launch/recovery exists.

## Freeze and review request

Base: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`.

Seven Go files are sealed by `final/source-after.sha256`, identical to `source-before.sha256`, SHA256 `da345a0f8a5bbad19b352177f541390ab3abee62da3fc793ef42e733cc664913`:

- `backend/internal/adapters/chatdriver/persistenthost/exact_shutdown.go`
- `backend/internal/adapters/chatdriver/persistenthost/host.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_linux.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_darwin.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_evidence_test.go`
- `backend/internal/adapters/chatdriver/persistenthost/provider_owner_retirement_unix_test.go`

The nine-file source/document snapshot is `final/snapshot.sha256`; its archive is `final/retirement-correction.tar`. `final/evidence.sha256` seals final and diagnostic logs. `final/integrity.json` records their counts and exact digests without circular references inside the snapshot. `final/backend.patch` is the seven-file backend correction, including the new test.

`final/preservation.json` (SHA256 `1c2d502e563e7f3c108d44393d054a445619bb237c2a83545ba76153d6f8ee65`) verifies all 91 protected paths, 33 generated files and the five-entry guest-design package (three design documents and two evidence records). The old seals were not edited. No public API/generated contract changed; preservation is not a claim of regeneration.

Reviewer request: review the acknowledgement guard and cached-receipt revalidation, native exit separation, error handling and foreign-platform evidence controls against the exact source manifest. Confirm that no unrelated replacement is signalled and no incomplete proof can acknowledge deletion. Do not clear F1/F2 product recovery or platform acceptance from this bounded guard. Independent review has not yet returned.

## Remaining product requirements

F1 requires contained execution from birth and durable exact stop/recovery, not another census. The separate Linux feasibility experiment is not wired into production. Existing uncontained owners must never acquire proof from a replacement's containment.

F2 still needs the accepted per-launch Mac guest experiment and runtime implementation, including same-boot keeper death on native arm64 and x64. This Linux environment has neither the native SDK nor a native runner. Cross-compilation does not close that gap.

No native Windows execution, live-provider or desktop evidence is claimed by this backend safety correction. Protected source, generated contracts and the guest-design seal must remain unchanged.

No new visible UI behavior was implemented, so screenshot/recording capture is not applicable to this slice. Full backend/runner/frontend release sweeps and real desktop/provider acceptance were not repeated here. Three of six acceptance gates are met; S4, R1 and R2 remain unmet, with no abandonment. A native Mac worker and the kernel-containment integration review are required next. No commit, push or PR edit was made.
