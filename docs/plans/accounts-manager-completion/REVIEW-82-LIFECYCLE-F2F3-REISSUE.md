# F2/F3 read-only review routing through orchestrator59

Status: review reauthorized for the existing bounded package. This routing note supersedes the earlier withdrawal only. It does not supersede the later failing Windows matrix or change any runtime acceptance gate. Reviewer82 should return a bounded verdict or concrete findings through the orchestrator. No source edit, publication or new test execution is requested.

## Exact immutable input

Freeze root: `/tmp/pr-5769-life-f2f3-79.CYTx36`.
Worktree: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`, with the preserved working tree.

Seal: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-seal.json`, SHA256 `3583edc67c8fa9f31b59a37efcf4bde5c1c3f601abb3904b60ed388ea846a39c`.
Original frozen handoff: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/docs/plans/accounts-manager-completion/REVIEW-82-LIFECYCLE-F2F3.md`, SHA256 `b172934257c985065c4349ca377c037cfef3e683235413f613ff9bc6b4a48cab`.

| Manifest | Exact path | Files | SHA256 |
| --- | --- | ---: | --- |
| source | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-source.sha256` | 1821 | `5c2eaa27f21f9cde64323e626ef32a0a058e9199441853b9878d6bf9967be035` |
| correction | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-correction.sha256` | 12 | `45146f8077efe78c90f2a721428e92d916922cf8961007231f99312b485be812` |
| lifecycle | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-lifecycle.sha256` | 19 | `bb80947b72e5884e99c1866e96c686fe8af8e02b314e06c49da17510379ec1f9` |
| deletion-union | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-deletion-union.sha256` | 69 | `c23c10df21c9253126afe36a91c6f391b1454bc3415da58602fa212a8babb298` |
| protected | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-protected.sha256` | 91 | `462fd9b445fe2d6764f6ac0d8371aedb742aed22071bbb4afb2e6b943fca6358` |
| generated | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-generated.sha256` | 32 | `f059364a26a4b299aeb69689cc9190e7664aa7ce240aebcd9af3cf4e1915f37f` |
| r4 | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-r4.sha256` | 4 | `57e158ce52f9d536dd5362e21292fb9163d850a62392567b5f0e8c9cfa5ff9c5` |
| d2-delta | `/tmp/pr-5769-life-f2f3-79.CYTx36/final-d2-delta.sha256` | 3 | `c593f71b99f3e257f5ac00687bb26b5ec615499427d229b95d0761936d961e1c` |

