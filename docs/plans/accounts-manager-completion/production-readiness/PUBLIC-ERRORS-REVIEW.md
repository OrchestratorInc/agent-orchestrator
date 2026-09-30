# Public failure projection review

## Failed-first evidence

Evidence root: `/var/tmp/pr-5769-public-errors-79.BBRYzt`.

- `http-red.log`: SQLite close/reopen retains stored causes, but five readback routes per known-code case omit them. Fifteen assertions fail, plus two accepted-operation assertions. Private-code and ownership/request-ID controls pass.
- `cli-red-corrected.log`: twelve missing-code human/JSON assertions fail. The initial `cli-red.log` also rejected the CLI's normal telemetry POST; the corrected fixture explicitly separates that route from account mutations. Neither run changed production behavior.
- `client-renderer-red.log`: thirteen failures cover both missing renderer labels and retention of unknown or stale diagnostics by the client boundary.
- `typed-red.log`: exactly two TS2339 errors, the generated switch and removal response types lack `errorCode`.

## Midpoint self-review

First green: `backend-first-green.log` and `frontend-first-green.log`. No execution, admission, storage, lease or recovery files changed.

1. Durable reason versus request failure: successful HTTP readback can describe a failed operation. Keep the optional code inside the operation. Existing error envelopes and correlated request IDs remain unchanged.
2. Terminal suppression: `ready`, `complete` and `cancelled` suppress stale diagnostics at server and client boundaries. `failed` retains its cause. A code does not grant retry or cancel capability.
3. Redaction: exact 28-value allowlist, without whitespace/case normalization, bounds response text. Unknown values are omitted; renderer uses an own-property check so prototype names cannot pass. Empty or missing values preserve older-server behavior.
4. Compatibility drift: generated enum types require the renderer vocabulary to match. Added executable CLI/schema and HTTP/schema vocabulary checks. Added actual public-router CLI status/retry/cancel checks because a generic HTTP fixture alone is not evidence of projection compatibility.
5. Cache ownership: normalization returns new objects, including the nested session operation. Added frozen-response controls and mutation readback checks. The code does not mutate a retained network response or infer an account change from the diagnostic.

Test fixture cleanup was made nil-safe if SQLite reopening fails. Invalid operation-phase combinations were removed from the CLI table rather than counted as product schedules. These test-quality corrections do not alter the failed-first missing-code oracle.

## Verification and limits

All source checks below ran on the unchanged 25-file snapshot. SQLite reopen tests exercise real storage and HTTP projection; their retry adapter deliberately returns the durable record without launching a controller. Actual router tests use synthetic service results. Neither proves runner/vault reconstruction, live provider behavior or native platform retirement.

| Check | Result and log under the evidence root |
| --- | --- |
| Focused HTTP/CLI failure matrix, race count 3 | PASS, `focused-final.log` |
| Complete controller, CLI, API specification/generator and telemetry metadata package races | PASS, `affected-backend-race.log` |
| Same complete backend packages in the checkpoint-only detached checkout | PASS, `checkpoint-only-race.log`; 244.134 seconds including fresh checkout compilation |
| Backend `go build ./...` and `go vet ./...` | PASS, `backend-build.log`, `backend-vet.log` |
| Pinned changed-scope lint, including new files | PASS, zero issues, `changed-lint.log` |
| Nine affected frontend test files | PASS, 347 tests, `frontend-final.log` |
| Both actual switch-dialog test files in the checkpoint-only checkout | PASS, 36 tests, `compact-ui.log` |
| Frontend typecheck and renderer production build | PASS, `frontend-typecheck.log`, `renderer-build.log` |
| API regeneration and exact generated-byte comparison | PASS, `api-regenerate-final.log`; source manifest unchanged |
| Existing source/docs seals, containment and protected files | PASS, `preservation-final.log` |

No source changed during or after final verification. Existing test `act` warnings and renderer chunk/dynamic-import warnings remain in the logs. The full integrated backend suite was not rerun or claimed green; the known five retirement failures and other release gaps are unchanged.

## Desktop evidence

The desktop skill was used to launch a detached checkout with a real `npm ci`, production daemon/runner and real provider catalog. That checkout contains only published `b0378b674`, the four reviewed cold files and these 25 public files. The separate local inventory correction is excluded. Isolated home/data contain no real credentials. Initial packaging selected Go 1.26 outside the backend module; explicitly selecting the module's Go 1.27.1 toolchain made both daemon and runner builds pass. No source changed for this environment correction.

Evidence: `/var/tmp/pr-5769-errors-desktop-79.KrysKu`. `desktop-before-final.log` and `desktop-cancellation-final.log` pass. Real SQLite fixtures demonstrate stored diagnostic display before and after a complete app/daemon restart. The normal Switch agent popup reaches the updated compact control. A real cancellation returns 202 and readback confirms `cancelled`, no stale code, unchanged native selection and revision 1. Removal still reports `recovery_required`, `SOURCE_STOP_UNCONFIRMED` and no cancellation. Both final capture checks record zero page errors.

The failed `desktop-after.log` expected 200 at cancellation, although the route's documented status is 202. The successful request is in `launch-restart.log`; corrected continuation verifies its durable result without resetting records. Earlier capture failures were stale-menu/remembered-tab navigation errors. Logs are retained, not counted as product failures or successful checks. Native device account events return 503 with no account configured; no positive provider/usage assertion is made.

Three screenshots and a 2.04-second direct recording are staged under `docs/screenshots/pr-5769-public-errors/`. Images and representative recording frames were inspected. The existing compact retry/cancel row can overflow the narrow popup; it is documented, not changed by this projection slice.

## Immutable review request

Freeze root: `/var/tmp/pr-5769-public-errors-79.BBRYzt/seal`. Exact source manifest: `public-errors-v1-source.sha256`; archive: `public-errors-v1-source.tar.gz`. The source manifest matches `source-verification-start.sha256` byte-for-byte. Final docs, visual assets and evidence are sealed separately; `handoff.json` records their exact counts and SHA256 values without self-referential hashes.

Request reviewer82 to check red-first evidence, all 28 bounded codes, optional old-server behavior, successful/cancelled suppression, unchanged capabilities, real-router CLI output, SQLite restart projection and renderer cache immutability. Re-run focused tests against the archive as needed. Preserve all source bytes while review is pending. Independent review of this public slice remains required; cold-retry is already independently CLEAR and its original seal is unchanged.

The proposed checkpoint contains only cold-retry plus public diagnostics, with their docs and direct desktop evidence. The inventory correction and containment work remain separate. Local checkpoint commits may be prepared; push and PR updates remain blocked until independent review, an exact action announcement and specific approval. Release HOLD remains for containment, managed Chat production admission, settings-generation fencing, historical repair, complete runner/vault recovery, native platforms and live-account acceptance.
