# Reviewer82: corrected PC2 HTTP boundary

Request: re-review F1 from `/tmp/pr-5769-pc2-review-82.txt` against this exact snapshot. Local verification is complete; independent CLEAR is pending. Stop after this review. CLI, UI, guest and lifecycle implementation remain paused. No publication.

## Exact target

- Worktree: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
- Branch: `ao/agent-orchestrator-79/accounts-manager`; HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`.
- Evidence root: `/tmp/pr-5769-pc2-f1-79.E8WKAc`.
- Authoritative source manifest: `pc2-source.sha256`, 13 files, SHA256 `a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9`.
- Source archive: `pc2-source.tar.gz`, the same 13 entries, SHA256 `8fe33f511aecc9484d596e06adb23e1272472723c4970397156cfeb9d2faa1fd`.
- Exact correction: `pc2-f1-correction.patch`, SHA256 `92fd67c6965c90ac8242b6a0b144c07fdf3ace76b6ffa75de37ba993a7e355fa`. One production validator changed; one test file was added.
- `pc2-slice.sha256` covers these source files plus this handoff and the two PC2 gate documents. `pc2-review.json` records its digest and the final results. Neither inventory includes CLI production or guest/lifecycle changes.

This supersedes the rejected twelve-file source manifest at `/tmp/pr-5769-public-api-79.xfBKWE/pc2-source.sha256` as the review target. That prior evidence remains preserved; its passing tests did not cover F1. The old handoff is historical, not corrected-snapshot acceptance.

Exact source inventory:

```text
backend/internal/httpd/accounts_manager_controls_test.go
backend/internal/httpd/api.go
backend/internal/httpd/apispec/openapi.yaml
backend/internal/httpd/apispec/specgen/accounts_manager_controls.go
backend/internal/httpd/apispec/specgen/build.go
backend/internal/httpd/controllers/accounts_manager.go
backend/internal/httpd/controllers/accounts_manager_controls.go
backend/internal/httpd/controllers/accounts_manager_controls_dto.go
backend/internal/httpd/controllers/accounts_manager_controls_service_test.go
backend/internal/httpd/controllers/accounts_manager_controls_test.go
backend/internal/httpd/controllers/accounts_manager_controls_validation_test.go
backend/internal/httpd/controllers/accounts_manager_session_controls_test.go
frontend/src/api/schema.ts
```

## Correction and failed-first evidence

The first parser pass now requires each decoded member name to match an exact allowed JSON tag. It rejects repeated decoded canonical names. Switch and removal have separate allowed sets; retry/cancel have an empty set. This replaces lowercasing-based equivalence, not the standard value decoder. The existing struct decoder, unknown-field rejection, null rejection, body limit and trailing-value checks remain.

Literal and escaped U+017F alias pairs are rejected before delegation in both orders. Exact JSON escapes such as `expected\u0052evision` remain valid because the decoded name is canonical. Explicit removal revision zero remains valid; missing/null revision remains invalid. No blacklist or special-case Unicode substitution was introduced.

Failed-first command, backend CWD, Fish orchestration, isolated environment:

```text
go test -mod=readonly -p=1 ./internal/httpd/controllers -run '^TestAccountControlUnicode' -count=1 -timeout=3m -v
```

Observed exit 1, 32 failed leaves. `unicode-red.log` SHA256: `b49f280d0e98dabc0a2ed009c9551bf4a7210223f7f161ffb35e6001166bfb16`. Besides accepted fake-service requests, the real SQLite admission cases created switch/removal journals and a deletion fence. The corrected cases require HTTP400, the original request ID, zero admission calls, no operation rows, unchanged account and session fences, binding and impact.

The exact reviewed validator is preserved as `reviewed-controls.go.txt`, SHA256 `273b97fda5afa3fda02654f866e372397a693becd777d3bfde73877736421bb1`. The initial regression is `unicode-red-test.go.txt`, SHA256 `4e07f4a0371940ce7075e57b39ed62711ab196972a1bb850af2a0ba743aa068e`. Its assertions are unchanged in the final test file; only the additional boundary tests and their import were appended.

## Additional boundary coverage

- Nonempty retry/cancel bodies on both resources return correlated HTTP400 with no mutation call; the existing operation lookup remains read-only.
- Foreign operation IDs fail before mutation. Changed operation IDs or switch session identity after mutation are not projected as success.
- Explicit native mode reaches the boundary without an account ID. Escaped canonical tags and both explicit history-choice booleans remain valid; repeated escaped/plain canonical names are rejected in both orders.
- Every switch/removal scalar field rejects null, including the optional history flag's existing strict behavior.
- Valid JSON padded to 4095 and 4096 bytes succeeds; 4097 bytes returns correlated HTTP413 before mutation, across both admissions and all four retry/cancel routes.

Midpoint self-review checked the two-pass parser against DTO tags, presence semantics, decoded-key duplicates, value decoding and the empty mutation shape. The correction changes no route, DTO, generated contract, durable service or runtime behavior. Service-backed tests exercise actual SQLite admission, not controller launches or active runtime recovery.

## Final commands and observed results

All commands ran serially on the final source bytes. Backend commands used `env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs`. The reviewed wrapper removes service credential variables. Fish orchestrated commands and preserved exit status through each log pipe.

Paths in the log column are relative to the evidence root. Every final check below exited 0.

| Check | Exact command | Log and result |
| --- | --- | --- |
| Focused controller race | `go test -mod=readonly -p=1 -race ./internal/httpd/controllers -run '^TestAccountControl' -count=3 -timeout=3m -v` | `focused-controller-race.log`, 24.938s, 369 passing leaf executions, 123 distinct leaves, no failures/skips/race warnings |
| Full controller race | `go test -mod=readonly -p=1 -race ./internal/httpd/controllers -count=3 -timeout=4m` | `final-controller-race.log`, 134.149s |
| API/spec generation and route parity race | `go test -mod=readonly -p=1 -race ./internal/httpd/apispec/... -count=3 -timeout=3m` | `final-route-spec-race.log`, 1.824s and 82.341s |
| Production API dependency construction | `go test -mod=readonly -p=1 -race ./internal/httpd -run '^TestAccountsManagerControlsDependencyWiring$' -count=3 -timeout=2m` | `final-wiring-race.log`, 1.251s |
| Backend build | `go build -mod=readonly -p=1 ./...` | `final-backend-build.log`, exit 0 |
| Full backend vet | `go vet -mod=readonly -p=1 ./...` | `final-backend-vet.log`, exit 0 |
| Changed-scope lint | `/tmp/pr-5769-life-f2f3-79.CYTx36/golangci-lint-go1271 run --timeout=3m --concurrency=2 --new-from-patch=/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-final-backend.patch ./internal/httpd/controllers ./internal/httpd/apispec/... ./internal/httpd` | `lint.log`, pinned v2.13.2 / Go1.27.1, 0 issues |
| API artifacts, root CWD | `npm run api` | `final-api-generate.log`, exit 0 |
| Generated drift, root CWD | Compare both generated SHA256 values against `pc2-source.sha256` after regeneration | `final-api-drift.log`, `API_GENERATED_DRIFT_PASS` |
| Frontend typecheck, root CWD | `npm --prefix frontend run typecheck` | `final-frontend-typecheck.log`, exit 0 |
| Whitespace | `git diff --check` | exit 0, repeated after document edits |

The build/vet logs are empty on success; their status was observed in the fail-fast serial command, which completed with `PC2_F1_BACKEND_CHECKS_PASS`. The artifact/typecheck command completed with `PC2_F1_CONTRACT_CHECKS_PASS`. No source edits followed these runs. Generated YAML and TypeScript are byte-identical to the previous contract.

## Preservation and remaining gates

Both required manifests were checked before correction and again after validation, with zero mismatches:

- 113 lifecycle/design files: `/tmp/pr-5769-public-controls-79.PbwFW0/lifecycle-guest-frozen.sha256`, SHA256 `ffc37c82bafedcf7223478d3d4a6ff6aefa97dea9f04b615b63ef1df66d3e96b`.
- 91 protected native/Subscriptions paths: `/tmp/pr-5769-public-controls-79.PbwFW0/protected.sha256`, SHA256 `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358`.

The five-file prior guest review package also matches. `final-preservation.json` records the exact audits. The newly written protocol draft and its continuation ledger were outside these inventories; they are recoverably parked in `/tmp/pr-5769-pc2-f1-79.E8WKAc/guest-protocol-deferred.patch`, SHA256 `1f37f56f1057bda08cf627b35ab293dfd2ed7fe59ad336f47fd1fb1377ade27f`, and absent from the live tree. No earlier accepted guest design was discarded.

CLI production remains parked at `/tmp/pr-5769-public-api-79.xfBKWE/pc3-deferred.patch`, SHA256 `47017f5535c3cae86ea280840d4c0f317da166c7c85be35e4ae291401a623fa0`. `git diff HEAD -- backend/internal/cli/session.go` is empty; the proposed production file and registration are absent. The retained red CLI test hashes `10f565d845ef1c6d2b6d1adbef740d44c94d430180113edad0f8bcf8d2fcf0ee`, unchanged from the accepted baseline. Activity mentioning `session.go` was read/diff inspection, not an edit.

No full-branch test pass, desktop behavior, live-provider execution or platform acceptance is claimed. Production control-service wiring remains nil/unavailable. Native Windows has no attached runner and hosted execution is unauthorized. Guest keeper-death/escaped-descendant proof, lifecycle completion, CLI/UI, real desktop/provider and performance remain release gates. No screenshots are applicable to this backend-only correction.

Please re-run the Unicode and adjacent boundary cases against the source manifest and return bounded PC2 CLEAR or specific findings. This is not a request to clear runtime deletion or release the product. Work pauses here for reviewer82.

Fun fact: JSON escapes are decoded before member names are matched.
