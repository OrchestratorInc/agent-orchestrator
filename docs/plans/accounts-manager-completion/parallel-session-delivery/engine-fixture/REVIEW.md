# Registration fixture review request

Review the two-file test-only correction. The fixture blocked the second concurrent worker and assumed it belonged to Account B. A diagnostic on unchanged HEAD proved Account A could be blocked instead. Its failed cleanup released the worker without joining it, allowing later model-list tests to see lingering registrations.

The correction observes individual completion, tests both enqueue orders, asserts the other account stays pending and joins test-owned work before global cleanup. Production code and scheduling are unchanged. See [midpoint review](MIDPOINT.md) for the synchronization argument.

## Exact snapshot

Root: `/var/tmp/pr-5769-integrated-79.6t2fcN/engine-fixture-final`.

- `source.sha256`: two files, SHA256 `edd38d083f443852e5db24a2c7857f487f636a0a0b4e27fe93a4d12fbfc83424`.
- `source.tar.gz`: SHA256 `c8e59eb9cf5189ad813832615edfa3136ecade900f9c43723621ef52a72d85f8`.
- `correction.patch`: SHA256 `d9069e1cc4f2305dd82370ce88e3d9036a05361383004976e753cb1d9c781cc3`.
- Source files: `accounts-manager/engine/sdk/cliproxy/service_auth_sync_test.go` and `accounts-manager/UPSTREAM.md`.
- `source-before.sha256` records the committed test and the preceding upstream-note snapshot. The patch excludes earlier authorized vendor corrections.
- The integrated 78-entry manifest is `/var/tmp/pr-5769-integrated-79.6t2fcN/integrated-corrected.sha256`, SHA256 `c290321c49cf56234c6edce50815e7118a78975383bfa6d0cce3308adb016b32`.
- `FREEZE.md` supplies the documentation/evidence manifests and verification result.

## Verification

Commands use Go 1.27.1, credential-stripped environments, `-mod=readonly`, package concurrency one, uncached execution and bounded timeouts. All logs are under `/var/tmp/pr-5769-integrated-79.6t2fcN`.

| Check | Result and log |
| --- | --- |
| Failed-first focused race, ten repeats | 18 passes, 12 failures; `engine-auth-focused-before.log` |
| Unchanged HEAD control, ten repeats | 15 passes, 15 failures; `engine-auth-head-control.log` |
| Assertion-only owner diagnostic | Three failures show A pending and B complete; `engine-auth-hook-diagnostic.log` |
| Corrected focused race, ten repeats | 40 passes, zero failures/skips; `engine-auth-focused-corrected.log` |
| External per-task-completion mutation | Both enqueue orders fail the intended assertion, with clean cleanup; `engine-completion-negative.log` |
| Complete affected package, race count three | 534 passes, zero failures/skips; `engine-sdk-corrected.log` |
| Complete engine build/vet | Pass; `engine-build-corrected.log`, `engine-vet-corrected.log` |
| Complete engine ordinary tests | 10,043 passes, eight declared skips; `engine-full-corrected.log` |
| Complete engine race tests | 10,038 passes, one media-relay failure, 11 declared skips; `engine-race-corrected.log` |
| Complete runner build/vet/ordinary/race | Pass; each test command has 297 passes, no failed/skipped cases; `runner-*-corrected.log` |
| Source and preservation checks | All pass; `engine-corrected-status.txt` and `preservation-midpoint.json` |

The media-relay failure remains in `TestPionMediaRelayBridgesAudioAndDataChannel`: its upstream data channel was not created. The same case fails all three race repetitions on the unchanged HEAD archive (`engine-media-head-control.log`). Neither its source nor deadline changed. No data-race warning appears in the corrected complete run. A slow-server close warning belongs to a separate fixture that completes after its existing ten-second handler bound and passes.

The standalone preservation helper initially contained a mistyped expected archive digest. The original freeze proved the correct digest; only the external helper was corrected. `preservation-helper-red.log` is retained. No archived bytes changed.

## Acceptance and limits

Confirm that completion is observed for the correct account without worker-order assumptions, the pending account remains fenced, cleanup joins before registry removal and the external mutation still fails. Confirm that the patch contains no production change or weakened assertion. Return a bounded independent verdict; self-review does not supply it.

This request does not clear the five positive retirement failures, unwired containment candidate, native platform gaps, managed Chat/profile work, live A/B/quota evidence or complete engine race failure. Earlier seals remain immutable. No publication is authorized.
