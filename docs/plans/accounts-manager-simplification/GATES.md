# Simplification planning gates

Scope: write an evidence-backed specification and implementation plan. This ledger certifies the documents only, not dependency extraction, cleanup or production readiness. Existing source and review snapshots remain unchanged.

- [x] D1: The specification uses measured base/head counts and identifies the actual dependency, patch, packaging and CI boundaries.
  EVIDENCE: Exact Git-object counts match PR metadata: 602,994 additions, 532,876 in engine including 292,955 Go-test lines, 70,118 arithmetic remainder. Reviewed runner replacement, UPSTREAM patch groups, desktop builder/license copy, package verifier and nested-module workflow. Independent recomputation in /var/tmp/pr-5769-simplification-plan-79.BVWEoD/audit.json; DOCS_AUDIT_OK.
- [x] D2: The specification preserves essential account guarantees and defines explicit refusal when exact teardown cannot be proven.
  EVIDENCE: Manual self-review of SPEC.md essential contract and containment decision table preserves encrypted credentials, explicit A/B isolation, queues, ownership, revocation and restart; missing proof cannot complete deletion or authorize an unsafe switch. Terminal SOURCE_NOT_QUIESCENT and native-platform gaps stay open.
- [x] D3: The plan gives ordered implementation slices, test ownership, negative controls, rollback, review checkpoints and publication prerequisites.
  EVIDENCE: Manual self-review of PLAN.md P0-P5 and E1-E10. Required callback, streaming and test-isolation patches are retained; dependency tests remain owned; fork publication is an explicit external prerequisite. Self-review added actual binary module-identity verification and clarified no new runtime service/download.
- [x] D4: Final self-review verifies document links and preserves all pre-existing repository file bytes; no extraction or release clearance is claimed.
  EVIDENCE: Read-only audit via Node under /usr/bin/fish at repository root exited 0, DOCS_AUDIT_OK: 5,589 pre-existing file/symlink hashes unchanged, working inventory and HEAD unchanged outside these documents, 10 relative links valid, git diff --check passes. Private gitlink directory was not traversed. New docs use allowed punctuation/wording. No product tests or dependency modifications were performed for prose-only work.

Implementation acceptance is tracked separately in PLAN.md and starts unverified. No product tests are required to certify prose-only changes.

Planning result: four met, zero unmet, zero abandoned. All ten implementation acceptance entries remain unverified. Evidence location above is local, not a published review link.
