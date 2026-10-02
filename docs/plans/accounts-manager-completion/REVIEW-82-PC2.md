# Reviewer82: current PC2 HTTP snapshot

Status: corrected F1 snapshot ready for bounded independent re-review. Local checks pass; reviewer82 CLEAR is pending. Implementation is paused. No CLI, UI, guest, lifecycle, commit or publication work is authorized by this handoff.

This replaces the rejected twelve-file target. The original handoff is preserved at `/tmp/pr-5769-pc2-f1-79.E8WKAc/prior-review-82-pc2.md`; prior logs and manifests remain historical evidence, not corrected-snapshot acceptance.

## Exact artifacts

- Worktree: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`; branch `ao/agent-orchestrator-79/accounts-manager`; HEAD `048a59775999b60f8276a1f5d1107dbef57f5483`.
- Source manifest: `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.sha256`, **13 files**, SHA256 `a3618c483f9411738333c1c0870f921552dd9c58d3a5ba5232f084c6649bddc9`.
- Immutable source archive: `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-source.tar.gz`, SHA256 `8fe33f511aecc9484d596e06adb23e1272472723c4970397156cfeb9d2faa1fd`.
- Two-file correction patch: `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-f1-correction.patch`, SHA256 `92fd67c6965c90ac8242b6a0b144c07fdf3ace76b6ffa75de37ba993a7e355fa`.
- Source plus PC2-only handoff/gate inventory: `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-slice.sha256`. Its digest and exact verification statuses are in `/tmp/pr-5769-pc2-f1-79.E8WKAc/pc2-review.json`.
- Detailed correction, all command lines/results, reviewer checklist and remaining gates: [REVIEW-82-PC2-F1.md](REVIEW-82-PC2-F1.md).
- Final preservation: `/tmp/pr-5769-pc2-f1-79.E8WKAc/final-preservation.json`. Gate ledger: [PC2-F1-GATES.md](PC2-F1-GATES.md), F1-F4 locally met, F5 independent re-review pending.

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

## Correction and verification

The validator now accepts exact decoded JSON tags and rejects repeated canonical names before the existing struct decoder. No case-fold blacklist or decoder-option change. Explicit removal revision zero, native mode and escaped canonical tags remain valid.

`/tmp/pr-5769-pc2-f1-79.E8WKAc/unicode-red.log` records the failed-first matrix: exit 1, 32 failed leaves, including actual SQLite journal and fence changes. SHA256 `b49f280d0e98dabc0a2ed009c9551bf4a7210223f7f161ffb35e6001166bfb16`. The corrected matrix requires HTTP400, request ID preservation, zero admission and unchanged durable state. Additional coverage rejects nonempty recovery bodies and identity mismatches, covers nullable fields, and checks 4095/4096/4097-byte requests.

All final checks exited 0 with Fish orchestration and isolated test environment:

| Check | Log under `/tmp/pr-5769-pc2-f1-79.E8WKAc` | Result |
| --- | --- | --- |
| Focused controller race, count3 | `focused-controller-race.log` | 24.938s, 369 passing leaf executions, no failures/skips/race warnings |
| Full controller race, count3 | `final-controller-race.log` | 134.149s |
| API/spec generation and route parity race, count3 | `final-route-spec-race.log` | 1.824s and 82.341s |
| Actual API dependency wiring race, count3 | `final-wiring-race.log` | 1.251s |
| Full backend build and vet | `final-backend-build.log`, `final-backend-vet.log` | Both exit 0 |
| Pinned changed-scope lint | `lint.log` | 0 issues |
| API regeneration and generated drift | `final-api-generate.log`, `final-api-drift.log` | Both generated files byte-identical |
| Frontend typecheck | `final-frontend-typecheck.log` | Exit 0 |

No source edits followed the final checks. No full backend test pass or runtime/platform acceptance is claimed. The red CLI boundary test is intentionally retained.

## Preservation and hold

The 113-file lifecycle/design manifest remains SHA256 `ffc37c82bafedcf7223478d3d4a6ff6aefa97dea9f04b615b63ef1df66d3e96b`. The 91-file protected manifest remains SHA256 `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358`. Their manifest bytes and every listed file match. The five-file prior guest package also matches.

CLI production file and registration are absent; `session.go` has no diff. The CLI regression remains unchanged at SHA256 `10f565d845ef1c6d2b6d1adbef740d44c94d430180113edad0f8bcf8d2fcf0ee`. Its production candidate remains outside the tree at `/tmp/pr-5769-public-api-79.xfBKWE/pc3-deferred.patch`, SHA256 `47017f5535c3cae86ea280840d4c0f317da166c7c85be35e4ae291401a623fa0`.

The two newly written guest drafts are absent and recoverably parked in `/tmp/pr-5769-pc2-f1-79.E8WKAc/guest-protocol-deferred.patch`, SHA256 `1f37f56f1057bda08cf627b35ab293dfd2ed7fe59ad336f47fd1fb1377ade27f`. Earlier accepted guest design work was preserved. No guest/lifecycle content belongs to the corrected PC2 slice.

Production control-service wiring remains unavailable. Native Windows has no attached runner and hosted execution is unauthorized. Guest containment, complete lifecycle recovery, CLI/UI, real desktop/provider and performance gates remain open.

Request: reviewer82 rechecks the exact corrected PC2 source and returns bounded CLEAR or specific findings. Stop here until that verdict. No publication.

Fun fact: exact JSON tag matching separates wire-format identity from case-insensitive text matching.
