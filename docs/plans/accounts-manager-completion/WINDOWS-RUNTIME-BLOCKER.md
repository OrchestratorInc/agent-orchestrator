# Windows runtime verification blocker

Status: HOLD. The complete requested matrix remains failing. No replacement freeze, review request or publication is permitted. The earlier bounded review request was withdrawn from reviewer82 with CLI exit 0; its immutable archives and exact failed logs remain preserved.

## Independent reproduction

`backend/internal/adapters/chatdriver/persistenthost/provider_owner_windows_contract_test.go` calls Windows APIs directly. It does not launch an AO host, read ownership evidence, run a coordinator or modify any production branch.

1. Create an unnamed job and successfully set `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` (`0x2000`). Both basic and extended queries return success with flags `0` under Wine 11.3.
2. Query the same classes using an invalid handle. Both calls also return success. This demonstrates that these responses cannot provide authoritative ownership evidence on the available runtime.
3. Open the current process with each documented query right. Membership observation fails for `PROCESS_QUERY_LIMITED_INFORMATION`; the `PROCESS_QUERY_INFORMATION` control succeeds.

All failures reproduce three times in `/tmp/pr-5769-windows-recovery-79.TPYVn9/windows-kernel-contract-red.log`, SHA256 `2b3e2a4e871166cd1f7b11ff942fa946ea2729c54d05db7abfbbb3a6b173232a`.

Wine's versioned source zero-fills basic and extended limit queries without consulting the supplied job handle. Its membership implementation checks the stronger process query right. Microsoft's documented membership contract accepts either right. [Wine job queries](https://github.com/wine-mirror/wine/blob/wine-11.3/dlls/ntdll/unix/sync.c), [Wine membership query](https://github.com/wine-mirror/wine/blob/wine-11.3/server/process.c), [Microsoft membership contract](https://learn.microsoft.com/en-us/windows/win32/api/jobapi/nf-jobapi-isprocessinjob).

The inspected current Wine source retains the same query behavior, so an unverified upgrade is not a demonstrated solution. No emulator patch, mocked result or production bypass has been introduced. These findings explain why this environment cannot certify the ownership checks; they do not prove that the complete implementation works on native Windows.

## Complete rerun, still red

Executable: `/tmp/pr-5769-windows-recovery-79.TPYVn9/persistenthost-windows-recovery.test.exe`.
Prefix: `/var/tmp/ao79-lifecycle-wine.qrH2XN`.
Log: `/tmp/pr-5769-windows-recovery-79.TPYVn9/windows-complete-matrix-red.log`, SHA256 `75db63eec52e8512fbc1b89baf5114fc5a2e23d22928eb758b47c5ec47821933`.

```text
wine persistenthost-windows-recovery.test.exe -test.run='^Test(RemovalHost|ACPHost|HostReconnectsSameProviderAndReplaysDetachedOutput|ProviderOwnerWindows|ProviderOwnerEvidence)' -test.count=3 -test.timeout=180s -test.v
```

Observed: exit 1. There are 35 unique leaf cases, each executed three times: 75 passes, 30 failures, no skips. Helper entry points are excluded from those counts. Exact shutdown, ACP prompt recovery, reconnect, after-publication/after-resume cuts, limit proof and same-owner probe remain failing. Birth containment and collision preservation do not override those failures.

The command uses the existing isolated environment wrapper with credential variables removed. `DISPLAY`, `WAYLAND_DISPLAY`, `TMPDIR`, `GOTMPDIR` and `TMUX` are unset for Wine; `TEMP` and `TMP` point to `C:\windows\temp`. All test children and jobs are fixture-owned and cleaned through normal test code. There was no debugger or external signal orchestration.

## Unaffected checks and integrity

- Strict duplicate, incomplete-proof, exact-size and PID-range tests: 26 unique leaves, three race repetitions, 78 passes, zero skips, 2.842 s. Log: `evidence-race.log`.
- Windows test cross-compile, entire backend Windows cross-build and package vet: exit 0. Logs: `windows-recovery-compile.log`, `windows-build.log`, `windows-vet.log`.
- Pinned Windows-target changed-scope lint on the added contract test: 0 issues. Log: `windows-diagnostic-lint.log`.
- `diagnostic-integrity.json` verifies all 1,821 previous source files unchanged and exactly one new Windows-only test. All 91 protected paths, four R4 files, three D2 correction files and 32 generated files retain their hashes. There are no production source edits in this continuation.

The prior F2/F3 seal still hashes to `3583edc67c8fa9f31b59a37efcf4bde5c1c3f601abb3904b60ed388ea846a39c`. Its source/review archives, evidence manifest and historical handoff are untouched. The current diagnostics are not a new freeze or completion certificate.

## Required next action

Provide a native Windows 10+ x64 execution target for this unchanged strict matrix. Run the direct API controls first, then the complete matrix and fix any product failures observed there. Native success is not presumed. A separate conforming test-environment implementation would require an explicit scope decision and would still not replace native release evidence.

The native-target request is persisted through `ao report --needs-input` (exit 0). Live sending to session59 failed with `INTERNAL_ERROR`, request `Arch/xQO0CDwDLg-002270`. No native target or reply has been observed. No remote CI, installation, deployment or publication was attempted.

Remaining holds are unchanged: complete Windows recovery/publication proof, macOS recoverable supervision, escaped descendants, the remaining evidence and refresh matrix, full verification, public controls, desktop behavior, live-provider flows and responsiveness. The product contract has not been narrowed.

Fun fact: successful job configuration and successful job querying are separate Windows API operations.
