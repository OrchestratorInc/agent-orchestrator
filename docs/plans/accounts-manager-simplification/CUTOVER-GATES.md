# Gates: published dependency cutover

Scope: use the published fork without changing runner behavior, remove only its archived local copy, and prepare the existing compact controls for publication. Baseline consumer commit: `43578e5cba1565bf08f1332656b2e0873bc1e125`. Dependency commit: `f8e08347b8f7667bfaf26de2e59081dc346fc644`.

Evidence directory: `/var/tmp/pr-5769-cutover-79.OVswfT`.

## Implementation order

1. Preserve existing source and archives. Add a failed-first packaging boundary with no local engine and negative identity/license checks.
2. Pin the published Go module, resolve notices from verified module metadata, inspect binary provenance, and update dependency CI. Review the initial working slice before removal.
3. Move the exact archived engine directory outside the checkout. Verify public-fetch, offline-cache packaging, module/source closure and all five target builds without that directory.
4. Run runner suites, affected backend and compact UI checks, full relevant suites, generated drift, lint and source-preservation checks. Record inherited failures separately, without suppressing assertions.
5. Review and commit the dependency cutover separately from the already-local compact controls. Recalculate net PR counts, prepare the exact publication action and request approval before pushing.

- [x] C1: pin and packaging checks reject missing/wrong dependencies and retain exact notices without an engine sibling.
  CHECK: node --test frontend/scripts/accounts-manager-dependency.node-test.mjs
  EXPECT: fail 0
  EVIDENCE: packaging-red.log fails before implementation; packaging-final.log passes all 13 cases. forge-green.log passes 29 cases, including missing notices. The Node test discovery regression was corrected using the established filename convention.
- [x] C2: actual public resolution, offline packaging, binary provenance and five-target builds work against the immutable dependency.
  EVIDENCE: public-fetch, package-offline, binary-provenance, binary-no-toolchain, dependencies-summary.json and all five cross-* records. Complete Linux Electron package passes in desktop-package-ci with CI toolchain selection; packaged-binary-provenance and packaged-binary-smoke also pass. Native platform acceptance is not inferred.
- [ ] C3: full runner ordinary/race checks and affected product verification establish the bounded cutover and compact-control behavior.
  EVIDENCE: Full runner ordinary/race/build/vet/tidy and five cross-builds pass on minimum and CI toolchains. Focused product races, 242 UI tests, typechecks/build, lint and generated drift pass. frontend-final exits 1 with 5663 passed, seven skipped and four unfinished due the unchanged native browser-import worker abort. Fork full race retains the media failure; its CI repeat also required interrupting a stalled unchanged upstream SDK test. The complete SDK package passes separately. Backend full race retains five retirement cases, two native-switch deadlines and a too-short local SQLite deadline. The native-switch controls pass three repeats; the complete SQLite package passes with CI's existing twenty-minute deadline in 690.548s. C3 is not a full-product pass.
- [ ] C4: removal is recoverable, protected files and prior seals are unchanged, final diff is reviewed and independently handed off.
  EVIDENCE: postpackage-preservation verifies the 1362 relocated files, 5582 unchanged entries and all seven prior preservation manifests. Exact ten-file code seal and self-review are recorded in CUTOVER-REVIEW.md. Independent review requested; verdict pending.
- [ ] C5: the explicitly approved publication matches the final commits, PR counts and verified remote head.
  EVIDENCE: pending; no push approval for a newly announced exact action has yet been received.

## Existing release holds

Five positive retirement failures, terminal wait-for-turn rejection, cold recovery/isolation gaps, native-platform acceptance and final integrated review remain open. Fork CI also reproduces the unchanged upstream streaming time-budget test failure. A dependency packaging change does not close these gates. The prior extraction archives remain immutable.
