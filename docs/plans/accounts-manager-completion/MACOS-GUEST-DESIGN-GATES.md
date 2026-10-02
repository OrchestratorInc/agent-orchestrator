# Gates: per-launch macOS guest containment design

OWNS: docs/plans/accounts-manager-completion/MACOS-GUEST*, docs/plans/accounts-manager-completion/REVIEW-82-MACOS-GUEST*

Scope: design and feasibility only. Preserve runtime source, the escaped-descendant regression, all protected paths and the bounded F2/F3 artifacts. No implementation or publication before independent design review.

Checked items below establish document coverage and preservation only. They do not certify a working Mac guest backend. Native helper-death proof, compatibility and the overall lifecycle/product gates remain HOLD.

Plan: inspect existing VM, snapshot, provider and desktop boundaries; verify platform contracts against primary sources; write the lifecycle and isolation design with concrete reuse/replacement decisions; adversarially review crash, recovery and compatibility schedules; route the exact document to reviewer82.

- [x] GD1: repository reuse claims and current gaps are supported by exact source locations
  EVIDENCE: MACOS-GUEST-CONTAINMENT.md, existing-code table. Inspected the three concrete cloud adapters, reconciler, checkpoint writer, local ownership/deletion boundaries and desktop packaging. No local Mac VM implementation found in those paths.

- [x] GD2: the design specifies durable pre-start identity, complete guest execution, exact joined stop, tombstones and replacement preservation, with unresolved proof stated explicitly
  EVIDENCE: MACOS-GUEST-CONTAINMENT.md, ownership and retirement sections. G4 requires same-boot cleanup after keeper death; no proven cold VM handle/cleanup contract is claimed. This runtime requirement remains unmet.

- [x] GD3: packaging, signing, both desktop architectures, sharing and desktop/provider compatibility have concrete requirements and blockers
  EVIDENCE: MACOS-GUEST-CONTAINMENT.md, compatibility table and linked Apple primary sources. No Mac execution, signing, guest image or live-provider result claimed. Host-specific tool and editor compatibility remains unresolved.

- [x] GD4: adversarial acceptance tests cover the reviewer schedule without weakening the existing descendant contract
  EVIDENCE: MACOS-GUEST-CONTAINMENT.md, G1-G10. /tmp/pr-5769-macos-guest-design-79.7xIxWq/preservation-audit.json verifies all 1822 source-inventory files unchanged, 91 protected paths against the baseline, 32 generated paths, four R4 paths, three D2 correction paths and both prior freeze archives/seals. Existing escape test remains RED, not rerun.

- [ ] GD5: reviewer82 has returned an independent decision on the exact design before implementation
  EVIDENCE: REVIEW-82-MACOS-GUEST.md is the routing handoff. A delivery receipt is not an independent decision. Product and implementation gates remain HOLD.
