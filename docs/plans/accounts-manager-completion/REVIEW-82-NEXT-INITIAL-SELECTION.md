# Initial selection review request

Review the immutable archive. The live tree may advance in an independent slice. This is a local checkpoint, not release approval or publication.

Base: `2ec9d7c80006a6e0afc6e7bea36dea88228571ec`.
Tree: `47243d155de8668ae63018879b547978f9cce64d`.
Root: `/var/tmp/pr-5769-next-79.wos4rh/initial-selection-freeze`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `initial-selection.sha256` | 44 source, test and generated files | `c2276b3b90a888308858a305c21061bdd1f4a01ea41bba387abfba3601059226` |
| `integrated-source.sha256` | 5,695 files | `3e25ef692d95fc262191428424c27f855f431b1f31722e42b5cfe533622b9342` |
| `plan.sha256` | 1 file | `c127268701cb5ad02f71b6714377c3f24a98aa1d000d642c1a0e9ff24e712356` |
| `protected.sha256` | 91 files | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `generated.sha256` | 33 files | `9ec0320cc5304386fe5abee3f74d09a27b15c358a583c7d357a5ea8bf2249d01` |
| `evidence.sha256` | 37 artifacts | `989f7c6189603790ed4a8a7b9717670a1d8bf6fa28d95ca5d0a9b4b44ca7e7bc` |
| `source.tar` | complete source tree | `298b22174101fcd665817ba69d7e4145c3a21b8c6d34baed8e67b24d95010f54` |
| `correction.tar` | 44 code files and plan | `66fc1fdc129223d28524fac9e0f52b28db5ca56cbd6a6ba190b29566647eaa3c` |
| `correction.patch` | full bounded delta | `2739f8f5693f14ce81bb7eb44c7f81af7c4ee83af0347ccc4b51e78bfcb98924` |

`SUMMARY.json` contains the exact file list, absolute evidence paths, hashes and optional submodule pointer. The private submodule is not included or claimed tested. This handoff and the execution ledger are outside the archived snapshot.

## Contract and scope

Optional `account` intent carries explicit native mode or managed mode with one public account ID. Both standalone spawn and task delegation preserve it. Omission retains the older saved-default/native behavior. Unknown, null, duplicate and aliased account members fail before service execution. The new capability route reflects real manager/store support so an older daemon cannot silently discard explicit intent.

The manager validates installation, account eligibility and interface compatibility before creation. Session creation or prepared-row promotion commits the binding atomically in SQLite, including deletion admission and exact automation-adoption checks. The first launch uses the durable binding and current route admission. No inventory-order selection, default rewrite or failed-managed-to-native fallback occurs. Managed app-server Chat remains explicitly unsupported until its separate execution slice.

The CLI is a thin HTTP client with explicit mode/account flags, capability checks, usage/runtime exit distinctions and preserved request IDs. The desktop requires a choice when the capability is available, rechecks before submission, preserves unavailable selected IDs and reports only daemon-confirmed creation.

The shared harness field has an opt-in managed eligibility input used only by the task composer. It does not rewrite native readiness. Installed harnesses with fresh usable managed accounts remain selectable despite device logout; eight negative controls retain the native menu boundary. Pending/managed choices neither execute nor display cached device model discovery. Explicit model entry remains available. Managed submission skips the device-only advisory probe; native submission retains it.

## Failed-first evidence and self-review

All logs below are under `/var/tmp/pr-5769-next-79.wos4rh` and included in the evidence manifest:

- `initial-account-http-red.log`: explicit initial intent was discarded.
- `initial-account-store-red.log`: durable atomic creation was absent.
- `initial-account-launch-boundary-red.log`: the first launch did not see the required account boundary.
- `initial-account-client-boundaries-red.log`: delegation and capability handling lacked the initial intent contract.
- `initial-account-background-red.log`: managed or unreadable bindings could fall through to unbound native background work.
- `initial-account-renderer-red.log`, `initial-account-renderer-error-red.log`: missing picker, explicit choice and safe error behavior.
- `initial-account-desktop-boundary-red.log`: the real harness menu hid the managed-capable option, and cached native models triggered refresh before selection.
- `initial-account-native-preflight-red.log`: managed submission still ran device-only readiness.

