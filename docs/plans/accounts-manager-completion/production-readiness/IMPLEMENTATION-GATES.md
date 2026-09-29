# Gates: production-readiness implementation

Scope: execute PLAN.md against SPEC.md. All milestones are OPEN. Writing this ledger does not execute tests, clear a review or close a product gate. The document-authoring ledger is GATES.md.

Milestones combine automated assertions, native/live observations and independent review. Before starting one, attach exact owned files, runnable checks with expected results, test discovery and negative controls. A passing command covers only its assertions. Missing resources remain unmet. Shared acceptance IDs require evidence from every contributing milestone and final reconciliation in M11.

- [ ] M00: preserved baseline, base integration, capability matrix and reviewed ownership contracts are ready for implementation.
  EVIDENCE: partial, /var/tmp/pr-5769-production-79.Lvj7m9. HEAD d23bdaee42640da7d7fe70fd0b9af52843a3ca61 preserved; all 91 protected paths and five guest-design records match the integrated baseline. Five retirement assertions reproduce in three groups. Read-only merge-tree against observed main e853b39601876d4be6d51c7f99dce0adcea5fa10 exits zero, but no base integration was performed and the provider reports a conflicting PR. Ownership review, resource and capability reconciliation remain open.

- [ ] M01: real managed Linux launches use independently reviewed creation-time containment and prove exact original-owner retirement.
  EVIDENCE: pending; PLAN.md M01; R1-A, R1-B and R4-A foundation. Retain all five positive recovery cases and the late-fork/moved-descendant negative controls.

- [ ] M02: coordinated multi-session removal, durable revocation and every required crash/cancellation cut recover without fallback or resurrection.
  EVIDENCE: pending; PLAN.md M02; R1-A, R1-B, R1-C, R1-D, R1-E, R1-F. Requires M01 independent CLEAR on the exercised platform and separate deletion review.

- [ ] M03: new shutdown interruptions and existing misclassified switch journals recover with exact ownership and correct cancellation semantics.
  EVIDENCE: partial, SWITCH-SHUTDOWN-GATES.md and SHUTDOWN-REVIEW.md. New pre-stop shutdown correction reproduced red; 105 focused cases x3, four affected package races, 29 process cases on rerun, build/vet/lint and preservation checks passed. Independent review is pending. Historical failed-record repair, public pre-stop Retry exposure and complete M03 acceptance remain open. This independent slice proceeds while M01 ownership design is under review; it does not enable containment or deletion.

- [ ] M04: model/effort/permission state, queues and authorization remain consistent through reattachment and actual runner/vault cold restart.
  EVIDENCE: pending; PLAN.md M04; R2-B, R2-E, R2-F. Preserve uncertain remote outcomes without automatic replay.

- [ ] M05: separately owned managed Codex Chat controllers produce isolated overlapping work, resume safely and integrate with exact retirement.
  EVIDENCE: pending; PLAN.md M05; R3-A, R3-B. Platform capability enablement requires that platform's cleared ownership/recovery contract.

- [ ] M06: isolated profiles and explicit identity-checked migration/reconnect preserve native state and serialize credential generations safely.
  EVIDENCE: pending; PLAN.md M06; R3-C, R3-E. Includes wrong-identity, lost-response, refresh/remove races and independent credential review.

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
