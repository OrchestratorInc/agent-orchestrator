---
name: testing
description: Test a pull request or reproduce an issue in an isolated target application, with evidence and a code-review opinion.
---

# Test a pull request or issue

Before testing, list the skills and docs available in this repository and in
AO, then read and use the relevant ones. Include the repo's guidance for
running or launching the app and any diagnostics or triage skill for
gathering evidence.

For a PR, read its description, comments, reviews, linked issues and diff. Run
the PR's latest commit once in dev mode and verify the fix through the UI and
backend with screenshots, clips and logs. Give a code-review opinion. For an
issue, read it and its comments, then reproduce it on current code. Do not
run a base commit by default.

Do not run the repository's build, test or lint suites, even if its docs or
skills recommend them. This includes `go build`, `go test`, `npm test`,
`npm run build` and lint commands. For suite results, only read existing CI
status, for example `gh pr checks`. Starting the app in dev mode is allowed.

Explore the repository at that commit. Start with its own docs and skills:
`AGENTS.md`, `CLAUDE.md`, `README`, `.claude/skills` and `.agents/skills`. Learn
how to run the application and drive the scenario with its own CLI and UI.
Use those tools to create the projects, accounts, sessions or other data the
scenario needs. An empty app or "no data" is not a blocker when its tools can
create the missing state.

Prove that the reported trigger actually happened. The bug merely not
appearing is not proof that it is fixed. State what you observed and what
remains unverified.

Capture screenshots, relevant logs and CLI or read-only DB output. Record
clips around the reproduction: start just before it, stop just after it, and
keep each clip at most 60 seconds. Keep evidence that shows both the trigger
and the resulting behavior.

Report the outcome, steps to repeat, evidence, code-review opinion and a
draft GitHub comment. Never post the comment; posting happens only after
the user approves. Record the actual model, tokens, cost and elapsed time.
Mark unavailable usage or cost as unknown rather than estimating it without
a source.
