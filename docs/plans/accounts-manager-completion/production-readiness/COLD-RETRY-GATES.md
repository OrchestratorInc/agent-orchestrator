# Gates: cold switch retry outage

OWNS: backend/internal/session_manager/accounts_manager_recovery.go, backend/internal/session_manager/accounts_manager_switch.go, backend/internal/session_manager/accounts_manager_cold_retry_test.go, backend/internal/session_manager/accounts_manager_cold_retry_e2e_test.go, docs/plans/accounts-manager-completion/production-readiness/COLD-RETRY-*.md

Scope: preserve explicitly requested cold recovery through target revalidation outages without crossing the stop boundary. Shell: fish. No containment enablement or publication.

- [x] G1: a failed-first assertion reproduces loss of pre-stop retry/cancel after target outage.
  CHECK: node /var/tmp/pr-5769-cold-switch-79.2u9WV2/verify.mjs red
  EXPECT: RED ASSERTIONS VERIFIED
  EVIDENCE: outage-red.log and its command/exit metadata record eight assertion failures on the pre-correction production files, not compile failures. Both handle forms and both pre-stop phases became failed with TARGET_UNAVAILABLE. Verification of that evidence exits zero.

- [x] G2: repeated outages, SQLite reopen, explicit resume/cancel and race controls pass three times.
  CHECK: go test -race ./internal/session_manager -run '^TestAccountsManagerColdRetry' -count=3 -v -timeout=3m
  CWD: backend
  EXPECT: PASS
  EVIDENCE: focused-final.log, fish, backend, exit 0, 105.892s. Three named tests, 19 leaf schedules per repetition, all repeated three times with no skips. Covers repeated outages/reopen, eventual resume/cancel, both cancellation barriers, historical terminal refusal and post-stop non-cancellability. Source hashes match source-verification-start.sha256.

- [x] G3: real direct/fallback runtimes survive outage and settle only after explicit recovery.
  CHECK: go test -tags=e2e -race ./internal/session_manager -run '^TestAccountsManagerColdRetryReal' -count=3 -v -timeout=3m
  CWD: backend
  EXPECT: PASS
  EVIDENCE: process-first.log, fish, backend, exit 0, 64.801s. Four actual-process schedules repeated three times without skips. Production runtime construction, live supervised source, two SQLite/new-manager reconstructions, zero effects while validation is unavailable, surviving source on cancel, one interrupt and chosen target launch on retry. Synthetic provider output, not live credentials or complete runner/vault restart.

- [x] G4: midpoint review and final affected race/build/vet/lint checks pass on one snapshot.
  CHECK: node /var/tmp/pr-5769-cold-switch-79.2u9WV2/verify.mjs checks
  EXPECT: FINAL CHECKS VERIFIED
  EVIDENCE: midpoint findings and controls are in COLD-RETRY-REVIEW.md. switching-race.log passes three repeats, 204.127s. Full domain/store/session-service/Chat races pass (2.619s/174.874s/62.564s/254.668s). Full session-manager race passes, 218.656s. Backend build/vet and tagged session-manager vet exit 0. Pinned v2.13.2 changed-scope lint and explicit four-file patch lint both report 0 issues. All command/exit metadata use the credential-stripped run.mjs; no source edits after final verification began.

- [x] G5: exact source/docs/evidence are sealed and all previously frozen/protected bytes remain unchanged.
  CHECK: node /var/tmp/pr-5769-cold-switch-79.2u9WV2/verify.mjs seal
  EXPECT: EXACT SEAL VERIFIED
  EVIDENCE: cold-retry-v1 source/docs/evidence manifests, archives, summary and seal-report.md in /var/tmp/pr-5769-cold-switch-79.2u9WV2. Four cold source files and three cold documents are separate from the one-file inventory review addendum. Audit verifies the inventory seal (3 source/3 docs), earlier first-slice seal (18 source/5 docs), all 91 protected paths and nine unchanged containment files. API/SQL/dependency files match HEAD. Index empty, no commit/push/PR edit. Independent cold-retry review is requested, not yet CLEAR.
