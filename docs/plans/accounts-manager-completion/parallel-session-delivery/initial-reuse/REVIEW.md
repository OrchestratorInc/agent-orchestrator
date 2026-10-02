# Existing-session account reuse review

Read-only independent review requested. Base HEAD: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. All changes are local. This bounded W1 correction does not adopt containment or enable a new execution strategy.

Evidence root: `/var/tmp/pr-5769-initial-reuse-79.9BArne`.

## Correction

Generic orchestrator spawn and same-automation-run replay previously returned an existing session before checking the explicit account choice. The caller could select B or native mode and receive success for an existing A session. The store's initial-selection replay check was never reached.

Both return paths now call a service-local read-only guard. Omitted-choice behavior is unchanged. Explicit reuse requires a valid choice, matching supported harness/provider, exact durable session/account/mode, positive binding revision and no removal fence. The latest switch journal must belong to this session/provider and be terminal. A second binding read rejects a commit that changed the first snapshot. Missing read capability, unresolved switching, read errors and request cancellation cannot become success. Nothing switches, launches or changes a binding to satisfy the retry.

Four source files:

- `backend/internal/service/session/service.go`, two existing return sites.
- `backend/internal/service/session/accounts_manager_reuse.go`, new read-only guard.
- `backend/internal/service/session/accounts_manager_reuse_test.go`, new service regressions.
- `backend/internal/httpd/controllers/sessions_account_reuse_test.go`, real public router/service/SQLite coverage.

Source manifest: `final/source.sha256`, SHA256 `a3c0651a22f44dff6cc905a8203e8e22e6d2db5dde921281fcc902ecc0437e27`.

Source archive: `final/source.tar.gz`, SHA256 `644bb3496f861c59d0d1eae34160c6b0ab2f14a015b8b5131589f6c99c4daa73`.

## Failed-first and midpoint review

- `reuse-red.log`: 26 mismatched/unknown/invalid choice cases fail across interactive and automation reuse, while six matching/native/omitted controls pass. The original production source and first regression are preserved in `pre-fix-source.tar.gz`, SHA256 `a709929fc062d91abea3361275f8d645bd6bd05ac40fb3473f5444cd542574ab`.
- `reuse-green.log`: the first correction passes three times under race.
- Self-review found that removal's blocked flag is not switch-journal state. `midpoint-red.log` proves eleven pending/foreign/mixed/cancelled snapshot failures, with three terminal controls passing. The guard now checks the journal and rereads the binding.
- `focused-green.log` and `switch-public.log` establish the midpoint correction and a real persisted pending-switch case. The final focused runs include both supported provider mappings and run after the last source edit.
- Final self-review confirms no changes to fresh worker creation, omitted-choice orchestrator compatibility, dedicated orchestrator replacement, controller ownership, native switching, Subscriptions, routes, DTOs or generated schemas. A returned session is an observed existing resource, not a new launch or an account switch.

## Verification on the final four-file snapshot

Use the exact commands in `GATES.md` and external `verify.fish`. All use the existing credential-stripping wrapper, Go 1.27.1, a session-owned temporary directory and Fish.

| Check | Evidence | Result |
| --- | --- | --- |
| Both service regression tables, race count 3 | `final-gates.log` | 144 leaf executions, 1.033s. |
| Real HTTP/service/SQLite table, race count 3 | `final-gates.log` | 21 case executions, each with two requests, 17.664s. |
| Complete session-service race count 3 | `service-race.log` | 750 top-level executions, 1599 leaf executions, 26.009s. |
| Complete controller race count 3 | `controllers-race.log` | 1344 top-level executions, 2190 leaf executions, 171.879s. |
| Backend build and vet | `backend-build.log`, `backend-vet.log` | Exit 0. |
| Pinned v2.13.2 changed-scope lint | `changed-lint.log` | Zero issues. |
| HTTP API spec/parity tests | `api-parity.log` | Exit 0. |
| API and SQL regeneration | `api-generation.log`, `sql-generation.log`, `generated.log` | Exit 0; all 33 generated-file hashes unchanged. |
| Protected/previous/guest preservation | `protected.log`, `integrated.log`, `guest.log` | 91 protected, 51 prior integrated and five guest hashes unchanged. |
| Source before/after and diff check | `source-before.log`, `source-after.log`, `diff-check.log` | Tested bytes unchanged; clean diff check. |

No skips or failures in either complete affected package. The gate checker exits 1 solely because independent review remains unmet; all three runnable gates pass. This is not full backend, frontend, native-platform or live-provider acceptance. No visual surface changed, so screenshots do not apply to this backend-only guard.

## Review request and remaining work

Please verify both early-return paths, absent-choice compatibility, matching native/managed replay, journal ownership/phase, mixed-read rejection, public error/request-ID preservation, and zero runtime mutation. The source archive is independent of later worktree changes. `final/FREEZE.md` records the documentation and integrated manifests.

The runner removal correction/process proof and Linux containment candidate retain their own pending independent reviews. Production containment, three positive retirement groups, managed Chat/profile execution, native Mac/Windows runs, actual-account workflows, desktop/performance acceptance and full final-tree verification remain open. Nothing in this correction clears those gates or authorizes publication.
