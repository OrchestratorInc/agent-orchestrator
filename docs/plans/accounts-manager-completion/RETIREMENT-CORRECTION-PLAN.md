# Retirement correction

Baseline: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. Review evidence: `/var/tmp/pr-5769-review-79.OZI4RL/REVIEW.md`.

## Decision

An uncontained Unix process group is not a complete execution boundary. A missing group cannot establish that an unobserved child did not leave it. Do not acknowledge exact retirement or reuse an old stopped receipt using that observation alone. A positively different boot can retire prior-boot execution. Windows job proof remains a separate contract.

This is an immediate safety correction, not completed containment. Same-boot automatic retirement of uncontained managed Unix launches remains unavailable until a kernel-backed launch boundary is implemented. Do not change the product contract to count refusal as success. Do not change native session launch, native switching, Subscriptions, or the frozen guest design.

## Execution

1. Preserve and rerun the existing escaped-descendant red regression. Add a failed-first check for previously persisted false stopped receipts and unknown-identity retirement. Retain foreign-owner and previous-boot controls.
2. Centralize retirement acknowledgement validation. Both fresh completion and cached receipts must require the same platform proof. Keep ordinary host shutdown separate from a credential-deletion acknowledgement.
3. Review every acknowledgement path and check native host behavior. Run bounded race tests, then the full affected package, build/vet and platform compile checks. Record any assertion that relied on the old incomplete group proof rather than hiding it.
4. Hand off the bounded correction for independent review. Complete actual same-boot contained launch/recovery only after the accepted Linux/Mac feasibility contracts have execution evidence.

## External constraint

This workspace is Linux x86_64 and has no `xcrun` or `swiftc`. The Mac guest design requires native arm64 and x64 keeper-death proof before production adoption. There is no existing local guest adapter or packaged guest image to wire in. Native execution and containment implementation remain open; a process-group fallback is not a safe substitute.

## Acceptance

- [x] S1: the existing two-leaf escaped-descendant regression refuses retirement and credential deletion while the child lives, including SQLite reopen/retry
  CHECK: go test -v -race -count=3 -timeout=180s ./internal/adapters/chatdriver/persistenthost -run '^TestProviderOwnerEscapedDescendantBlocksRetirement$'
  EXPECT: /ok\s+.*\/persistenthost/
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=0714becd3eff/40 entries; output=2026/09/29 10:26:24 ERROR interface transition: retry queued-message delivery error="list deliverable interface transitions: context canceled" | ok  	github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost
- [x] S2: cached group-only stopped receipts cannot authorize exact or unknown-identity retirement on the same boot
  CHECK: go test -v -race -count=3 -timeout=180s ./internal/adapters/chatdriver/persistenthost -run '^TestProviderOwnerGroupReceiptRequiresRetirementProof$'
  EXPECT: /ok\s+.*\/persistenthost/
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=0714becd3eff/40 entries; output=PASS | ok  	github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost	1.274s
- [x] S3: Linux native host operation, protected source and unaffected ownership evidence remain unchanged or verified
  EVIDENCE: final/safety-final-race.log passes seven selected top-level tests three times, including raw/ACP native shutdown, replacement survival and strict proof controls; full ACP adapter race passes 98 top-level tests. final/preservation.json verifies all 91 protected files, 33 generated files and the five-entry guest-design package. Source manifest SHA256 da345a0f8a5bbad19b352177f541390ab3abee62da3fc793ef42e733cc664913. All final paths are under /var/tmp/pr-5769-retirement-fix-79.x3Z1f1.
- [ ] S4: the bounded safety correction has final race/build/vet/compile evidence and independent review
  EVIDENCE: final/verification.json records 14 passing commands and one failed full persistent-host race suite (three groups, five failing cases). Build/vet, bounded races, Linux/Mac changed-scope lint and Mac arm64/x64 plus Windows x64 package cross-compile/vet pass. Independent review pending. Full same-boot recovery remains unmet; see RETIREMENT-CORRECTION-REVIEW.md.
- [ ] R1: Linux same-boot contained execution and exact deletion recover across host death, replacement and escaped descendants
  EVIDENCE: unresolved containment implementation; a refusing guard cannot meet this gate
- [ ] R2: Mac arm64/x64 same-boot contained execution and cold retirement satisfy the accepted guest design
  EVIDENCE: blocked on native feasibility execution and guest implementation; requested through orchestrator
