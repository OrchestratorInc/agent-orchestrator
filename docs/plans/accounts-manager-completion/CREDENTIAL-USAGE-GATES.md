# Gates: credential verification and usage controls

OWNS: accounts-manager/runner/internal/runner/credential_*.go, accounts-manager/runner/internal/runner/{control,serve,integration_test}.go, backend/internal/accountsmanager/{management_*,supervisor}.go, backend/internal/service/accountsmanager/service.go, backend/internal/httpd/controllers/accounts_manager*.go, backend/internal/httpd/controllers/dto.go, backend/internal/httpd/apispec/openapi.yaml, frontend/src/api/schema.ts, frontend/src/renderer/components/SessionAccountControl*, frontend/src/renderer/components/settings/AccountsManagerSection*, frontend/src/renderer/components/settings/AccountUsage*, frontend/src/renderer/hooks/useAccountsManagerQuery.ts, frontend/src/renderer/lib/accounts-manager-controls.ts, frontend/src/renderer/i18n/*.json, docs/plans/accounts-manager-completion/CREDENTIAL-USAGE*

Scope: verified manual credentials and truthful per-account usage without changing protected switching, subscription, or platform lifecycle behavior.

- [x] V1: provider rejection is reproduced before correction, with zero credential commit or route availability.
  EVIDENCE: verification-red.log captures both providers returning 200 instead of 422. runner-final-proof-race.log passes the corrected no-commit regression three times. desktop-restart-verify.log records real upstream rejection for both synthetic bad keys with unchanged inventory.
- [x] V2: bounded verification covers authenticated success, rejection, transient failure, redirects, body limits, cancellation, and safe errors.
  CHECK: env GOWORK=off go test -mod=readonly -race ./internal/runner -run 'TestCredentialVerification' -count=3 -timeout=180s
  EXPECT: /ok\s+.*internal\/runner/
  CWD: accounts-manager/runner
  EVIDENCE: runner-final-proof-race.log passes the complete runner module three times under race. controllers-final-race.log passes the complete controller package three times, including safe invalid-versus-unavailable errors and request IDs.
- [x] V3: saved unverified credentials cannot authorize new work; explicit recheck is durable, generation-bound, and idempotent across retry/restart.
  CHECK: env GOWORK=off go test -mod=readonly -race ./internal/runner -run 'TestCredentialVerificationLifecycle' -count=3 -timeout=180s
  EXPECT: /ok\s+.*internal\/runner/
  CWD: accounts-manager/runner
  EVIDENCE: replacement-red.log proves the adjacent replacement/replay defect before correction. Durable proof now includes a normalized credential fingerprint. runner-final-proof-race.log passes replacement, lifecycle, refresh, reconnect and integration tests three times. The real app and runner restarted with four unverified accounts preserved; runtime-identity.json matches the running executable to the final binary.
- [x] U1: supported quota windows, freshness, coalescing, and failure isolation pass runner and management boundary tests.
  EVIDENCE: quota-red.log captures the original 404. runner-final-proof-race.log and backend-verified-race.log pass parsing, missing/invalid measurements, coalescing, cache, removal and quota-failure isolation. No account or default is selected by usage checks.
- [x] U2: desktop tests prove usage rendering, unavailable/error states, explicit refresh, stale-response rejection, and unchanged explicit account choice.
  EVIDENCE: ui-red.log precedes implementation. ui-expanded.log passes 81 focused tests. frontend-final-full.log passes all 341 files, 5419 tests and seven existing skips after environment repair, with no suite exclusion. All locale coverage is included. Live successful quota remains unobserved without a verified supported account.
- [ ] I1: final affected package suites, build/vet/lint, frontend typecheck/build, generated drift, and protected-path preservation have final-snapshot evidence.
  EVIDENCE: Bounded checks pass: runner/backend build and vet, complete runner race x3, management/service/controller/spec race x3, full frontend suite, typecheck, Linux desktop package, both changed-scope linters (0 issues), generated-drift.log and protected-audit.log (91 files). Expanded routing-compatibility-race.log retains a daemon production-readiness fixture failure in both target modes. production-control-baseline.log reproduces it on the preserved pre-correction tree. No unrelated readiness or shutdown code was edited. Full-branch acceptance stays open. This local frontend run uses Node 22, not CI Node 24.
- [x] I2: isolated real desktop captures are inspected, exact delta is frozen, and remaining live-provider/platform gaps are reported for independent review.
  EVIDENCE: CREDENTIAL-USAGE-REVIEW.md and review-seal.json index the exact source, patch, archive, verification logs and captures. Screenshots and decoded recording frames were inspected. Native platform, successful live quota, scope-limited token and production A/B runtime gates remain open. Independent review is requested, not yet CLEAR.

Artifact root: `/tmp/pr-5769-credential-usage-79.AVs5wx`. This ledger covers the bounded correction and does not certify the whole PR.
