# Usage lookup correction: review handoff

Date: 2026-09-28. Base HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`.

The reported browser-account usage lookup now succeeds in the isolated native desktop. The previous parser interpreted every top-level provider field as a quota window. Unrelated metadata therefore rejected an otherwise valid response. Parsing now reads only supported windows and continues rejecting malformed observations. Separate fixed error categories replace the generic operation error without exposing provider bodies, tokens, or private endpoints.

Request independent read-only review of this bounded correction. No independent CLEAR is claimed.

## Exact source snapshot

Root: `/tmp/pr-5769-usage-failure-79.Rucxda`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `source.sha256` | 23 source/generated/test/catalog files | `285ebe9570fcf958b042b3f52e5a87b31ccdfd428b96af74d51181a7d0e2f587` |
| `source.tar.gz` | Same 23 files | `4a2d40d212d863bd00611c812ce60c00dd36e139bd8977452ec0b6b5e6ceb1ee` |
| `correction.patch` | Delta from preserved pre-correction files | `9f064f8e260a7a7424d89888e03e365540249651dfc5b96d45f94b1a1c427a98` |

`source-files.txt` lists every path. `baseline-source.json` and `baseline/` preserve the prior bytes. The delta includes six runner files, five backend files, generated frontend types, three renderer/test files, and eight locale catalogs. It does not modify credential storage, lifecycle coordination, native switching, or Subscriptions. The two handoff/gate documents are sealed separately in `docs.sha256` and `docs.tar.gz`; `review-seal.json` records their final digests and the evidence manifest.

The earlier 48-file review package remains unchanged at `/tmp/pr-5769-credential-usage-79.AVs5wx`. Its source archive SHA256 remains `2a26fc1815ea9a816ccda5d6a1a1318fa0e3095e18e6105400e47e0409f1a2e7`; its review seal remains `3f3fad72c0bafbf42c0254c7dd631abc4f05dbd33861854b15f29de0562ad2ad`. Review this new delta without replacing or rewriting that package.

## Failed-first and midpoint evidence

- `categories-red.log`: seven runner category assertions fail before correction.
- `parser-red.log`: four valid-window responses with unrelated scalar, boolean, object, or array metadata fail before the typed parser.
- `http-categories-red.log`: five real management-client/service/controller assertions fail because the public code was generic; request-ID preservation is asserted.
- `ui-red.log`: five renderer cases fail before category-specific messages.
- `baseline.json`, `diagnostic.json`, `fixed.json`: the same account progresses from collapsed HTTP 424 to identified parse rejection, then HTTP 200 with two windows. These observations contain no credentials or provider bodies.

Midpoint review retained lifecycle conflict/not-found handling and the existing invalid-response sentinel. It added an explicit renderer code allowlist and prototype-name negative controls. Quota lookup cannot mutate account authorization, fallback selection, or credentials. The introduced typecheck error was corrected using the established message-key type; final checks below supersede that diagnostic run.

## Final verification

Commands ran against the frozen source. Go commands used the existing environment-sanitizing wrapper, `GOWORK=off`, a session-owned temporary directory, and two process workers.

| Working directory | Command | Evidence and observed result |
| --- | --- | --- |
| `accounts-manager/runner` | `go test -mod=readonly -race -p=1 ./... -count=3 -timeout=240s` | `runner-full-race.log`: pass |
| `backend` | `go test -mod=readonly -race -p=1 ./internal/accountsmanager ./internal/service/accountsmanager ./internal/httpd/controllers ./internal/httpd/apispec/... -count=3 -timeout=240s` | `backend-full-affected-race.log`: all five packages pass, including route/spec parity |
| Each Go module above | `go build -mod=readonly ./...` and `go vet -mod=readonly ./...` | `backend-build.log`, `backend-vet.log`, `runner-build.log`, `runner-vet.log`: pass |
| Each Go module above | Pinned local linter, `run --timeout=3m --concurrency=2 --new-from-patch=<module delta>` over changed packages | `backend-lint.log`, `runner-lint-final.log`: zero issues |
| `frontend` | `npm test -- --maxWorkers=2` | `frontend-full-final.log`: 341 files pass, 5,432 tests pass, seven skipped; no exclusions |
| `frontend` | `npm run typecheck` | `typecheck-final.log`: pass |
| `frontend` | `npx --no-install vite build --config vite.renderer.config.ts --outDir <evidence root>/renderer-build` | `renderer-build-final.log`: pass; existing chunk-size warnings |
| Repository root | `npm run api`, source manifest checked before and after | `api-drift-final.log`: pass, no generated drift |
| Repository root | `git diff --check`, protected/source/archive/runtime audit | `final-audit.json`: pass, 91 protected paths unchanged |

Focused three-repeat runner and HTTP results are in `categories-green.log`, `parser-green.log`, and `http-green.log`. `ui-green.log` records the initial focused pass; the final complete frontend suite includes the later allowlist controls. The complete frontend suite used the existing local zip utility directory on PATH for repository packaging fixtures. No fixture was skipped to obtain the pass.

## Real desktop evidence

The app runs from `/var/tmp/pr-5769-pc5-desktop-79.E7xDus/checkout`, with isolated data under that lab's `ao-home/pc5`. Both executables match the tested builds, and all 23 deployed source files match this snapshot. Existing accounts and routing choices remain unchanged.

`desktop-evidence.json` records the real native Electron page, two quota bars matching the public response, initial and refreshed HTTP 200, and unchanged account identities/selections. `usage-refreshed.png` shows both five-hour and weekly quota bars with the provider reset timestamp. `usage-refresh-with-hold.mp4` is a 4.84-second recording of the actual app interaction; `recording-checked-frame.png` was extracted and inspected. No request mocking or page reconstruction was used. The app remains open.

Keep screenshots and recordings local because the actual account label is visible. No evidence has been uploaded or published.

## Remaining scope and release gaps

- This verifies the reported browser-signed-in account. Setup-token classification/usage eligibility and other live-provider accounts are not proven by this correction.
- Independent review of this correction and the prior credential/usage package remains open.
- Full backend-wide tests were not rerun in this bounded correction. Previously documented production-readiness and HTTP shutdown fixture failures remain separate; no unrelated fixtures were changed here.
- Broader A/B switching, deletion/recovery, native Windows acceptance, and native macOS containment release gates remain open. This handoff makes no release-completion claim.
- Local frontend checks used Node 22.22.0; the CI-pinned Node 24 environment and full desktop packaging were not rerun for this renderer-only correction.
- No commit, push, PR edit, or publication. Protected native switching and Subscriptions remain unchanged.
