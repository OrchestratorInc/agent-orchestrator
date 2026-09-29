# W2 Linux start boundary review

Read-only review requested for a bounded, unwired launch journal/permit/identity implementation. Do not clear production containment, deletion, managed Chat or release readiness from this package. All work remains local.

Base: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`.

Evidence root: `/var/tmp/pr-5769-w2-start-79.9S9kdc`.

## Exact source

Six new files, all under `backend/internal/adapters/chatdriver/persistenthost/`:

- `contained_identity_linux.go`
- `contained_launch_linux.go`
- `contained_launch_linux_test.go`
- `contained_permit_linux.go`
- `contained_permit_linux_test.go`
- `contained_process_linux_test.go`

Manifest: `final/source.sha256`, SHA256 `5ad965c308bfacc7409be0ba95192a930175980be1697a6216359292397df24f`.

Archive: `final/source.tar.gz`, SHA256 `be8fbe9b4444e5d6463596e788b1ec8c0d9dde7c73be57efd128fd4b90d1bdb2`.

The source manifest matched after all final commands. No existing production source, native switch, subscription surface, generated contract, guest design, W1 source or retirement-correction file was changed in this leaf. Final documentation, evidence and preservation hashes are in `final/FREEZE.md`.

## Review focus

1. Sync-before-permit ordering, exact pipe-to-init binding, cancellation CAS and no replay after uncertain delivery. All admission reloads the durable record under a cross-process lock. The new journal never converts legacy group receipts.
2. Complete strict proof decoding: bounded size, exact names, duplicates, mandatory fields and phase/revision combinations. Missing/corrupt files are not retirement evidence.
3. Kernel lifetime and identity: exact PID-namespace init, boot-scoped 64-bit `pidfs` identity, process descriptor revalidation before signal and joined exit observation. PID, start tick or namespace inode alone is insufficient. Unknown/unsupported capability fails closed.
4. Crash-cut fixtures: synthetic helpers only, including a moved late descendant after its parent exits, host/keeper death, replacement and native controls, lost commit responses, and a cancellation winning while a grant waits.
5. Adoption limits: identity capture does not establish an arbitrary caller's closed-access mount/network configuration. Production construction, helper packaging and closed broker/workspace access must be reviewed before wiring this boundary. No direct user-manager or broad process-group fallback is added.

## Failed-first and diagnostic evidence

- `block-pipe-red.log`: actual raw blocking-pipe EOF grants the synthetic command without permission. External source is `block_pipe_test.go` and remains unchanged.
- `pipe-owner-red-valid.log`: real init A receives permission while durable identity names B. Preserved pre-correction archive: `pipe-owner-prefixed-source.tar.gz`, SHA256 `08274b1f3171410aedf74c07cc1f83e0686f8d8da97734dc3ccc85d4acf1a6be`.
- `kernel-identity-red.log`: durable proof lacks a stable kernel process identifier. The correction requires `pidfs`, preserves it across reopen, and rejects unknown descriptors.
- `namespace-reuse-diagnosis.log`: repeatable actual namespace-inode reuse exposes the old test oracle, with old/replacement PIDs and start times recorded. Final assertions identify the original fixture members instead of treating a recycled namespace number as ownership.
- `process-first.log` and `pipe-owner-red.log` stop at an unsupported fixture flag. They are not valid production red evidence. `integrated-first.log` contains the stale oracle failure. `lint-first.log` contains two corrected findings. Earlier final logs are superseded by the `final2-*` snapshot after the last interleaving assertion was added.

## Final verification

Commands ran serially from `backend/`, under Go 1.27.1 with `GOWORK=off`, `GOMAXPROCS=2`, a session-owned temporary directory and the credential-stripping wrapper. The wrapper is `/tmp/pr-5769-rebase.reOkRW/run-isolated.mjs`, launched by Node 24.21.0. No live credential or external provider was used.

| Command after the common `go` prefix | Log | Result |
| --- | --- | --- |
| `test -tags=e2e -mod=readonly -json -race -count=3 -timeout=180s ./internal/adapters/chatdriver/persistenthost -run TestNamespace` | `final2-focused.log` | Exit 0, 5.110s. 60 real-process cases and 105 unit leaves, plus three inactive helper entries. No skips. |
| `test -mod=readonly -json -race -count=3 -timeout=120s ./internal/adapters/chatdriver/persistenthost -run TestNamespace` | `final2-default.log` | Exit 0, 1.538s, 105 unit leaves. No skips. |
| `build -mod=readonly ./...` | `final2-build.log` | Exit 0. |
| `vet -mod=readonly ./...` | `final2-vet.log` | Exit 0. |
| `vet -tags=e2e -mod=readonly ./internal/adapters/chatdriver/persistenthost` | `final2-tagged-vet.log` | Exit 0. |
| `run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --timeout=5m --build-tags=e2e --path-mode=abs ./internal/adapters/chatdriver/persistenthost` | `final2-lint.log` | Exit 0, zero findings. |
| Same lint command without `--build-tags=e2e` | `final2-default-lint.log` | Exit 0, zero findings. |
| `test -tags=e2e -mod=readonly -json -race -count=1 -timeout=180s ./internal/adapters/chatdriver/persistenthost` | `final2-full-package.log` | Exit 1, 57.612s. 66 top-level passes; exactly three existing recovery groups fail, five leaf cases. No skips. |

The unchanged failing groups are `TestRemovalHostCrashReplacementCannotHideOriginalProvider`, `TestRemovalHostExactShutdownSurvivesReplacementAndReattach`, and `TestRemovalHostCrashLateForkSQLiteRecovery`. The safe guard still refuses to certify old uncontained descendants. They are release blockers, not accepted passing checks.

Linux arm64 cross-compilation is recorded separately in `final2-linux-arm64-compile.log`; it is not execution evidence. Native Windows, Mac arm64/x64 containment, actual provider accounts, desktop integration and positive quota observations remain unverified by this leaf.

Preservation checks cover all 91 protected paths, 33 generated files, nine retirement files, four W1 source files and five guest-package entries. `git diff --check` passes. The review package retains its own source archive so subsequent independent work cannot change the reviewed bytes.
