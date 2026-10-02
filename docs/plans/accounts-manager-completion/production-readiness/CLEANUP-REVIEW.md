# Review: embedded-library cleanup

Baseline: `d23bdaee42640da7d7fe70fd0b9af52843a3ca61` plus the unchanged shutdown and retry-control seals. This review concerns cleanup only. Production acceptance remains open in [REMAINING.md](REMAINING.md).

## Scope

Remove 222 upstream standalone files containing 31,612 lines: examples, server/catalog commands, private terminal/command/storage packages and container/release tooling. Keep all production SDK dependencies, embedded data, relevant regression tests, local patches and licenses. Tidying removes 42 obsolete engine requirements and preserves the runner's existing requirement versions.

The remaining imported runtime source is unchanged. A five-target package/file/import comparison proves the runner's 110 upstream package records are identical before and after cleanup. Reference documentation is retained and UPSTREAM.md explains the reduced import policy. Do not reimport excluded files without checking whether the new version actually needs them.

The new top-level verification workflow covers the two nested Go modules, which existing Go jobs omit. It uses read-only repository permissions, build/vet/full-race checks and a separate five-target cross-build job. No hosted run or native execution is claimed.

## Recoverable removal

Evidence root: `/var/tmp/pr-5769-cleanup-79.Eur8Lb`.

- `removal-inventory.json`: explicit path, hash, byte/line count and reason for every deletion.
- `removed-source.tar.gz`: every removed file, read back and hash-verified. SHA256 `0c9be2a28348cd59df49ee57dc78ef3411fa11069c39b7eeb3732d9c5e891408`.
- `removal.sha256`: SHA256 `cae68ed5bfcb565ca52d335782097fe170870b507f17ba419d733d0a2ae8ef4a`.
- `starting-engine.sha256`: SHA256 `00612e287e52fbc6de3396cd8f424f09b586eedcdb6df99ad81acfbc76508a4f`.

An untouched HEAD control was extracted outside the repository and checked against the starting engine manifest. No working source or personal runtime data was overwritten to make that control.

## Verification and review questions

The full runner ordinary and race suites each pass 297 leaf cases without skips. The engine race run passes 9,947 leaves, skips 11 existing cases and fails the media-channel test at media_test.go:268. Its unchanged HEAD control fails all three repetitions at the same assertion. Preserve both logs. That issue remains a release-verification blocker and must not be hidden with a skip, timeout increase or a test deletion.

The complete ordinary engine baseline passes 10,041 leaves with eight existing skips. The post-cleanup ordinary suite passes 9,952 leaves with eight existing skips. Final counts are recomputed from JSON events in test-summary.json, excluding parent tests. Fixture/reference scans retain the configuration read by executor tests, embedded catalogs and source-scanning invariants. The deletion guard includes a known imported-file negative control on every target.

Both modules pass build/vet and tidy drift checks. Runner builds pass for Linux amd64/arm64, Windows amd64 and Mac amd64/arm64. The normal desktop runner packaging script passes; packaged license/provenance files match the source bytes.

The normalized Linux amd64 runner executable is byte-identical to the pre-cleanup HEAD build: SHA256 `cda4c59d6582f89b3285c13aaa5e432c243b284c8b142c4d0507b11fbcbafe06`. Both builds use Go 1.27.1, `CGO_ENABLED=0`, `-trimpath` and `-buildvcs=false`. This establishes equivalence for that build configuration, not every native-platform runtime.

Tests run with a stripped credential environment, Go 1.27.1, GOMAXPROCS=2 and serial package execution. Prior desktop results are not recertified by this cleanup.

Review exact exclusion boundaries, module-version changes, license packaging, hidden file-based fixture dependencies, native/protected preservation and the new workflow's trigger/command scope. The original three-file shutdown and 16-file retry-control requests remain separate and pending. Cleanup must not be mistaken for their independent clearance or a final full-feature verdict.

No visible behavior changes in this slice; new screenshots are not applicable. No commit, push, PR edit or evidence upload occurred. Final production review follows completion of the remaining implementation and native/live gates.

## Exact cleanup seal

Root: `/var/tmp/pr-5769-cleanup-79.Eur8Lb/cleanup-final`.

