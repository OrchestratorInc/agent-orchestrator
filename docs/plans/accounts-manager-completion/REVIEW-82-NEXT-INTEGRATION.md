# P0 integration review request

Review scope: normal main integration, six conflict resolutions and compatibility for databases created with the earlier account migration numbers. This is a bounded source review, not a release-completion request. Review the immutable archive because the live tree will continue with independently scoped switching corrections.

## Exact source

Freeze directory: `/var/tmp/pr-5769-next-79.wos4rh/integration-freeze`.

Published parent: `04e12ca3dc78ba96674a63c2039ac6b0b271b52f`.
Main parent: `e853b39601876d4be6d51c7f99dce0adcea5fa10`.
Source tree: `9902a253fb1ffbae3ab1492d3068f721b6da2973`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `integrated-source.sha256` | 5,671 files | `2c81afe6060d3ba7897f16c551777ef617d9d73fd938f2f2cb30f221b47fcb08` |
| `integration-delta.sha256` | 318 present files | `c8b3f6599abd5675e240fcdbac84636375fcf1088149ecdd2cfe5a04bf1bd9c2` |
| `integration-correction.sha256` | 20 files | `04c4f684b3a880dff83c97df1bdbfe3c913ae1c799067614490ce7a4106c7842` |
| `protected-integrated.sha256` | 91 files | `3438055a85087fe789dfb0aa5f2dc583ab52dac8a51bb683e0100647b096396f` |
| `integration-source.tar` | complete tracked source, submodule pointer recorded separately | `d86d167a5cdd93c3233cd3a5f9359a47946b60955ee894718f5064359f0e4919` |

`SUMMARY.json` records nine removed paths: eight byte-identical migration moves and one incoming UI deletion. The optional private submodule pointer is recorded, not claimed as tested or included source. Planning/review documents written after the seal are outside the archived code snapshot.

## What changed

- Kept both account controls and incoming automation dependencies in daemon/API construction. Preserved passive account-switch launches while accepting main's updated prompt preparation. Regenerated API artifacts from the merged source.
- Main owns migrations 161-164. The branch's eight account migrations now occupy 165-172 without SQL-content changes. Merged migration files were not edited.
- Added transactional, schema-backed migration-history repair before older compatibility repairs. It preserves selections, revisions, queued turns, switch owner/generation, empty-history decisions, removal acknowledgements and persistent host identity. Partial schema or absent history proof is rejected before history writes.
- Added eight historical-upgrade cases, six main-version controls, six incomplete-proof cases and four transaction-failure/reopen cuts. Updated versioned fixtures and pinned the incoming reversible migration test to its actual target version.

The correction manifest lists every reviewed path. All other delta files are incoming main changes or automatic integration and remain visible in the complete source/delta manifests.

## Evidence

All logs are under `/var/tmp/pr-5769-next-79.wos4rh`.

| Check | Result / log |
| --- | --- |
| Unchanged published readiness and escaped-child regressions | Expected assertion failures, `published-blockers-red.log` |
| Duplicate migration numbers before renumbering | Expected failure, `migration-number-collision-red.log` |
| Eight historical upgrades before repair | Eight failures; main controls pass, `migration-legacy-upgrade-red.log` |
| Final migration race matrix, three repetitions | Exit 0, `migration-race3-final.log` |
| Full SQLite and API specification packages | Exit 0, `schema-full-final.log` |
| Backend build and vet | Both exit 0, `backend-build-final.log`, `backend-vet-final.log` |
| Native restore/passive prompt and account readiness negative controls | Exit 0 under race, `prompt-readiness-contracts.log` |
| Full HTTP controller and API specification race packages | Exit 0, `http-contracts.log` |
| Repeated API and SQL generation | Exit 0, zero generated drift, `generated-drift-final.log` |
| Integration-only lint against the published parent | Exit 0, zero issues, `lint-integration.log` |
| Frontend typecheck, Node 24.21.0 | Exit 0, `integration-frontend-typecheck-final.log` |
| Protected paths, main migrations and moved migrations | Exit 0, `protected-integration-audit-final.json` |
| Untouched main with inherited shell | Runtime failure reproduced, `main-inherited-shell-control.log` |
| Main and branch with deterministic shell/test-owned sockets | Three race repetitions pass in both trees, `main-runtime-controls.log`, `branch-runtime-controls.log` |

Exact command arrays, exit codes and durations for the eight serial integration checks are in `integration-final-checks.log`. The original protected manifest remains byte-identical at `/tmp/pr-5769-public-controls-79.PbwFW0/protected.sha256`, digest `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358`. Of 91 protected files, 89 match that baseline and two match main exactly. No feature-authored protected delta exists.

## Review requests and open gates

Check history remapping at every legacy cutoff, rollback/restart atomicity, compatibility with existing repair helpers, preservation of irreversible removal/queue evidence, and both sides of the six merge conflicts. Confirm that the migration test adjustment retains its downgrade oracle.

The readiness fixture, intermittent cancellation conflict, escaped-descendant deletion, initial account selection, supported credential-kind/usage behavior, full lint cleanup, native platform tests, real A/B provider sessions and final desktop evidence remain open. Prior full-suite red logs remain valid historical evidence. Changed-scope lint is not a full lint pass. No shared shutdown code was changed. No commit has been published and no PR metadata was edited.