Source archive: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-frozen-source.tar.gz`, SHA256 `4f18fc9eb588cde679e460100819746adf62d723473d9891af7bab70c9b8e67f`, 1821 source members.
Review archive: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-review-package.tar.gz`, SHA256 `dacb4b688ddf634f28778809c19d57e842d9b6fb84145c31ea9182388d62f16e`, 55 members.
Evidence manifest: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-evidence.sha256`, SHA256 `5c8d118dc286a1f013b282b830cc28995506361f6d5b1b9e900e71136400f11e`, 38 entries.

Fresh read-only audit: `/tmp/pr-5769-f2f3-routing-79.AVrtAH/integrity-recheck.json`. It rehashed every listed source and evidence entry, the seals, manifests, archives and original handoff. All matched. This is integrity verification, not a test rerun. The prior source/review archive contents retain their earlier per-member verification because the archive bytes are identical.

The correction is twelve files. The 19-file lifecycle context and 69-file deletion union are not the review delta. The current tree also has one later Windows contract test outside the 1821-file archive; it does not change any frozen file and is not part of this bounded correction. No review should silently substitute that current tree for the frozen archive.

## Twelve-file review scope

Per-file SHA256 values are in `final-correction.sha256` at the freeze root and repeated in the fresh audit.

1. `backend/internal/adapters/chatdriver/persistenthost/child_windows.go`
2. `backend/internal/adapters/chatdriver/persistenthost/host.go`
3. `backend/internal/adapters/chatdriver/persistenthost/host_race_test.go`
4. `backend/internal/adapters/chatdriver/persistenthost/provider_child_unix.go`
5. `backend/internal/adapters/chatdriver/persistenthost/provider_child_windows.go`
6. `backend/internal/adapters/chatdriver/persistenthost/provider_owner.go`
7. `backend/internal/adapters/chatdriver/persistenthost/provider_owner_duplicates_linux_test.go`
8. `backend/internal/adapters/chatdriver/persistenthost/provider_owner_evidence_test.go`
9. `backend/internal/adapters/chatdriver/persistenthost/provider_owner_json.go`
10. `backend/internal/adapters/chatdriver/persistenthost/provider_owner_other.go`
11. `backend/internal/adapters/chatdriver/persistenthost/provider_owner_windows.go`
12. `backend/internal/adapters/chatdriver/persistenthost/provider_owner_windows_test.go`

## Preserved failed-first logs

| Exact log | SHA256 |
| --- | --- |
| `/tmp/pr-5769-life-f2f3-79.CYTx36/pid-range-red.log` | `10351689bf9c0c4a4c22d933fbc4e2a553aef65a37914229712baf06bdb8678b` |
| `/tmp/pr-5769-lifecycle-79.tRGYkc/owner-duplicate-red.log` | `2932f838b8312cbaff3a64b0c3d6797cac6f687aa99c3b6edd249c68fc80b00e` |
| `/tmp/pr-5769-lifecycle-79.tRGYkc/owner-escaped-descendant-red.log` | `52fbc04de344dbeceeff971554ad8e241b6a2352f6dd3458e523dcba0cfbd4a6` |
| `/tmp/pr-5769-lifecycle-79.tRGYkc/owner-incomplete-red.log` | `fd1164733d7b5497aff038e7baee7d29208ea9b1a1f26af107725bcbbd61d578` |
| `/tmp/pr-5769-lifecycle-79.tRGYkc/owner-oversized-red.log` | `824dcb89b95edeb53f7dab2bea583d9d1bcdd41137c4addc8d08fdcc5f7ae1c7` |
| `/tmp/pr-5769-lifecycle-79.tRGYkc/windows-birth-red-wine-v2.log` | `ce684e6a79b58741cbf8213b06bbfba83fb26861de48ccda7bb48fc5ddc20aee` |

F2's valid failed-first case is the suspended orphan at the former create-before-assign cut. F3's valid failed-first case is duplicate fields authorizing retirement of a matching live owner. The PID-range log proves the numeric conversion guard was missing at the reader and both retirement entry points.

The incomplete-record log is supporting historical evidence. The original oversized log predates the complete-proof fixture; the final exact-limit positive and limit-plus-one negative controls are the relevant repaired assertions. `pid-range-red-wine.log` is not valid red evidence: an unrelated refusal made its negative-only check pass before correction. The retained same-owner positive control exposes that problem.

Preserved pre-fix archives and executable references are hashed in the evidence manifest: `/tmp/pr-5769-lifecycle-79.tRGYkc/windows-birth-red-source.tar.gz`, `/tmp/pr-5769-lifecycle-79.tRGYkc/persistenthost-windows-red.test.exe` and `/tmp/pr-5769-life-f2f3-79.CYTx36/pid-range-red-source.tar.gz`. Do not overwrite or rerun them as part of this read-only request.

The escaped-descendant log is a still-open failure, not a corrected F2/F3 case. Its test remains unchanged. The broader milestone stays HOLD.

## Frozen passing commands and retained failure

These are the command strings and outcomes recorded in `/tmp/pr-5769-life-f2f3-79.CYTx36/final-verification.json`. They are historical final-snapshot evidence, not commands rerun for this routing task.

Go working directory: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend`.
Recorded Go isolation prefix:

```text
env -u TMUX GOMAXPROCS=2 SHELL=/bin/sh TMUX_TMPDIR=/tmp/ao79-tmux.QMXnIq GOTMPDIR=/var/tmp/ao79.uF9PZF TMPDIR=/var/tmp/ao79.uF9PZF node /tmp/pr-5769-rebase.reOkRW/run-isolated.mjs
```

