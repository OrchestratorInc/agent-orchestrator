# Gates: PC5 switch execution supplement

OWNS: backend/internal/daemon/accounts_manager_execution_test.go, docs/plans/accounts-manager-completion/PC5-EXECUTION-GATES.md, docs/plans/accounts-manager-completion/REVIEW-82-PC5-EXECUTION.md

Scope: extend the sealed production-wiring tests through real coordinator target launch and readiness, using isolated synthetic outbound ports. Preserve the prior four-file seal and shutdown source. No publication or full desktop/runtime claim.

- [ ] E1: the production execution test fails against an exact pre-wiring overlay before passing on the wired service.
  EVIDENCE: pending
- [ ] E2: explicit A-to-B and managed-to-native switches execute through startSession, the public router, SQLite, account service and real coordinator; readiness is not acknowledged before the target is proven ready.
  EVIDENCE: pending
- [ ] E3: failed target launch retains its recovery obligation and input fence; explicit retry reaches readiness without replaying the task or changing unrelated sessions.
  EVIDENCE: pending
- [ ] E4: focused races, affected package checks, self-review, source hashes and protected manifests cover the final supplement; reviewer82 receives an exact midpoint handoff.
  EVIDENCE: pending

## Plan and limits

Use a separate test file so every byte in `/tmp/pr-5769-pc5-79.0k7pqi/pc5-wiring-slice.sha256` remains unchanged. Construct the concrete service and coordinator with their production factory. Substitute only the outbound runtime, provider command and credential catalog. Gate target readiness explicitly and observe the real durable journal through the HTTP contract. A pre-wiring Go overlay removes only the already-proven dependency and new service adapter for the failed-first comparison; the live tree is not reverted.

After the first passing execution matrix, inspect the test's oracle and negative controls, rerun under race and freeze for midpoint review. Real process, runner authorization, cold recovery and actual desktop acceptance remain broader PC5 work. This supplement cannot clear native platform or live-provider gates.
