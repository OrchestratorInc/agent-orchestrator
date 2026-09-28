# Independent review: credential format and availability

Review the immutable archive. This is a bounded input/display correction, not completed native-token profiles or release acceptance. No publication accompanies it.

Base: `4440f579639285f4d7ae492ae53b265eddb44b8a`.
Source tree: `ba2d1198a6b4e5c331491d7c996bc91f1fe9f4f4`.
Artifact root: `/var/tmp/pr-5769-next-79.wos4rh/credential-freeze`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `credential-correction.sha256` | 30 source/test/generated files | `978feb3490d3ad4efffb02c881f65771c7c4d1c1b224d1fa0853a063974f62a6` |
| `integrated-source.sha256` | 5,707 files | `2d22c2092eabc1ac44b59195d8d916eb4e797c1d6d8bd9c0f3e973d44df28427` |
| `plan.sha256` | 1 file | `8769ca352a9acdbcc848614af925a60e9740a9f36a51a9d3597364ddbd8b8b17` |
| `protected.sha256` | 91 files | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `generated.sha256` | 33 files | `ccfb0a3263f7bf322af29bb35e880334d515572c15d3e01965d25317f2ca42dd` |
| `evidence.sha256` | 108 logs/results/captures | `dd1d6906a625b4df4c0ddc3b2428a04ce9e736ffc2e33a2484733e182dee05f5` |
| `source.tar` | Full source | `44c30c4828d79e629fabb68ed1f27f28df76f481d0231faadb1f4d4f9b4ece44` |
| `correction.tar` | Correction and plan | `b2a10afc945b92c2d25824e5ba20bc8444de5da4899d6c487c71071192ccb590` |
| `correction.patch` | Exact delta | `c083e48c1ab5eac55758102ff7738373c3a6b53c51f9f31b0f2a6b99e80bbf76` |

The correction manifest and `SUMMARY.json` contain the complete 30-file list. This handoff and the execution ledger are outside the archive. Their creation closes C6's local seal/handoff obligation, not independent acceptance. All four earlier integration/switching/initial-selection/lint manifests and archives were rehashed unchanged. All 91 live protected files match the integrated baseline; the original protection manifest is unchanged.

## Review contract

1. Known sign-in-token input is rejected by the API-key form before private transport, provider verification, durable operation creation or publication. A format prefix does not prove validity. Existing validation/error redaction stays intact.
2. Legacy token-shaped API-key records project as `access_token`. Projection preserves secret bytes, identity, generation, prior verification, readiness, admission, bindings and defaults, including vault reopen. It neither migrates data nor grants usage permission. Contradictory private quota flags cannot permit usage read/reset.
3. The renderer separates API-key billing from subscription quota and explains missing token permission. Format belongs in the usage cache identity; a late request cannot overwrite a new unsupported state. Existing exhausted/stale/error usage semantics remain covered.
4. Harness shows managed availability beside unchanged native status and controls. Fresh inventory, installation, provider, verification and availability are distinct. No implicit selection or claim that managed app-server Chat already works.
5. The new public kind is regenerated through OpenAPI and frontend types. HTTP, CLI and renderer retain the known safe error category/request ID, without retaining raw server text or private endpoints. All new text exists in eight locale catalogs.

## Failed-first and midpoint corrections

Logs are under `/var/tmp/pr-5769-next-79.wos4rh` and listed in the evidence manifest.

- Valid red evidence: `credential-format-red-runner.log`, `credential-format-red-management.log`, `credential-format-red-http-valid.log`, `credential-format-quota-red.log`, `credential-format-quota-commands-red.log`, `credential-experience-ui-red.log`, `credential-usage-causality-red-valid.log` and `credential-error-code-red-valid.log`.
- Midpoint review corrected contradictory quota permission and usage-cache causality. An exploratory proposal to disable existing token records was rejected after tracing distinct engine execution paths. The `credential-format-admission-red-*` logs test that rejected policy, not a proven defect. No production admission change was made.
- Real Electron caught the renderer's missing safe-code allowlist entry. The original component mock had bypassed normalization. The final component test uses the actual mutation helper; both normalization and UI fail before the one-entry correction. `credential-desktop-capture.log` and `credential-desktop-rejection-red.json` preserve the real failure.
- Diagnostic only: the first HTTP fixture lacked its JSON content type, an initial cache test lacked a decisive abort assertion, and one test command used the wrong external wrapper path. Two introduced test-only type errors were corrected. Host Go selection caused the first packaging failure; Go 1.27.1 fixes that environment. One public prefix gets a narrow lint explanation, with no package suppression.

## Final verification

Node 24.21.0, Go 1.27.1, pinned lint 2.13.2. Commands use credential-stripped environments and private temporary storage. Exact arguments, exits and tested hashes are in the final JSON files.

- `credential-backend-final2-results.json`: 11 commands, all exit 0, tested files unchanged. Focused runner and backend races repeat three times. Complete races pass for core management, account service, HTTP controllers and CLI (216.460s command); API spec/parity race passes (34.988s); full runner race passes (9.990s). Backend/runner build and vet pass. Complete backend/runner lint reports zero findings.
- `credential-frontend-final3-results.json`: six commands, all exit 0, tested files unchanged. Ten focused files pass 177 tests. Full suite: 351 files, 5,606 passes, seven existing skips. Main/E2E typechecks and Linux desktop packaging pass. The private ZIP executable resolves the inherited missing-tool fixture limitation.
- `credential-generated-drift.json`: API and SQL regeneration matches the accepted bytes across 33 artifacts, zero drift. No SQL contract changes in this slice.
- Final `git diff --check` and protected/archive integrity checks pass. Full-branch containment, native platforms and all workflow environments are not covered by these scoped passes.

## Actual desktop evidence

Capture root: `/var/tmp/pr-5769-next-79.wos4rh/initial-desktop/credential-captures-v2`.
Detached desktop commit: `9859e5593ff1693de82ad931d9ca596fa33d7838`.
All 30 correction files match the frozen source; subsequent documentation is separate. The app used its real preload bridge, actual daemon and provider catalog with isolated data at `initial-desktop/ao-home/initial/data`, not the user's profile. Daemon: `127.0.0.1:46125`; renderer: `http://localhost:5174/`. The owned app/listener were stopped after capture.

Inspected screenshots: `api-key-guidance.png`, `wrong-method-rejected.png`, `separate-harness-availability.png`. Recording: `credential-experience.mp4`, 1.68 seconds, 56 captured frames and 26 encoded frames; sampled native frames were inspected. `evidence.json` records HTTP 400, the specific safe error/request ID, zero accounts before/after and no renderer errors.

No live credential was copied or used. There is no positive quota or A/B provider evidence from this empty profile. Screenshots are local artifacts, not published reviewer links.

## Requested decision

Return CLEAR or bounded findings against this archive. Focus on non-mutating legacy projection, wrong-method input ordering, error-code normalization, usage causality and native/managed status separation. Do not treat pending earlier review requests as cleared.

Still open: provider-supported isolated native profiles and explicit migration/reconnect; managed app-server execution; escaped-descendant retirement; native Windows and both Mac architectures; live A/B/restart/revocation/queue evidence; final integrated tests, responsiveness and independent review. Deletion remains held. No publication or release-completion claim.