Midpoint review added atomic prepared promotion and exact automation adoption, capability discovery, durable-before-async-launch ordering, and native background input leases. Managed title generation now retains a deterministic local title instead of creating an unbound provider process. The real Electron check caught a component-test mock that bypassed the actual menu filter; new dialog tests exercise the real field. Native model state remains stable during a capability recheck, and no unbound model result becomes managed-account evidence.

One introduced provider-union type mismatch failed the first renderer typecheck. It was corrected before the final complete rerun. Earlier renderer logs are diagnostic only. No protected native switching or Subscriptions source was changed.

## Final observed verification

Toolchains: Go 1.27.1 and Node 24.21.0. Go checks use stripped credential environment, owned temporary/runtime directories, `GOMAXPROCS=2`, `-mod=readonly`, serial package execution and a deterministic shell for process fixtures.

| Check | Observed result | Evidence |
| --- | --- | --- |
| Six-package initial-selection matrix, race count 3 | Exit 0, 141.389s | `initial-final-backend-race3.log` |
| Full manager, session service, HTTP controller, daemon, SQLite store and CLI race suites | Exit 0, 780.232s | `initial-final-backend-race.log` |
| API specification and parity | Exit 0 | `initial-final-backend-contract.log` |
| Backend build and vet | Both exit 0 | `initial-final-backend-build.log`, `initial-final-backend-vet.log` |
| Pinned changed-scope backend lint | Exit 0, zero issues | `initial-final-backend-lint.log` |
| Nine focused frontend files | 208 passes | `initial-renderer-v2-focused.log` |
| Full frontend | 350 files, 5,583 passes, seven skips | `initial-renderer-v2-full.log` |
| Frontend and E2E typechecks | Both exit 0 | `initial-renderer-v2-typecheck.log`, `initial-renderer-v2-e2e-typecheck.log` |
| Real Linux desktop package | Exit 0 | `initial-renderer-v2-build.log` |
| API and SQL regeneration | 33 files, zero drift | `initial-generated-drift-final.json` |
| Protected inventory | All 91 match the upstream-adjusted integration baseline | `initial-renderer-protected.log` and freeze-time recheck |

`initial-final-backend-results.json` and `initial-renderer-v2-results.json` confirm complete successful command lists and unchanged tested bytes. The sealing step independently rechecked both source manifests. The original 91-path protection manifest remains byte-identical; its two prior main-only differences remain documented in P0.

The first broad frontend diagnostic required a signature-verified local ZIP utility and rebuilding the installed SQLite native dependency for the pinned Node ABI. No source workaround, dependency-lock edit or test exclusion was used. Desktop packaging then rebuilds its own Electron ABI. Full branch lint, native platform workflows and live-provider checks are not implied by these results.

## Desktop evidence and limits

The isolated real Electron checkout ran source commit `9b0e00b5e9add49a78e71c83e44ac2c2072ec95e`; its code matches this seal, while the later plan text differs. It has its own home, app profile and data directory. No user credentials were copied. A native Electron user agent and preload bridge, production loopback daemon, actual provider catalog and capability response were checked directly.

`initial-desktop/correction-captures-v3` contains two inspected screenshots, a four-second recording made from the native window, and `evidence.json`. The empty managed inventory reports ready with zero accounts and does not unlock a signed-out native harness. No runtime task was launched. This is unavailable-state evidence, not proof of managed positive selection or A/B provider execution. An unrelated installed harness's model-discovery failure remains visible and was not hidden to improve the capture.

## Independent review request

Return CLEAR or bounded findings for initial choice propagation, atomic durability, deletion race admission, automation idempotency, background authorization, CLI compatibility and renderer request causality. Check the real-field tests and the distinction between native login and managed availability. Reviewers should use the archive rather than infer the snapshot from the evolving live worktree.

Remaining release gates: managed app-server execution; the escaped-descendant deletion/containment defect; credential-kind, usage and Harness completion; native Windows and both Mac architectures; real A/B provider sessions; restart/revocation/queue evidence; responsiveness; full branch lint/tests and final integrated review. No release-complete claim or publication accompanies this handoff.
