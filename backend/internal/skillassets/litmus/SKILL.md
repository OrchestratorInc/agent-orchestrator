---
name: litmus
description: "Test one pull request by driving its build, comparing it with main, and leaving evidence."
trigger: "You were asked to test a PR with `ao testing`."
---

# Litmus PR testing

You test one pull request. Litmus builds the PR and its base commit in private app windows. Check the PR's claim and likely nearby breakage. You have 25 minutes from when builds are ready; Litmus saves unfinished work at the deadline.

You have a shell. Keep scratch files in `scratch/`; never edit Litmus's run files. Never open another run's folder; it is not evidence. Build and drive app windows only through `ao testing`, leave checkouts alone, and never push or post to GitHub (also not `ao testing start|post|discard|prune` or `gh auth`).

The loop:

1. `ao testing intake <pr-url>`, then read `context-pack.md`: the PR text, the diff by area and suggested checks.
2. Decide what to check. Start with the PR's headline claim, in the author's words. Verify a broken claim or new failure first; record a proved failure even when main cannot be compared. Then check likely nearby breakage. Plan pairs: at most 4 for a PR over 30 files, 6 otherwise (a guide, not a limit). One observable thing per check; split a check that mixes two things: `ao testing check add <name> --expect "<what a user would see>"`.
3. `ao testing prepare` (waits for both builds), then `ao testing launch head` (the PR build) and `ao testing launch base`.
4. Run each check on the PR build, then at once on main, before the next check; it is done only when it has a result on both. `check start <id>`, then `snapshot` to see, `click <ref>`, `fill <ref> <text>`, `type`, `press <key>`, `wait` to act. Finish with `check end <id> --result pass|fail|partial|absent|skipped --note "<what you saw>"`. If an earlier receipt shows the failure, pass --shot <N>. On main, re-add the check by name with `--leg base` and pass `--leg base` on every verb. Use `replay <headCheckId> --leg base` only to redo on main a check already completed step by step on the PR build; a first-time check must be driven one step at a time.
5. A check that passes on the PR build and fails on main is what makes a verdict. If main does not have the feature at all, end the main side as `absent` with a screenshot of where it would be; `fail` means the feature is there and broken. If main will not build or launch, or time is short, `submit --skip-base --reason "<why>"` records each unrepeated check as not run on main.
6. Use spare time to pin down a suspected bug, not to add breadth. Add checks only as pairs, never on the PR build alone.
7. `ao testing submit` writes the verdict, posts the comment and ends your session.

Explore as a user. If access needs a login, key or build flag, skip and say which. For missing features, read gate notes.

Never import, export, sign in to or read the owner's real browser profiles, cookies, passwords, keychain or accounts; use test-window data or skip and say why. Accounts marked Device, Active, In use, ChatGPT or Claude are the owner's real logins, and a harness tab's Login, Logout or Refresh login acts on them too: look, never click Use, Switch, Log out, Sign out, Remove, Renew, Sign in again, Login, Logout, Refresh login or Use reset on them. End such a check `skipped` with 'would touch the owner's login'. Accounts you add yourself (API key) are fully testable.

Billing and plan links are off limits, including a "Billing" tab.

Agent chats use the owner's account. Unless the PR changes agents or models, use Haiku 5.5 or Codex's smallest model and one-line prompts.

Read the after-shot; `compare <id>` diffs a check's main and PR-build shots for subtle differences.

At check end, Litmus saves light and dark shots to the receipt and comment. Inspect both on each changed screen. Identical shots yield `theme pair not taken: the app ignored the system theme`; Litmus keeps one. Add differences between similar items (row summaries, list order, mixed names) to the check's `--note` as `Worth a look: <what differs>` at its start or end. Litmus moves that text to a `Worth a look` comment line; it does not change the verdict.

For renderer or UI PRs, do the UI checks in the pack's suggested order.

Never run the PR's tests or any package tests; Litmus judges what a user sees. If the PR has nothing to click, add one check, end it `skipped --note "nothing to click; not tested by Litmus"`, and submit.

Notes go to the PR author. Say what you saw in plain words: buttons present, what happened after a click, what was missing. Omit check ids, build names, receipt numbers and comparisons; Litmus adds those.

Results. `pass`: expectation held. `fail`: it did not. `partial`: saw part; say which. `skipped`: could not reach it; say why. A pass or fail needs a receipt.

When stuck. Never ask. End a broken check `skipped` with a one-line reason. `absent` is for main only. Use `--same-fault` on a base `fail` or `partial` only after proving it has the same cause as the head failure. If a `fail` is recorded, never submit `--blocked`. If a verb or intake fails after one retry, submit `--blocked --reason "<what failed>"` only when no failure is recorded. `status` shows the run.
