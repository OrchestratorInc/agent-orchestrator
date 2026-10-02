# Dependency extraction implementation gates

Scope: execute the reviewed simplification plan in bounded slices. Protect the existing compact controls, native switching, Subscriptions, credential/runtime behavior and prior evidence. No publication without the user's approval of the exact action.

- [x] P0: Complete upstream delta and five-platform dependency/asset baselines are preserved and reviewed.
  EVIDENCE: EXTRACTION-AUDIT.md and evidence-root upstream-delta.json, engine-retained.json, dependencies-summary.json. Exact public upstream tag confirmed; 1,351 unchanged, eight modified, three added, 222 established exclusions. Each of five target closures has 110 engine packages with selected-file hashes. Source archive digest d9a6ad163fe0bbcca5bd5971d54124ea3f41f4919b7ada5c90fa3572f42b0a83. Review found no unexplained runtime delta.
- [x] P1: Exact local dependency candidate preserves all retained engine bytes and required patch behavior with negative controls.
  EVIDENCE: Local candidate f8e08347b8f7667bfaf26de2e59081dc346fc644. CANDIDATE_VERIFIED checks 1,364 files, with only documented distribution metadata exceptions. Required callback/delimiter/registration races pass count 3. Restoring stock callback interface or translator fails as expected; missing license and catalog controls fail. Full ordinary: 9,952 passed, eight skipped. Full race: 9,947 passed, 11 skipped, one inherited media failure, retained as a release HOLD. Build, vet and tidy drift pass. Independent candidate review is still pending; this gate records local equivalence, not remote publication approval.
- [ ] PUBLISH: Independently reviewed immutable dependency is publicly fetchable from an approved destination.
  EVIDENCE: pending; explicit user approval requested for public accounts-proxy fork under the project organization and exact candidate commit only. Read-only midpoint review requested through the orchestrator. No fork creation, push or PR modification performed.
- [ ] P2: Normal build and packaging resolve the exact dependency and verify binary identity/notices without a local engine tree.
  EVIDENCE: pending
- [ ] P3: Recoverable engine removal preserves behavior and passes exact-snapshot checks and independent review.
  EVIDENCE: pending
- [ ] P4: Any further pruning has caller/platform/state evidence; unsupported teardown remains explicitly blocked.
  EVIDENCE: pending
- [ ] P5: Essential workflows, including terminal wait-for-turn, pass final integrated and supported-platform acceptance.
  EVIDENCE: pending; existing product failures and native-platform gaps are not waived

Evidence root for this run: `/var/tmp/pr-5769-extraction-79.4UyUrH`. The prior documentation-only gates do not certify these implementation outcomes.

Local candidate handoff: [REVIEW-EXTRACTION-P1.md](REVIEW-EXTRACTION-P1.md). Source manifest: 1,364 files, SHA256 `5b0e4e5334ad2f9633e5588f631bebea2c84c0add50d40bce8d07d21339ee5db`. Evidence manifest: 97 files, SHA256 `1ba9a08b5639b7d2db5661ef135a983dd78f848639e09ee3ad0e87b65c6c55f6`. All five candidate cross-builds and workflow lint passed after the recorded suites. Seven prior preservation manifests and the complete consumer baseline passed. This records two locally met gates and five unmet gates, with no abandonment. No reduction of the current PR diff has occurred yet.
