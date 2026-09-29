# W1 installed-process routing proof

Extend the sealed runner overlap proof with the installed command-line client and production launch configuration. This is a bounded protocol/identity test, not live-provider, terminal-controller, managed Chat or release acceptance. Preserve W1/W2/W4 source seals and the protected native-switch paths.

## Decision and sequence

1. Build the current runner before entering a test-owned network/PID namespace. Run the fixture, runner and both client processes inside it. Use only synthetic credentials, a private home and a local upstream. No provider traffic, host login or real desktop data is available.
2. Generate route configuration through the existing production adapter. Exercise the installed client's documented noninteractive JSON mode, retain the production account flags, and suppress only test-owned activity hooks. The upstream records actual credential identity and holds both streams open concurrently.
3. Require A and B to complete under their chosen identities with conflicting synthetic native file and environment credentials. Add a wrong-token oracle, A-only disable/revocation, B survival, restart and unchanged native-file controls. Any discovered production defect needs a preserved failed-first assertion before correction.
4. Self-review the oracle and cleanup after first green. Run narrow race repetitions, the complete adapter package, affected runner checks and preservation. Seal the exact test and any necessary production correction for independent review.

Official references: [noninteractive mode](https://learn.chatgpt.com/docs/non-interactive-mode) documents JSON events and ephemeral runs; [configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference) documents custom provider endpoints, environment credentials and memory-only credential storage. Actual installed behavior remains the test authority; no version claim comes from documentation alone.

## Acceptance

- [x] I1: both installed processes have overlapping upstream requests authenticated as distinct explicit accounts; no default/native credential substitution.
  EVIDENCE: `final-focused.log` in `/var/tmp/pr-5769-w1-installed-79.SeryiP` passes 15 scenario executions across three race repetitions on client 0.153.4. Both upstream streams remain open before either is released. A second scenario uses distinct session capabilities for the same account. All credentials and responses are synthetic.
- [x] I2: the identity oracle rejects a deliberately mismatched route; disabling/revoking A cannot select B or stop B's work.
  EVIDENCE: the wrong-route control observes B's actual credential and denies the A-only prompt. Disabling A produces zero A model calls while B remains active and later completes. A stale revision also produces zero upstream calls. This does not test cancellation of an already-running A stream.
- [x] I3: restart/reconciliation and stale-capability controls preserve binding identity, with native credential files unchanged before and after.
  EVIDENCE: a restarted production runner denies old capabilities before reconciliation, then completes overlapping A/B requests after the exact bindings are restored. Advancing A's revision rejects its old capability. Synthetic native file-login controls pass before and after; original profile bytes match.
- [x] I4: midpoint review, repeated race and package checks, secret-safe diagnostics and preservation pass on the exact sealed source.
  EVIDENCE: final focused race count 3 passes in 135.305s. Complete tagged adapter race passes in 65.587s, with 125 parent-test leaves plus five embedded process scenarios; two existing Windows-only discovery tests skip on Linux. Backend build/vet, tagged vet and pinned lint pass. All preservation manifests match. Exact details are in `W1-INSTALLED-PROCESSES-REVIEW.md`.
- [ ] I5: independent review accepts the bounded proof. Live identities, interactive terminal/controller behavior, native platforms and production containment remain separate.
  EVIDENCE: pending.

## Midpoint review

After the initial overlap passed, fixture cleanup was extended to stop even a partially started runner and join all client/renewal work. Diagnostics check synthetic secrets before being displayed. Completion now requires parsed stdout events and the account-specific message, independently of stderr; a substring in a diagnostic cannot count as success. The final verification ran after this stricter oracle was installed.

No production defect was reproduced in this leaf, so no production source was changed. The deliberately wrong-route case is a negative oracle control, not a claimed pre-fix failure. The actual runner is built from the current source; the client uses production launch flags plus noninteractive/test-hook adjustments. This does not exercise the interactive terminal controller, live account permissions, or contained managed Chat.
