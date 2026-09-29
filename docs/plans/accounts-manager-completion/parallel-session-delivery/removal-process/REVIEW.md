# Pending-removal process recovery review

Read-only independent review requested. This is a test-only supplement to the unchanged removal-drain correction, not whole-product acceptance. Base HEAD: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. Work remains local.

Evidence root: `/var/tmp/pr-5769-w4-crash-79.gqzmHG`.

## Exact scope

One source file: `accounts-manager/runner/internal/runner/credential_removal_process_test.go`.

- Source manifest: `final/source.sha256`, SHA256 `ad1c8ad8d1469ed092caab24ee6fad174eae8c3de12ef09e955b517ff17d81c2`.
- Source archive: `final/source.tar.gz`, SHA256 `6f139723d107870e8db055f989439bc1fa7991dfba0969076baad1724293b29c`.
- The prior 13-file correction and its 47-file integrated manifest remain byte-identical. Prior root: `/var/tmp/pr-5769-w4-drain-79.S6e2Ns/final/FREEZE.md`.
- `final/FREEZE.md` records documentation, integrated, evidence, generated and protected manifests after this handoff is written.

## What the test proves

The table covers refresh, usage and manual credential recheck. Each case starts three separate runner test processes through the production Serve construction, using the same encrypted scratch vault and real loopback HTTP. Test-only executors/transports block A's cleanup after cancellation and prohibit external requests. All identities and credentials are synthetic.

The first process reaches an entered-worker barrier, commits removal through HTTP, and reaches cancelled cleanup without completing it. A remains resolvable but disabled, deletion has no response, and B returns positive quota. The test explicitly kills and joins only the owned process, checks abnormal termination, and accepts only fenced or disconnected worker results. It rejects every deletion response from the interrupted attempt.

A fresh process denies A's refresh, quota and enablement without dispatching provider work, while B remains usable. Repeating deletion by the original reference succeeds idempotently. A third process finds A absent and B usable. Final vault inspection requires one deleted generation, no sealed credential and no pending removal operation. Diagnostics are checked for fixture secrets and race reports; encrypted storage is checked for plaintext.

This closes the local process-crash portion of the prior D3 proof. It does not establish persistent provider-host containment, coordinator database recovery, actual provider credentials or native platform execution.

## Review and negative controls

- `first-process.log`: initial implementation passed. This is additional coverage, not a newly reproduced production defect.
- Midpoint self-review found an ignored worker result and best-effort process cleanup. The final test retains status/error separately and requires explicit kill/join evidence. `midpoint-process-corrected.log` passes nine case executions under race.
- `recovery-negative/overlay.json` changes only an external copy of the vault loader to discard the pending removal operation on reopen. `recovery-negative.log` fails all three cases with `fresh process authorized the removing account`. The live production source was not edited.
- `midpoint-process.log` records an invalid Go flag order and is not verification evidence. The corrected command ran from the runner module directory.
- Review focus: actual process ownership, deterministic cancellation barriers, no lost response masquerading as completion, no fallback, B preservation, and whether the negative control measures durable recovery.

## Final verification

Commands use the existing credential-stripping wrapper, Go 1.27.1, Node 24.21.0, `GOWORK=off`, `GOMAXPROCS=2`, a session-owned temporary directory, and Fish. Exact arguments and exit results are retained in `verify.fish`, `gates-run.log` and `verification-status.txt`.

| Check | Evidence | Result |
| --- | --- | --- |
| Process table, race count 3 | `gates-run.log`, `midpoint-process-corrected.log` | Nine case executions pass; final gate command 4.614s. |
| Complete runner tests | `full-test.log` | 120 top-level tests, 297 leaves, 11.746s. |
| Complete runner race | `full-race.log` | 120 top-level tests, 297 leaves, 16.557s. |
| Runner build and vet | `build.log`, `vet.log` | Exit 0. |
| Full pinned v2.13.2 runner lint | `lint.log` | Zero issues. |
| Windows amd64, Mac amd64 and arm64 test compilation | `*-compile.log` | Exit 0; compile-only evidence. |
| 47 integrated, 91 protected, 33 generated and five guest files | `integrated.log`, `protected.log`, `generated.log`, `guest.log` | All hashes match. |
| Final diff and test hash | `diff-check.log`, `source-before.sha256` | Clean diff check; final source unchanged since verification. |

No individual test was skipped. The command package has no tests. The gate checker reports three met and one unmet: independent review is pending. No full backend/frontend or native/live-provider acceptance is claimed. Backend-only test changes have no new visual behavior, so screenshots do not apply to this leaf.
