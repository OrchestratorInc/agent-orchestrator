# Reviewer82: Mac guest containment design

Request: independent static design/feasibility review before implementation. This is not a runtime CLEAR request. Keep the lifecycle/product and public-controls gates HOLD. Work locally only; do not publish or edit source during review.

## Exact review target

- Worktree: `/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79`.
- HEAD: `048a59775999b60f8276a1f5d1107dbef57f5483`.
- Primary design: `docs/plans/accounts-manager-completion/MACOS-GUEST-CONTAINMENT.md`.
- Design SHA256: `6a433b17831dd4f234bfbe3f5f361fbc06944f997177971bce864fb12a74bc6a`.
- Gates: `docs/plans/accounts-manager-completion/MACOS-GUEST-DESIGN-GATES.md`.
- Exact package manifest: `/tmp/pr-5769-macos-guest-design-79.7xIxWq/review-package.sha256`.
- Preservation audit: `/tmp/pr-5769-macos-guest-design-79.7xIxWq/preservation-audit.json`.
- Document checks: `/tmp/pr-5769-macos-guest-design-79.7xIxWq/document-checks.json`.

## Decisions needing independent review

1. Is an architecture-matched, per-launch Linux guest using Virtualization.framework an acceptable feasibility candidate? The design does not yet prove abnormal VM-helper-death cleanup or a cold, reopenable VM identity. G4 must establish same-boot exact retirement, including a consumed-start permit before active publication. Indefinite safe refusal is not accepted as product completion. If the framework cannot satisfy this, reject the candidate rather than weakening the test.
2. Does the proposed owner/tombstone protocol prevent restart across cancellation, SQLite reopen, replacement publication/removal, helper death, disk restoration and stale readiness? All workload launches are recorded before any guest execution; no application registry absence is retirement proof.
3. Are the execution/file/network boundaries complete? All provider tools and commands remain in the guest. No host command bridge, native fallback, writable host credential/Git metadata share, cloud checkpoint push, or cloud deletion-timeout success. Copied request capabilities must remain unusable after revocation.
4. Are the compatibility blockers explicit enough to guide implementation without narrowing the product contract? Linux guest tooling, host-specific project tools, editor sync, signed arm64/x64 packaging, old native managed owners, real desktop and live-provider evidence remain acceptance work.
5. Does G1-G10 cover the original host-parent death, escaped child, replacement publication/shutdown and SQLite-reopen schedule, including simultaneous unrelated native execution? Please identify any missing crash cut or false acknowledgement before code starts.

Requested verdict: bounded design acceptance for the feasibility experiment, or concrete HOLD findings. Neither verdict certifies the final runtime. Source implementation waits for the independent response. No Windows or refresh gate is advanced by this design.

## Verification and preserved evidence

This slice changes three Markdown files only. Sixteen repository source citations have existing paths and in-range line locations. Document whitespace checks and `git diff --check` passed. The design was opened in this session's existing desktop preview; that is document display, not application-flow evidence. No Go tests, guest runtime, Mac build, signing or provider flows were run for this prose-only slice.

The read-only preservation audit verified the 1821-file bounded F2/F3 source inventory plus the unchanged Windows contract test (1822 total), all 91 protected paths against baseline `b398a95c59425c381ec2f3d36d507097c9ccc2de`, 32 generated paths, four R4 files and three D2 correction files. It rehashed both prior source/review archives, their manifests, evidence and frozen handoffs. No protected/native switching or Subscriptions edits.

The bounded F2/F3 seal remains `/tmp/pr-5769-life-f2f3-79.CYTx36/final-seal.json`, SHA256 `3583edc67c8fa9f31b59a37efcf4bde5c1c3f601abb3904b60ed388ea846a39c`. D2 remains independently CLEAR within its bounded scope; its seal SHA256 is `eb53e642a36c87477b15e449f0240f9469bc27a12d6c7ffd40fdeaf4688abb72`. These frozen packages are not rewritten or recertified by this document.

The escaped-descendant test SHA256 remains `9db66e9f0504ec77a728d2872db4ab27d225a65fe328f30b2ce947eb3248d290`. Its inherited red log is `/tmp/pr-5769-lifecycle-79.tRGYkc/owner-escaped-descendant-red.log`; it was not rerun or weakened. The latest full Windows diagnostic matrix remains red under Wine, preserved at `/tmp/pr-5769-windows-recovery-79.TPYVn9/windows-complete-matrix-red.log`. The original bounded archive must not be interpreted as superseding that result.

The first sandboxed integrity command reported `spawnSync rg EPERM`; the same read-only audit succeeded with permitted access. The initial confined preview probe also failed; retry through the existing daemon succeeded. No service was started, shared process stopped, runtime source changed or remote publication attempted.

## Next action

Review the exact design and report to orchestrator59 and session79. If accepted for feasibility, a native Mac execution environment on both architectures is still needed to establish G4 before product wiring. The current Linux workspace has neither `xcrun` nor `swiftc`; this is an execution-evidence constraint, not proof that the architecture is impossible.

Fun fact: a VM's storage can outlive its execution, allowing account deletion to stop computation while preserving unfinished work.
