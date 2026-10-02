# Gates: production-readiness implementation

Scope: execute PLAN.md against SPEC.md. All milestones are OPEN. Writing this ledger does not execute tests, clear a review or close a product gate. The document-authoring ledger is GATES.md.

Milestones combine automated assertions, native/live observations and independent review. Before starting one, attach exact owned files, runnable checks with expected results, test discovery and negative controls. A passing command covers only its assertions. Missing resources remain unmet. Shared acceptance IDs require evidence from every contributing milestone and final reconciliation in M11.

- [ ] M00: preserved baseline, base integration, capability matrix and reviewed ownership contracts are ready for implementation.
  EVIDENCE: partial, /var/tmp/pr-5769-production-79.Lvj7m9. HEAD d23bdaee42640da7d7fe70fd0b9af52843a3ca61 preserved; all 91 protected paths and five guest-design records match the integrated baseline. Five retirement assertions reproduce in three groups. Read-only merge-tree against observed main e853b39601876d4be6d51c7f99dce0adcea5fa10 exits zero, but no base integration was performed and the provider reports a conflicting PR. Ownership review, resource and capability reconciliation remain open.

- [ ] M01: real managed Linux launches use independently reviewed creation-time containment and prove exact original-owner retirement.
  EVIDENCE: pending; PLAN.md M01; R1-A, R1-B and R4-A foundation. Retain all five positive recovery cases and the late-fork/moved-descendant negative controls. The 2026-09-30 full host race run reproduces those same five failures in /var/tmp/pr-5769-implementation-79.5HnQwE/host-final-race.log. The next managed launch integration boundary is LINUX-MANAGED-LAUNCH-PLAN.md; it is not implemented or independently cleared.

- [ ] M02: coordinated multi-session removal, durable revocation and every required crash/cancellation cut recover without fallback or resurrection.
  EVIDENCE: pending; PLAN.md M02; R1-A, R1-B, R1-C, R1-D, R1-E, R1-F. Requires M01 independent CLEAR on the exercised platform and separate deletion review.

- [ ] M03: new shutdown interruptions and existing misclassified switch journals recover with exact ownership and correct cancellation semantics.
  EVIDENCE: partial, SWITCH-SHUTDOWN-GATES.md, SHUTDOWN-REVIEW.md and RETRY-CONTROLS-REVIEW.md. New pre-stop shutdown correction and public pre-stop retry eligibility are implemented; the current switching/cancellation/recovery race selection passes three repeats in /var/tmp/pr-5769-implementation-79.5HnQwE/switch-final-race.log. Historical failed-record repair and complete M03 acceptance remain open. The later read-only audit found no stored prior-phase field; the switch CDC trigger records only session ID. A failed row with target_revision=0 alone cannot prove stopping never began because recovery_required can advance to failed before commitment. No blanket repair or journal rewrite was applied. This independent slice does not enable containment or deletion.

- [ ] M04: model/effort/permission state, queues and authorization remain consistent through reattachment and actual runner/vault cold restart.
  EVIDENCE: pending; PLAN.md M04; R2-B, R2-E, R2-F. Preserve uncertain remote outcomes without automatic replay.

- [ ] M05: separately owned managed Codex Chat controllers produce isolated overlapping work, resume safely and integrate with exact retirement.
  EVIDENCE: partial, RESUME-GATES.md C0 and RESUME-REVIEW.md. The managed adapter, isolated profiles, route validation and generation-bound host attachment are implemented. Installed-process synthetic A/B overlap, private-history restart, rejected A authorization with B survival, native profile preservation and hostile-proxy controls pass three repeats. Production registry/service enablement and complete containment/switch/removal integration remain blocked. Platform capability enablement requires that platform's cleared ownership/recovery contract.

- [ ] M06: isolated profiles and explicit identity-checked migration/reconnect preserve native state and serialize credential generations safely.
  EVIDENCE: partial managed Codex profile foundation in RESUME-REVIEW.md; private paths, explicit route authorization and native-home preservation are tested. This does not complete terminal profile integration or explicit legacy migration/reconnect. PLAN.md M06; R3-C, R3-E still require wrong-identity, lost-response, refresh/remove races and independent credential review.

- [ ] M07: supported login/verification and quota/status recovery are truthful, generation-scoped, secret-safe and responsive.
  EVIDENCE: pending; PLAN.md M07; R3-D, R3-F. Existing positive picker/quota evidence is retained but does not close final integrated acceptance.

- [ ] M08: native Windows kernel ownership, recovery and packaged desktop behaviour meet the full contract.
  EVIDENCE: pending native execution; PLAN.md M08; R4-B and Windows portion of R4-E. Cross-compilation and compatibility-runtime results cannot satisfy this gate.

- [ ] M09: both native Mac architectures prove keeper-death retirement before product guest integration, then pass production and artifact acceptance.
  EVIDENCE: pending native feasibility and implementation; PLAN.md M09; R4-C, R4-D and Mac portion of R4-E. Keep product HOLD if exact same-boot proof fails.

- [ ] M10: final live desktop and all platform workflows, restart/removal/revocation and measured responsiveness meet the specification.
  EVIDENCE: pending; PLAN.md M10; R4-A, R4-E, R5-C, R5-D. Destructive live checks require the relevant prior safety CLEAR and disposable credentials.

- [ ] M11: one final integrated candidate passes required suites, compatibility and independent review with every specification case accounted for.
  EVIDENCE: pending; PLAN.md M11; R5-A, R5-B, R5-E and final reconciliation of all 28 acceptance cases. SHUTDOWN-REVIEW.md retains an intermittent existing PTY fixture teardown failure that passed isolated and full reruns without source changes; it still needs release stability review. Publication needs separate explicit approval; it is not implied by implementation completion.