| Artifact | Count | SHA256 |
| --- | --- | --- |
| `source.sha256` | 6 retained cleanup files | `cc504bbb6980bfe17e677d9b3d71aa7c04c3bd9874618c91fdcd782322f6264a` |
| `source.tar.gz` | Same 6 files, read back and verified | `810e6b9c9ad5d8d9ad1294a3fd485dd503a7d1b27d896be50079aad981054d97` |
| `removal.sha256` | 222 deleted files | `cae68ed5bfcb565ca52d335782097fe170870b507f17ba419d733d0a2ae8ef4a` |
| `cleanup.patch` | 228 source/deletion paths; reverse-check passes | `b8af3bbfbc1487a9132de181edf9ac7e66767d54aeaef96690037c08550f8313` |

The six retained files are `.github/workflows/accounts-manager.yml`, `accounts-manager/UPSTREAM.md`, `accounts-manager/engine/AGENTS.md`, both engine module files and `accounts-manager/runner/go.sum`. The runner go.mod and all retained runtime source are unchanged. No existing directly required module version changed; tidying also makes two existing transitive test requirements explicit.

The final documentation archive covers CLEANUP-GATES.md, this handoff and REMAINING.md. Their hashes are in `docs.sha256`, not embedded here to avoid a self-referential digest. `manifest.sha256` binds the complete seal; `evidence.sha256` binds the external logs and audit scripts. No executable build artifact is included in the source archive.

Current worktree inventory is 258 changed/untracked paths: 27 preserved pre-cleanup files plus 231 cleanup paths (222 deleted, six retained and three documentation files). All 1,359 untouched retained engine files match the starting manifest. Verification also covers 91 protected files, five guest-design files, the 16-file retry-control source seal and the three-file shutdown seal.

## Final command evidence

Each log below is under the evidence root and has a matching `.command.json` and `.exit.json` with the exact invocation, working directory, status and elapsed time. Go package checks use `-mod=readonly` where applicable and serial `-p 1` execution.

| Check | Log | Result |
| --- | --- | --- |
| Pre-cleanup engine `go test -json -count=1 -timeout=15m ./...` | `engine-before.log` | Pass; 158.371s |
| Post-cleanup engine full ordinary suite | `engine-ordinary.log` | Pass; 98.630s |
| Runner full ordinary suite | `runner-ordinary.log` | Pass; 14.789s |
| Runner full race suite | `runner-race.log` | Pass; 149.110s |
| Engine full race suite | `engine-race.log` | Fails one inherited media assertion; 275.014s |
| Untouched HEAD media case, race, count 3 | `media-head-control.log` | Same assertion fails all three runs; 32.110s |
| Engine/runner build, vet and tidy drift | `{engine,runner}-{build,vet,tidy-check}.log` | All pass |
| Backend build/vet | `backend-{build,vet}.log` | Pass; 17.694s and 20.825s |
| Five runner target builds, CGO disabled | `cross-*.log` | All pass; compile only |
| Normal desktop packaging script | `package-runner.log` and `build-checks.log` | Pass; exact license/provenance retained |
| Trimmed, VCS-free Linux binary comparison | `binary-{before,after}.log` and `build-checks.log` | Byte-identical |
| Full runner lint v2.13.2, absolute unchanged backend config | `runner-lint-configured.log` | Pass; zero issues |
| Workflow actionlint v1.7.7 | `workflow-lint.log` | Pass |
| Five-target dependency graph rerun | `dependencies-final.log` | Identical to baseline |
| Workflow boundary, missing-file control and final check | `workflow-red.log`, `workflow-final.log` | Expected missing-file error before creation; final pass |
| Deletion archive, runtime files and all preservation manifests | `preservation-final.log` | Pass |

The first `runner-lint.log` used default rules because its command omitted `--config`. It reports 17 test-helper unchecked errors that the existing backend configuration deliberately excludes. The corrected full command uses `--path-mode=abs --config /home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend/.golangci.yml`. No new suppression or source edit was used to make it pass.

## Independent request and remaining gates

Review this bounded cleanup against the exact six-file source seal and 222-file removal manifest. Confirm that deleted standalone packages are outside every supported runner closure, file-based fixtures and licenses remain intact, module changes do not upgrade existing requirements, and CI coverage does not obscure the inherited engine race failure. Preserve the separate shutdown/retry reviews and do not infer their clearance from this request.

The final sequential gate rerun reports four met, two unmet and zero abandoned gates. `gates-final.log` records fresh passing C2/C3/C5 commands using `/usr/bin/fish` from the repository root. Its exit 1 correctly reflects the two open manual gates: C4 remains unmet on the inherited media race failure; C6 remains unmet until independent review. Current main has three merge conflicts, recorded in REMAINING.md, and no merge resolution is included here. The entire feature remains on production HOLD for the safety, isolation, native-platform and live-workflow gaps listed there.
