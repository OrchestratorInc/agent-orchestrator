# W1 runner correction: independent review handoff

Date: 2026-09-29. Base and current HEAD: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. Local-only. No commit, push, PR edit or publication. Follow [W1-RUNNER.md](W1-RUNNER.md) under [PLAN.md](PLAN.md).

## Exact snapshot

Evidence root: `/var/tmp/pr-5769-parallel-79.7QugRY`.

- Four-file source/metadata manifest: `final/source.sha256`, SHA256 `558bfebcbf946aad1c0e48f73cb29491a6389a19c1e2106a725706de3314bd52`.
- Four-file source archive: `final/source.tar.gz`, SHA256 `bf00e9df9c2193ae84d333112380202d58046229cb078b2b66632c8dc677ca22`.
- Exact relative inventory: `final/source.list`. Archive and live file hashes must match this manifest before review.
- The final documentation/evidence index is separate from the source archive to avoid self-referential hashes. It includes this handoff and the three plan/ledger documents.

Files:

1. `accounts-manager/engine/internal/translator/codex/openai/responses/codex_openai-responses_response.go`: retain the delimiter after a data line using per-response state. Leading empty lines stay empty. No routing, credential or retry policy change.
2. `accounts-manager/engine/internal/translator/codex/openai/responses/stream_delimiter_test.go`: data-only/named-event delimiters, leading/repeated empty lines and independent response state.
3. `accounts-manager/runner/internal/runner/parallel_sessions_test.go`: real subprocess runner, verified synthetic credentials, concurrent upstream/client barriers, shared client hints, exact selected identities/counts, independent mutations and restart, wrong-identity control and partial-response no replay.
4. `accounts-manager/UPSTREAM.md`: document the narrow local library correction and its reapplication boundary.

## Failed-first evidence and root cause

The original upstream-overlap characterization passed without production changes in `runner-first.log`. Strengthening the test to require client delivery before releasing the upstream exposed an actual framing defect. The broad diagnostic log `runner-midpoint-race3.log` also contains a separate fixture cleanup-order error. It is not clean failed-first production evidence.

After correcting only fixture cleanup ordering, `runner-stream-diagnosis.log` failed at `request did not reach client streaming barrier`, exit 1. The upstream had received the exact account credential and flushed the event, but the client could not observe it until another event arrived. This is the valid production red log.

`stream-delimiter-red.log` independently fails both data-only and named-event cases with `event delimiter = [""], want one complete delimiter`, exit 1. Its leading-empty control passes. The executor scans lines without line endings; the translator returned an empty payload for the terminating blank line. Existing stream layers drop empty payloads, so the response framer lost the event boundary.

The correction returns a nonempty delimiter only after a data line, with state owned by that response. It does not force an incomplete JSON fragment to become a frame or commit a leading empty event. The existing framer still validates and reconstructs frames. No timeout was increased, barrier removed or provider fixture changed to hide the failure.

## Midpoint self-review

- Added a client-delivery barrier: merely observing simultaneous upstream requests was insufficient for responsiveness.
- Added shared-account concurrency, wrong-identity and truncated-response controls. Exact provider request counts reject hidden retry/fallback.
- Registered temporary state before cleanup that scans it. Joined cancellation-aware client workers before runner/log/vault cleanup.
- Scanned every issued route capability, including rebind tokens, rather than only the two initially stored tokens.
- Added a fresh B request during every A-only mutation while B's first response stays open. Restart could otherwise hide a temporary admission regression.
- Kept native bindings, lifecycle ownership, persistent-host retirement and desktop files unchanged. The test's private credential removal is not evidence for coordinated in-use account deletion.

The last source edit was the fresh-B assertion. Only `runner-final2-*` logs are final runner evidence. Earlier runner green logs remain preserved but are superseded. Engine source/tests did not change during that last runner-only edit, and their final recorded hashes match the same manifest.

## Final verification

Go `1.27.1 linux/amd64`, Node `v24.21.0`, golangci-lint module pinned to `v2.13.2`. Fish launches use `GOWORK=off`, `TMPDIR=/var/tmp/pr-5769-next-79.wos4rh/tmp`, `SHELL=/bin/sh`, `GIN_MODE=release` for tests, and `/tmp/pr-5769-rebase.reOkRW/run-isolated.mjs`. The wrapper strips provider/session credentials and sets `GOMAXPROCS=2`. All provider endpoints in the new tests are test-owned loopback servers using synthetic keys.