The recorded linter resolves to `/tmp/pr-5769-life-f2f3-79.CYTx36/golangci-lint-go1271`, built from v2.13.2 with Go1.27.1. Its patch is `/tmp/pr-5769-life-f2f3-79.CYTx36/candidate2-backend-lint.patch`, byte-equivalent to `final-backend-lint.patch`. Wine resolves the executable at the freeze root, uses version11.3 and prefix `/var/tmp/ao79-lifecycle-wine.qrH2XN`; the frozen record includes the isolated environment details. Compilation and Wine are not native Windows proof.

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-linux-host-race.log`. Frozen result: PASS; exit 0; 70.512 s.

```text
go test -mod=readonly -p=1 -race -count=3 -timeout=240s ./internal/adapters/chatdriver/persistenthost -run '^Test(ProviderOwnerEvidence|RemovalHost|ACPHost|HostReconnectsSameProviderAndReplaysDetachedOutput)' -v
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-linux-build.log`. Frozen result: PASS; exit 0.

```text
go build -mod=readonly -p=1 ./...
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-linux-vet.log`. Frozen result: PASS; exit 0.

```text
go vet -mod=readonly -p=1 ./...
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-windows-build.log`. Frozen result: PASS, cross-build only; exit 0.

```text
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -mod=readonly -p=1 ./...
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-windows-compile.log`. Frozen result: PASS, cross-compile only; exit 0.

```text
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -mod=readonly -p=1 -c ./internal/adapters/chatdriver/persistenthost -o /tmp/pr-5769-life-f2f3-79.CYTx36/persistenthost-windows-final.test.exe
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-windows-vet.log`. Frozen result: PASS; exit 0.

```text
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet -mod=readonly -p=1 ./internal/adapters/chatdriver/persistenthost
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-linux-lint.log`. Frozen result: 0 issues; exit 0.

```text
golangci-lint-go1271 run --timeout=3m --new-from-patch=candidate2-backend-lint.patch ./internal/adapters/chatdriver/persistenthost
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-windows-lint.log`. Frozen result: 0 issues; exit 0.

```text
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 golangci-lint-go1271 run --timeout=3m --new-from-patch=candidate2-backend-lint.patch ./internal/adapters/chatdriver/persistenthost
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-windows-birth-evidence-wine.log`. Frozen result: PASS, Wine compatibility only; exit 0.

```text
wine persistenthost-windows-final.test.exe -test.run='^TestProviderOwner(WindowsContainedAtBirth|WindowsCollisionPreservesOwner|Evidence)' -test.count=3 -test.timeout=90s -test.v
```

Log: `/tmp/pr-5769-life-f2f3-79.CYTx36/final-windows-recovery-hold-wine.log`. Frozen result: FAIL, native recovery HOLD; extended-limit query flags zero and same-owner query access denied; exit 1.

```text
wine persistenthost-windows-final.test.exe -test.run='^TestProviderOwnerWindows(PublicationCrashCuts|JobProof|RejectsWrappedPID)$' -test.count=3 -test.timeout=90s -test.v
```

Later full diagnostic failure, preserved and still relevant: `/tmp/pr-5769-windows-recovery-79.TPYVn9/windows-complete-matrix-red.log`, SHA256 `75db63eec52e8512fbc1b89baf5114fc5a2e23d22928eb758b47c5ec47821933`. Its recorded 35 leaves run three times, with 75 passes, 30 failures and no skips. The direct API diagnostic `/tmp/pr-5769-windows-recovery-79.TPYVn9/windows-kernel-contract-red.log`, SHA256 `2b3e2a4e871166cd1f7b11ff942fa946ea2729c54d05db7abfbbb3a6b173232a`, isolates the available runtime's job-query and access-right behavior. This explains an evidence constraint; it does not establish native correctness.

## Reviewer acceptance checklist

1. Verify input integrity before and after inspection: all twelve correction hashes, frozen source membership, original seal/handoff, 91 protected paths, 32 generated paths, four R4 files and three D2 correction files. Request findings against exact file/line/hash, not a mixed snapshot.
2. Review F2's creation-time containment and failure boundaries: job-list attribute at process creation; original process/thread handles through durable publication and resume; only intended stream handles inherited; global unique job identity; collision preservation; kill-on-close and no-breakaway checks; no create-then-assign or broad termination fallback. Trace every cleanup/error path without assuming compile success proves kernel behavior.
3. Review F3 at the reader and both retirement entry points: canonical semantic-key uniqueness, alias/escaped duplicate rejection, complete stopped proof, exact-size positive and one-byte-over negative controls, interrupted/trailing records and DWORD range checks. Confirm genuine positive controls and no weakening of live-workload assertions.
4. Check shared-host integration: platform-owned wait/stop and pipe cleanup, retained Unix behavior, explicit limits of Windows-excluded shell fixtures, original/native/replacement preservation. An ambiguous or denied probe cannot become success.
5. Return a bounded CLEAR only for substantiated F2/F3 correction properties, or exact HOLD findings. Native Windows recovery/publication/reboot/power-loss, macOS guest retirement, escaped descendants, remaining refresh/evidence coverage, full affected suites, public controls, real desktop/provider behavior and performance remain separate open gates. No native or product completion can be inferred from the bounded verdict.

Existing acceptance ledger: `docs/plans/accounts-manager-completion/LIFECYCLE-WINDOWS-EVIDENCE-GATES.md`. It remains unchanged: W1-W5 have bounded evidence, W6 native Windows is unmet, W7 independent review is pending (5 met, 2 unmet, 0 abandoned). Fresh integrity checks validate preservation, not a fresh execution of those historical check commands.

## Guest feasibility work already prepared

The requested design was written in the preceding turn and delivered directly to reviewer82 (send exit0). It is also included here for orchestrator routing, so it need not be restarted.

Primary design: `docs/plans/accounts-manager-completion/MACOS-GUEST-CONTAINMENT.md`, SHA256 `6a433b17831dd4f234bfbe3f5f361fbc06944f997177971bce864fb12a74bc6a`.
Review handoff: `docs/plans/accounts-manager-completion/REVIEW-82-MACOS-GUEST.md`.
Five-file manifest: `/tmp/pr-5769-macos-guest-design-79.7xIxWq/review-package.sha256`, SHA256 `bcb421310148ec0976f2fb406b82de8e93e2d61003cc1c1690852a8adfca2d3a`. Fresh recheck confirms all five entries unchanged.

The design covers per-launch Linux guest ownership, durable identity before guest start, all commands inside the guest, joined stop, tombstones, replacement preservation, the reviewer crash schedule, packaging/signing, arm64/x64, sharing and desktop compatibility. Its G4 same-boot VM-helper-death proof remains an explicit feasibility blocker. No native Mac target was accessed. Host-specific tool/editor compatibility and native Windows evidence remain open. Independent design review precedes any guest implementation; public controls remain held.

This routing adds only this note and external evidence metadata. It leaves every frozen source, manifest and design byte unchanged. No publication.

Fun fact: a durable retirement receipt must identify an execution lifetime, not merely the reusable session name.
