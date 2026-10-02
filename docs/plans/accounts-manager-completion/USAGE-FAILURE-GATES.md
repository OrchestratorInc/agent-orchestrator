# Gates: usage lookup failure

OWNS: accounts-manager/runner/internal/runner/credential_quota*, accounts-manager/runner/internal/runner/credential_verification*, accounts-manager/runner/internal/runner/credential_http.go, backend/internal/accountsmanager/management_capabilities*, backend/internal/httpd/controllers/accounts_manager*, backend/internal/httpd/apispec/specgen/build.go, backend/internal/httpd/apispec/openapi.yaml, frontend/src/api/schema.ts, frontend/src/renderer/components/settings/AccountUsage*, frontend/src/renderer/lib/accounts-manager-controls*, frontend/src/renderer/i18n/*.json, docs/plans/accounts-manager-completion/USAGE-FAILURE*

Scope: fix the browser-account usage lookup reproduced on 2026-09-28 without changing credentials, account selection, native switching, or Subscriptions.

## Plan

1. Preserve the existing review archive. Add failing regressions for the lost upstream error categories. Retain only fixed categories, never tokens, URLs, or provider bodies.
2. Observe the actual failure through the isolated desktop runner. Correct the smallest demonstrated request or parsing defect, with a failed-first regression. No guessed credential replacements or authorization changes.
3. Self-review and run runner race, affected backend race, build/vet, focused frontend tests, typecheck, changed-scope lint, and generated contract checks where applicable.
4. Update the isolated desktop checkout, verify actual usage or an exact external limitation, inspect native evidence, and seal this delta separately for review. No publication.

Evidence root: `/tmp/pr-5769-usage-failure-79.Rucxda`.

- [x] G1: quota failures retain a secret-safe category and request ID instead of a generic operation failure
  CHECK: env GOWORK=off node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs go test -mod=readonly -race -p=1 ./internal/runner -run 'TestCredentialQuota' -count=3 -timeout=180s
  CWD: accounts-manager/runner
  EXPECT: ok
  EVIDENCE: `categories-red.log`, `http-categories-red.log`, and `ui-red.log` reproduce lost categories. `parser-green.log`, `http-green.log`, and final full suites pass. Authentication, access denial, rate limiting, service outage, and malformed-response categories retain the public request ID without provider bodies or private endpoints.

- [x] G2: the confirmed root cause has a failed-first regression and a verified correction
  EVIDENCE: `parser-red.log` contains four assertion failures for unrelated metadata in otherwise valid observations. `parser-green.log` passes three times. `diagnostic.json` records HTTP 502 `quota_response_invalid`; `fixed.json` records HTTP 200 and two provider-reported windows after the parser correction.

- [x] G3: affected full suites, build, vet, lint, and generated contracts pass on the final source snapshot
  EVIDENCE: `runner-full-race.log` and `backend-full-affected-race.log` pass three times. `frontend-full-final.log` records 341 passing files, 5,432 passing tests, and seven skips. Backend and runner build/vet pass. `backend-lint.log` and `runner-lint-final.log` report zero issues. `typecheck-final.log`, `renderer-build-final.log`, and `api-drift-final.log` pass. Regeneration leaves the entire 23-file source manifest unchanged. These are the full affected packages, not a claim that the entire backend or PR is verified.

- [x] G4: the native desktop shows real quota values for the reported browser account, or an exact external blocker is documented without claiming success
  EVIDENCE: `desktop-evidence.json` records native Electron, initial HTTP 200, refresh HTTP 200, two quota bars matching the live response, and unchanged account identities/selections. `usage-refreshed.png` and the 4.84-second `usage-refresh-with-hold.mp4` were inspected. `final-audit.json` verifies both running executables against the tested builds. No response interception or mock data was used.

- [x] G5: protected paths and prior archives are unchanged, with a separate exact correction manifest
  EVIDENCE: `final-audit.json` verifies all 91 protected paths, the prior review seal and every artifact it hashes, all 23 worker and desktop source files, and unchanged account identities/routing. `source.sha256` has SHA256 `285ebe9570fcf958b042b3f52e5a87b31ccdfd428b96af74d51181a7d0e2f587`; `source.tar.gz` has SHA256 `4a2d40d212d863bd00611c812ce60c00dd36e139bd8977452ec0b6b5e6ceb1ee`.

## Baseline

The screenshot request ending `000880` returned public HTTP 502. A read-only diagnostic confirmed the browser account is OAuth, verified, quota-supported, enabled, and available. The runner returned HTTP 424 `credential_verification_unavailable`. The current request helper collapses non-200, transport, and read errors; quota parsing uses the same error. No exact provider cause has yet been established.

The prior correction manifest `/tmp/pr-5769-credential-usage-79.AVs5wx/correction-source.sha256` and the 91-path protected manifest both matched before this slice. The prior archive remains immutable; its source manifest will describe the earlier snapshot after this correction changes live files.

## Midpoint review

The diagnostic runner reproduced HTTP 502 `quota_response_invalid` for the same browser account, which distinguishes successful upstream retrieval followed by parser rejection from HTTP refusal. Four synthetic valid-window responses with unrelated scalar/object/array metadata fail before correction. Parsing now uses a struct containing only supported window fields, so unrelated metadata cannot invalidate a valid observation.

The seven-category runner matrix, public HTTP category/request-ID matrix, and renderer category wording matrix failed before their respective corrections. Focused runner and HTTP race checks passed three times after correction. The first frontend pass includes 65 passing tests. Live successful quota is not yet observed.

Self-review: preserve the existing invalid-response sentinel through the new safe quota error type; retain lifecycle conflict/not-found errors instead of converting them into provider failures; add renderer allowlist and prototype-name negative controls. No automatic retry, credential mutation, account fallback, provider body retention, or private endpoint exposure is introduced. Exact live data still determines the final root-cause verdict.

## Final review

The same browser account now returns two valid windows in the native app. The typed parser ignores unrelated metadata but still rejects malformed supported windows and missing observations. Zero utilization remains a real 100-percent-remaining observation. Errors cannot invent zero usage, change verification, or select another account. Tests retain stale-observation labeling after a refresh failure.

The first typecheck found an introduced string-to-message-key mismatch; the mapping now uses the established `MessageKey` type. The final typecheck and complete frontend suite pass. An initial frontend invocation could not find npm because of shell PATH construction; the corrected invocation ran every test. Earlier logs remain preserved and are not counted as passes.

Bounded fix complete locally. Independent re-review and broader PR release gates remain open. No commit, push, PR edit, evidence publication, or changes to credentials were made. The desktop remains running for user testing.
