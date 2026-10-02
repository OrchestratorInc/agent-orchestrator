# PC5 production wiring: midpoint review request

Request: reviewer82, review this four-file production-wiring slice read-only before actual runtime or desktop evidence begins. This is a midpoint request, not product or release clearance. Delivery: `am-root-pc5-implement-20260928`. No publication.

## Exact source

Artifact root: `/tmp/pr-5769-pc5-79.0k7pqi`.

`review-freeze.json` indexes the final six-file source-plus-docs slice (`pc5-wiring-slice.sha256` and `.tar.gz`), full integrated source inventory (`integrated-source.sha256`), evidence manifest/archive (`pc5-wiring-evidence.sha256` and `.tar.gz`), verification and preservation receipts. Its recorded digests include this handoff and `PC5-GATES.md` without a circular self-hash. Source and documentation edits stop at that seal.

| Artifact | SHA256 |
| --- | --- |
| `pc5-wiring-source.sha256`, four files | `63312b8885fadfdf4129c4ff1bf9031160965e06741269849e3fbc5d5618b82f` |
| `pc5-wiring-source.tar.gz`, those same four files | `152fd2cec1078e381ef293fa2b5f0c780ded5917cabdcd270d8bef4a23e6a167` |
| `pc5-wiring.patch`, exact pre-PC5 delta | `3cd3352458ca9865f1a2bff8e92fbe3ed9e14b1e3bfcb2638363154493e1c6d8` |
| `daemon-before.tar.gz`, original daemon source | `0fefceefdd6d172b88905f4ecde671b57cc3705ce6307a0de13c051eced0a97d` |

Files:

```text
backend/internal/daemon/accounts_manager_controls_test.go
backend/internal/daemon/daemon.go
backend/internal/service/session/accounts_manager_controls.go
backend/internal/service/session/accounts_manager_controls_test.go
```

The daemon delta is one dependency assignment, `AccountsManagerControls: sessionSvc`, plus gofmt alignment within that literal. The new session adapter delegates mutations to the existing coordinated manager and reads existing durable records. It does not own runtime teardown, queues, credentials, recovery or route authorization. HTTP contracts and controller DTOs are unchanged.

The daemon file is the explicitly authorized exception to the old 113-file lifecycle freeze. Its original SHA256 is `311b614fc2c8de722c60f504ffd08ea01d2dfdc3a88789b45d3dfb4a7fd2c9a7`; current SHA256 is `267b3dd8b3ba6161e674ec6e43de5cd3355c9abb9ebc711641235775555491a6`. It is not one of the 91 protected paths. Earlier manifests and archives are not rewritten.

## Failed-first evidence and self-review

1. `production-red.log`: the actual daemon dependency AST lacked the control service. Public-router requests constructed from real `startSession` returned 501 instead of 200 for a committed read and 202 for a switch. All three assertions failed before the production edits.
2. `production-first-green.log`: reads passed, but a bodyless cancellation fixture supplied a non-nil empty reader and received 400. Only the new test helper changed to `http.NoBody`, matching a bodyless wire request. The reviewed controller decoder remains unchanged. `production-green.log` then passed all three initial tests.
3. Midpoint self-review found a mixed projection: the binding read could precede a commit while the subsequent operation read returned ready. `projection-failed-first.log` records the deterministic failure. The adapter now rechecks committed binding identity, revision, mode, selected account and blocked state after reading the operation and returns a conflict on change. The regression passes under race detection three times.
4. The first projection command in `projection-red.log` had a test constant typo and did not compile. It is retained as a diagnostic, not counted as failed-first behavior. The subsequent assertion log above is the valid reproduction.
5. The final pass checked provider mapping, missing and foreign ownership, safe errors, no read-side selection or binding creation, exact revision zero for removals, and delegation of only explicit user choices. No further defect was found in this bounded adapter. No claim is made that a focused race pass proves every coordinator interleaving.

## What these tests establish

The daemon tests use actual `startSession`, the concrete session service, manager, account service, SQLite store and public router. Only outbound runtime and credential-catalog ports are synthetic. The separate AST check prevents a manually constructed test router from hiding missing daemon wiring.

- Committed native/managed reads preserve provider, account and revision without inventing pending success.
- A public switch request creates the real durable journal and fences input. Duplicate requests are idempotent. Repeated pre-stop cancellation releases the fence, leaves the source and committed binding unchanged, and rejects retry of the cancelled operation.
- In-use removal previews only the matched account, rejects missing confirmation and a stale revision without a journal, stops the matched synthetic runtime through the real coordinator, removes the matched synthetic credential once, and preserves the unrelated B/native bindings and runtimes. Completed retries/cancellation conflict; repeated original acknowledgement does not delete again.
- The adapter preserves explicit managed/native mode and drain/interrupt policy, operation/session ownership, confirmation and removal revision zero. Unsupported capabilities are explicit. A missing persisted binding is a not-found response, not an implicit native/default choice.
- An unavailable catalog returns 503 with the request ID, creates no operation or input fence, and emits a templated telemetry route without account/session/operation identifiers, runtime handles, private endpoints or the injected secret marker. Public error and success responses are checked for private execution details.