Commands below run from `accounts-manager/runner` unless marked engine. Logs are relative to the evidence root.

| Command | Observed result | Log |
| --- | --- | --- |
| `go test -v -mod=readonly -race -count=3 -timeout=180s ./internal/runner -run TestRunnerParallelSessions` | PASS, 9 scenarios per repetition, 27 executions, no skips, 10.486s | `runner-final2-focused.log` |
| `go test -mod=readonly -count=1 -timeout=300s ./...` | PASS, complete runner, 7.189s | `runner-final2-test.log` |
| `go test -mod=readonly -race -p=1 -count=1 -timeout=300s ./...` | PASS, complete runner, 11.028s | `runner-final2-race.log` |
| `go build -mod=readonly ./...` | exit 0 | `runner-final2-build.log` |
| `go vet -mod=readonly ./...` | exit 0 | `runner-final2-vet.log` |
| `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --timeout=5m --path-mode=abs --config ../../backend/.golangci.yml --output.json.path <evidence>/runner-final2-lint.json ./...` | exit 0, zero issues | `runner-final2-lint.log`, JSON companion |
| Engine: `go test -v -mod=readonly -race -p=1 -count=3 -timeout=180s ./internal/translator/codex/openai/responses ./sdk/api/handlers/openai` | PASS, 852 top-level passes; 3 allocation tests intentionally skip per race repetition | `engine-translator-handlers-race3.log` |
| Engine: `go test -v -mod=readonly -p=1 -count=1 -timeout=180s ./internal/translator/codex/openai/responses ./sdk/api/handlers/openai` | PASS, 287 top-level passes, including all allocation tests, no skips | `engine-translator-handlers-test.log` |
| Engine: `go test -v -mod=readonly -race -p=1 -count=1 -timeout=180s ./internal/runtime/executor -run TestCodex` | PASS, 182 top-level passes, no skips; selected executor tests, not the full package | `engine-codex-executors-race.log` |
| Engine: `go build -mod=readonly ./...` and `go vet -mod=readonly ./...` | both exit 0 | `engine-build.log`, `engine-vet.log` |
| Engine: same pinned lint/config on `./internal/translator/codex/openai/responses` | exit 1, three unchanged-file findings, zero changed-file findings | `engine-translator-lint.log`, JSON companion, `final/lint-classification.json` |
| Preserved manifests and `git diff --check` | all exit 0 | `final/source.log`, `retirement.log`, `protected.log`, `generated.log`, `guest.log` |

The engine lint findings are two `revive` findings and one `wastedassign` finding in unchanged `init.go` and `codex_openai-responses_request.go`. `git diff --exit-code HEAD -- <both files>` passes in `final/lint-control.log`. No engine-wide lint pass is claimed; no unrelated upstream file was edited to green this audit.

Preservation: nine retirement correction entries, 91 protected native/Subscriptions paths, 33 generated entries and five guest-package entries match their recorded manifests. No API or SQL source/contract changed, and generators were not rerun for this correction. Existing archives remain unchanged.

## Requested independent review and remaining gates

Request: review the exact four-file snapshot read-only. Verify the delimiter state cannot cross requests or commit empty streams; check named/data-only framing compatibility, overlapping identity assertions, mutation/restart admission, partial-response no replay, secret-safe evidence and failure cleanup. Confirm a bounded verdict before closing W1E. Do not read inherited green milestones as acceptance for the integrated branch.

Full W1 is still open: no real terminal-controller launch or installed provider CLI was added to this runner matrix. Managed Codex Chat, isolated native profiles, migration/reconnect, real paired accounts/positive usage and full desktop workflows remain open. The three positive persistent-host recovery groups remain failing under the preserved guard. Linux containment, native Windows and both Mac architectures require their own implementation/acceptance. Native compilation is not execution evidence.

This backend streaming correction has no changed visual control or layout, so no screenshot is applicable to this bounded slice. Real desktop evidence remains mandatory for the broader PR. No release, merge-readiness or complete-test-coverage claim is made.
