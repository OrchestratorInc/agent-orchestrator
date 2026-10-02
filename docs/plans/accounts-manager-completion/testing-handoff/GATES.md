# Gates: cross-platform testing handoff

OWNS: docs/testing/accounts-manager.md, docs/README.md, docs/plans/accounts-manager-completion/testing-handoff/**

Scope: document practical testing on Windows, macOS and Linux; prepare the existing account-picker correction for a normal update to PR 5769. No new product implementation or release-readiness claim.

## Plan

1. Verify the existing 31-file correction against its sealed manifest and read its final test results. Preserve unrelated files and existing release blockers.
2. Write a tester guide with isolated desktop setup, common account workflows, native-platform checks, expected outcomes, and a redacted results template.
3. Review commands against current source, check links and whitespace, and inspect current real-desktop visual evidence without changing saved accounts or sessions.
4. Prepare focused local commits and the PR description update with exact net change counts. Request explicit approval for the named push and PR update before publishing.

- [x] G1: the existing correction still matches its verified source snapshot
  CHECK: sha256sum -c /var/tmp/pr-5769-account-picker-79.wFVwLz/source.sha256
  EXPECT: packages/product-ui/src/TaskComposerView.tsx: OK
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=73b5dbbe765e/37 entries; output=packages/product-ui/src/TaskComposerView.test.tsx: OK | packages/product-ui/src/TaskComposerView.tsx: OK

- [x] G2: the guide covers all requested platforms and separates verified, unverified, and blocked behavior
  EVIDENCE: Guide reviewed against current scripts, UI labels and CLI contracts; all relative links resolve; preview title is accounts-manager.md. Native Windows/macOS commands are reviewed, not executed. verify-corrected.log records the guide, source and current protected-baseline checks.

- [x] G3: publishing material contains inspected current desktop evidence and no credentials or personal account identifiers
  EVIDENCE: EVIDENCE.md links the inspected screenshot and 4.26-second actual Electron recording. Account text and background terminal are hidden at capture. Intermediate unredacted or incorrectly masked diagnostics remain outside the repository. The pinned offline source secret scan passes.

- [x] G4: local changes are scoped and ready for an explicitly approved draft update
  EVIDENCE: The verified 31-file correction is local commit d5a9ef1de76ea6f332e30431566b18b7b66bf9be. The follow-up is limited to the testing guide, docs index, this handoff and two redacted desktop captures. Fresh four-package focused race checks pass. Source inventory, secret-scan results and final proposed counts are retained under /var/tmp/pr-5769-testing-guide-79.nlPeub; publication remains gated by G5.

- [ ] G5: the named push and PR update are authorized, published, and read back
  EVIDENCE: awaiting explicit approval after the exact action is announced. Existing main conflicts, deletion failures, independent review and native-platform gates remain open.
