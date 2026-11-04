# Evidence for PR #7674: one login listed as two Claude Code accounts

Evidence-only branch: no shared history, never merged.

- **Before** = base commit `23840aa541` (main when the PR was opened). **After** = PR head `a4b2587801`.
- **Real data.** Each tree's real `agentaccounts` service and `AgentAccountsController` read a real Mac's real logins: the Claude Code CLI login (`~/.claude`) and one AO-added account. The two hold the same login. See `harness_test.go.txt`. The harness is read-only: no daemon, no DB writes, and it refuses every non-GET request.
- **Screenshots.** Each tree's own renderer (`npm run dev:web`) is driven by Playwright (`screenshot.spec.ts.txt`). `GET /api/v1/agents/accounts` comes from that tree's harness, and every other GET goes to the machine's live daemon. The email is replaced with `user@example.com` in every response before it renders.
- **Tests.** The PR's new tests are run on both trees. On the base tree, `AccountView.SameLoginAs` is added as an empty field (no logic) so the Go tests compile. The base failures are therefore missing behaviour, not a missing symbol.

| File | Before | After |
|---|---|---|
| `*-settings.png` | two rows, same login | one row |
| `*-accounts.json` | no `sameLoginAs` | CLI login `sameLoginAs` = the saved account |
| `*-go-tests.txt` | 9 bug scenarios fail, 5 guard cases pass | all pass |
| `*-vitest.txt` | 2 new panel tests fail, 5 existing pass | all 11 pass |
