# Independent review: lint and boundary corrections

Review the immutable archive, not a later live working tree. Local verification passes; independent acceptance and broader release gates remain open. No publication accompanies this handoff.

## Exact snapshot

Base: `c399189d241c707fdd40e2373a82c1028820636a`.
Source tree: `b730765f4fcb099a7522c97f1fe8b37df3ff87b4`.
Artifact root: `/var/tmp/pr-5769-next-79.wos4rh/lint-freeze`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `lint-correction.sha256` | 56 source/test files | `f769d644ae13dcf7a84d80895ea509a122197f90f72bae552416f7e379ae8da1` |
| `integrated-source.sha256` | 5,699 files | `801b8e9c295316765875eeab739547c09b1c85f1c98d835b1515a48fa7e41915` |
| `plan.sha256` | 1 file | `91165ac2fe01ec112046f615f3c2154f9361ea7d3d9f3895871bf3eb37a9c7c6` |
| `protected.sha256` | 91 files | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `generated.sha256` | 33 files | `9ec0320cc5304386fe5abee3f74d09a27b15c358a583c7d357a5ea8bf2249d01` |
| `evidence.sha256` | 31 logs/results | `7aa74366ffe68770c900dec00ab222ea0a952dfe23ff16dfbffe016595fab1dd` |
| `source.tar` | Full source | `167b4edee56f977607a3b6302aeaf293872e99df34040bd792d72fd2fb5753a2` |
| `correction.tar` | Correction and plan | `89a764a902f0c5d248bc44bda96f005df4f46aba46f89ea5fdd58d97458dbefd` |
| `correction.patch` | Exact delta | `60a8f9ed097db29101923005b5e1c8f33472ebbf534f75b8d4154519f4bf6364` |

`SUMMARY.json` contains the complete file list, archives and evidence paths. This handoff and subsequent ledger entries sit outside the archived source tree. The archive closes plan gate L5's local handoff obligation; it does not close independent review.

## Review focus

- Missing Chat handoff capability rejects readiness instead of panicking. Malformed internal route-cache values reject without admission or fallback.
- Failed HTTP-body cleanup cannot return a verified payload or successful private response. Public errors remain sanitized.
- Callback listener closure precedes credential commit. An authenticator-owned already-closed listener remains valid; an unexpected close failure cancels the durable operation. Midpoint review caught and corrected the initial error-after-commit ordering.
- Vault file/directory and scratch cleanup propagate errors conservatively. AES-GCM layout and format authentication remain unchanged. OS descriptor/directory-close failures were not fault-injected; do not mistake source review for native filesystem fault evidence.
- Remaining changes are contract documentation, standard HTTP constants, equivalent predicates, dead-wrapper removal and test style. Existing lint configuration is unchanged. Narrow annotations cover public path/format literals, fixed self-execution and an intentionally longer-lived legacy observer, not secret material or a new shutdown guarantee.

## Failed-first and final checks

Logs live in `/var/tmp/pr-5769-next-79.wos4rh` and are hashed by `evidence.sha256`.

- Full lint baseline: 86 backend and 53 runner findings. Final pinned version 2.13.2 reports zero for both complete scopes.
- Valid failed-first logs: `lint-backend-boundary-red.log`, `lint-runner-boundary-red-valid.log`, `lint-oauth-close-red.log`, `lint-browser-order-red.log`. The first runner cleanup fixture with nil headers was invalid and is not acceptance evidence.
- `lint-final2-results.json`: all 11 serial commands pass and tested files remain unchanged. Runner focused race repeats three times, 11.344s. Backend focused race repeats three times, 233.048s. Its runtime-selection filter selects no tests; the full suite below covers that package.
- Complete affected backend race suites: all 13 packages pass, 751.649s command time. Full runner race passes, 10.016s. Backend/runner build and vet plus tagged manager vet pass.
- `lint-final-process-results.json`: four actual-process top-level tests pass, direct/fallback, cold recovery, cancelled retry and ownership preservation, with no skips. Command time 83.604s; package time 73.150s. Synthetic protocol frames, no live provider credentials.
- API and SQL regeneration: zero drift across 33 generated files. All 91 protected hashes pass. The original protection manifest and prior initial-selection archives/manifests remain byte-identical.

The earlier `lint-final` serial command hit an external 300-second wrapper limit. It is retained as diagnostic only; `lint-final2` completed. A first generation command failed on PATH before generation, then the corrected command passed. Exact command arrays and exit statuses are in the final result files. No frontend behavior changed in this correction, so new screenshots are not applicable.

## Requested decision and remaining gates

Return CLEAR or bounded findings against this exact snapshot. Review error ordering, cancellation, vault admission, typed capability rejection and the annotations in particular. This request does not supersede pending integration, switching or initial-selection reviews.

Deletion remains held on the separately reproduced escaped-descendant defect. Managed app-server execution, credential-kind/usage/Harness work, native Windows and both Mac architectures, live A/B provider sessions, final integrated desktop evidence, responsiveness and full-branch release verification remain open. No native or live-provider success is claimed.