These are service/SQLite integration checks, not real process, provider, authorization-lease, cold-daemon recovery or Electron acceptance. A successful managed target launch and switch back to native remain next-slice work.

## Verification

All Go commands use backend CWD, Go 1.27.1 on Linux amd64, Fish, `TMPDIR=/var/tmp/ao79.uF9PZF`, `GOMAXPROCS=2`, and `node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs`. The wrapper strips inherited account/session and credential environment variables. No source edit follows the final focused command.

| Command after the wrapper | Observed result | Log under artifact root |
| --- | --- | --- |
| `go test -mod=readonly -race -p=1 ./internal/daemon ./internal/service/session -run AccountsManagerControl -count=3 -timeout=180s -v` | Exit 0; daemon 15.254s, service 1.022s | `final-focused-race.log` |
| `go test -mod=readonly -race -p=1 ./internal/daemon ./internal/service/session -count=1 -timeout=5m -v` | Exit 0; daemon 17.912s, service 16.762s | `package-race.log` |
| `go build -mod=readonly -p=1 ./...` | Exit 0, no diagnostics | `build.log` |
| `go vet -mod=readonly -p=1 ./...` | Exit 0, no diagnostics | `vet.log` |
| `go test -mod=readonly -race -p=1 ./internal/httpd/controllers ./internal/httpd/apispec/... -count=1 -timeout=3m -v` | Exit 0; full controller package, route/spec parity and fresh embedded-YAML drift checks | `controller-contract-race.log` |
| `/tmp/pr-5769-life-f2f3-79.CYTx36/golangci-lint-go1271 run --timeout=3m --concurrency=2 --new-from-patch=/tmp/pr-5769-pc5-79.0k7pqi/pc5-wiring.patch ./internal/daemon ./internal/service/session` | Exit 0, 0 issues | `lint-initial.log` |

Generated TypeScript was regenerated in memory with installed `openapi-typescript` 7.4.4 and compared byte-for-byte. `generated-drift.log` records equality: schema SHA256 `86e7b7f807ea28ae480936e6acfe0a00116840050f4be0f78464074b9d266f8d`; OpenAPI SHA256 `6410427cb3cc71ea1eeaea7d0e5d38e4b057dff9c9780cf843e196601c8a86f2`. No generated file was written. `git diff --check` exits 0 (`diff-check.log`).

## Preserved shutdown diagnosis

The immutable diagnosis remains at `/tmp/pr-5769-pc5-readonly-79.6uQkQt/DIAGNOSIS.md`. Its 21-file evidence manifest SHA256 is `fe20236ca4bd672299260077234530047f5838ce1ec0f99ccddce0e5817dda21`.

The current branch, untouched main and sealed PC4 checkout reproduced the five-second assertion respectively 5/20, 7/20 and 3/20 times. The installed transport starts a redundant connection while draining the health response. The server accepts it without a request and waits in `net/http.(*conn).readRequest`; the new-connection grace rule exceeds the fixture shutdown deadline. Server, router, test fixture and module files match main. No account service or worker is needed to reproduce it.

The latest delivery supersedes the diagnostic document's proposed fixture correction: no server or fixture edit is authorized merely to green this branch. Keep that inherited root-HTTP failure visible in later full-suite reporting. A separate fixture-only follow-up can use a test-owned transport and fully drained responses if separately scoped.

## Preservation and remaining gates

The preservation audit compares the full prior source inventory and exact manifests. PC2 13/13, PC3 13/13, PC4 F1 22/22, protected 91/91 and guest design 5/5 are unchanged. Lifecycle 112/113 are unchanged; only the documented daemon assignment differs. All four PC4 F1 source/correction/slice/evidence archives still match their sealed hashes. The complete prior diagnostic evidence remains unchanged.

`preservation-final.json` records one modified pre-PC5 source, five new owned files and zero removed paths. The final inventory has 5,566 regular files and one preserved gitlink. Ledger status is four met gates, three open gates, zero abandoned. The remaining gates are not completion claims or waived requirements.

Midpoint review must evaluate the adapter, production dependency, read consistency, exact ownership and boundary test fidelity. Stop source edits while that review runs. Broader PC5 gates remain open: actual A/B routing and managed/native target launch; public retry/cancel and removal recovery across process/SQLite restart; queue and lease revocation; missing-binding compatibility; full affected runner/backend/frontend verification; real isolated Electron flows and screenshots/recording; performance; native Windows evidence; native arm64/x64 guest retirement; authenticated provider evidence. Existing platform and guest holds are not waived.

No new frontend, CLI, runner, native switching, Subscriptions, lifecycle or guest implementation is included. No commit, push, PR edit or publication occurred.
