# Fix loop: iterate on failures without a human in between

The runner does more than report a failure. It classifies each one, says who
can fix it, and writes the next step to `<report>.next-actions.json`. An agent
works through that file, fixes what it is allowed to fix, reruns only what
changed, and stops when only the user or an upstream release can help.

## Classes

| Class | Typical evidence | Who acts | Agent action |
| --- | --- | --- | --- |
| `needs_user` | not logged in, `credentials configured, but AO could not verify them`, HTTP 401/403/429, `-32000 Authentication required`, `Limit Exhausted`, native "take over" prompt, agent not installed | User | Stop this agent. Give the exact step (for example "run `! claude auth login` in the audit account"). Keep auditing other agents. |
| `unsupported_upstream` | gate listed in the contract's `knownUpstream` after a direct native repro | Maintainer | Keep the FAIL. Link the upstream issue in the PR, make AO refuse or guard it truthfully, and do not retry. |
| `environment` | host preflight, build, startup, port, missing `sqlite3`/`go`, long TMPDIR | Agent | Fix the isolated setup, then rerun the same scope. |
| `contract` | interrupt input, ready cue, session/version/model args, missing contract | Agent | Correct the local CLI contract from real native frames or `--help`, then rerun. |
| `ao_bug` | AO API refused or misbehaved; instruction, history or approval not delivered | Agent | Fix AO in `fixTargets`, add a regression test that fails without the fix, rebuild, then rerun only that capability. |
| `runner_bug` | `runner_error` with a JS exception | Agent | Prove the runner wrong with a fixture in `scripts/*.test.mjs` first, then fix it. |
| `flaky_provider` | an accepted request whose token proof the model skipped once | Agent | Rerun once with fresh tokens. A second miss becomes `ao_bug`. |
| `prerequisite` | `NOT_RUN` because an earlier gate failed | None | Fix the first failure in the chain. |

Classification is a heuristic over the gate name, its reason and its observed
API results. When it is wrong, say so in the PR and act on the real class. Do
not edit the evidence to change the class.

## The loop

1. Run the audit with a fresh `--report` basename.
2. Read `<report>.next-actions.json`. Its `status` field decides the next step:
   - `continue`: work through `next` (one item at a time, in class order).
   - `needs_user`: hand over `userActions` and stop.
   - `blocked`: report the exhausted items and stop.
   - `upstream_decision`: record the upstream items for a maintainer and stop.
   - `done`: nothing is left to fix.
3. Fix the item, as one commit per fix on the integration branch.
4. Run the item's `rerun` command. It reruns only that gate or capability with
   its prerequisites, using `--only GATE --previous <this report>.json` and a new
   report basename. Use the agent's `rerunAll` command after fixing several items.
5. Read **Changes since previous attempt** in the new report:
   - `FIXED` is progress.
   - `STILL FAILING (same evidence)` means the fix did nothing.
   - `REGRESSED` means you broke something. Undo that change or fix the regression.
6. Repeat until `status` is no longer `continue`. Before handing off, run one
   full audit without `--only` to prove nothing regressed out of scope.

## Hard limits

- Never change an expected value, loosen a gate, shorten a timeout to dodge a
  check, add a `knownUpstream` entry without a native repro, or turn a FAIL
  into `UNSUPPORTED`. A pass must come from changed product behavior.
- Never touch credentials. Never log in or out, reinstall or remove the user's
  real agent, or change global AO settings outside the isolated data dir.
- Each gate gets at most 3 attempts (`maxAttempts`). Identical evidence after
  a fix stops that gate immediately (`exhausted`). Report it as a blocker with
  its issue file and the fixes you tried.
- Every fix is its own commit. Post before and after evidence for that gate in
  a comment on the integration PR, never in the repository.
- A run where you cannot tell AO from upstream behavior needs a direct native
  repro outside AO before either class is assigned.

## Before every push, and after every merge from main

```bash
node .agents/skills/ao-agent-harness/scripts/harness_touchpoints.mjs --harness <id> --like <similar-id>
node .agents/skills/ao-agent-harness/scripts/harness_pr_check.mjs --base <upstream>/main --pr <number>
```

`harness_pr_check.mjs` fails on the problems that kept reaching review:

- audit evidence committed under `docs/research` (reports, receipts, PNGs)
- workflow edits, or `/home/...` and `/Users/...` paths in added lines
- whitespace errors, or unformatted Go (`goimports`, falling back to `gofmt`)
- a new migration whose number is already used on main or sits below main's
  latest (use the timestamp generator)

It warns when shared observer, ACP, host or session code changed without
Codex/Claude tests, and when the branch has drifted far behind main. With
`--pr`, it reads exact-head CI. For each failed job, it prints the failing log
lines and a class with the next step:

| CI class | Next step |
| --- | --- |
| `lint` | Fix the reported lines. |
| `harness_matrix` | Main added a per-harness table; add your harness to it. |
| `migration` | Renumber the migration. |
| `live_feed` | Strip the version specifier from the package name. |
| `compile` | Fix the compile error. |
| `known_flake` | Rerun the job once. |
| `test` | Reproduce the failing test, then fix it. |

Do not post "waiting for CI" or "reconciled main" comments. Edit one status
comment, and only after reading the result.

After merging main, rerun the touchpoint scanner. Main keeps adding tables
keyed by harness ID, such as terminal-refresh support, install inventory
counts and the effort allowlist. A branch that was green turns red when one of
those tables lacks the new harness.
