---
name: testing
description: Test a pull request or reproduce an issue in an isolated target application, with evidence and a code-review opinion.
---

# Test a pull request or issue

For a PR, code review is the mandatory FIRST step, before any app launch.
Complete the steps below and save the review artifact before starting the
target. If the target is already running, complete this review before any
UI action or scenario setup.
Run the recorded scenario on the PR base, then on its latest head in dev mode,
using matching setup, UI actions and capture points. Retain both exact SHAs.
For an issue, read it and its comments, then reproduce it on current code.

## Review the diff first

These steps are mandatory before launching or using the app for a PR:

1. Run `gh auth status`.
2. Run `gh pr view <pr> --json title,body,comments,reviews,baseRefOid,headRefOid,files`
   and read linked issues and relevant review threads.
3. Run `gh pr diff <pr>`. Read the changed files and their callers, following
   the changed behavior through the code.
4. Run `gh pr checks <pr>` to read existing CI results.
5. Write an artifact stating what the PR changes, the likely trigger and its
   preconditions, expected behavior, and how to reach it in the UI. Record
   any unverified preconditions. Design the numbered scenario steps and
   matching capture points from that analysis.

Before testing, list the skills and docs available in this repository and in
AO, then read and use the relevant ones. Include the repo's guidance for
running or launching the app and any diagnostics or triage skill for
gathering evidence.

Use sequential base and head attempts through the existing `ao testing start`
CLI. Share the warm checkout and recorded scenario, waiting for base cleanup
before starting head. Pass the leg, exact SHA and shared artifact paths through
`--prompt-file`. The base investigator owns the head handoff in the same turn.
It must not start another attempt while its target is running, or return a
final answer before the head investigator has started.
The base investigator includes its scenario as exact numbered steps and inputs,
fixture instructions and capture points in the Markdown passed to
`submit_report`. This stores them in the run's retained evidence before cleanup.
Pass its run/attempt IDs and retained report path to the head investigator,
which must read that report and replay the same steps and inputs verbatim.
After the head attempt completes, run the final review once, using retained
media from both attempts in ONE combined side-by-side preview; do not publish
separate baseline feedback. If the base never reaches the PR trigger, state
that fact and mark the comparison `partial`, never fixed. If either leg or
scenario step is missing, report `partial` and state the missing evidence.

## Run the recorded scenario

Local host runs reuse one long-lived target checkout per repo under
`~/.ao/dev`, fetching and checking out base then head in the same folder.
Keep `node_modules` and the user's normal Go/npm caches. Never run `git clean`
or set a private `GOCACHE`. Run `npm install --prefer-offline` only when the
lockfile hash changed since the last successful install. Use the documented
target preparation and launch tools; stop the base target and wait for
cleanup before preparing head. Each launch gets private AO data, port and
Electron profile, removed after verified shutdown. The code and caches stay.
Cloud VMs are outside this workflow.

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

Put scratch fixtures in the supplied attempt-owned fixture directory. Use
unique child names, never fixed `/tmp` names or unchecked `rm -rf`. Recreate
the same fixture contents for both revisions; retain evidence before cleanup.

Prove that the reported trigger actually happened. The bug merely not
appearing is not proof that it is fixed. State what you observed and what
remains unverified. Each revision needs at least one real UI action and a
screenshot of its result before claiming behavior. If the actual PR trigger
was not reached, report `partial`, even if ordinary app behavior worked.
Generation delays do not prove delayed acceptance or other timing triggers.

Capture screenshots, relevant logs and CLI or read-only DB output. Retain
a matching screenshot and a short clip for each revision. Select segments
around the trigger and result from each retained original, at most 15 seconds
per clip. Use this skill's `scripts/export_clip.py` with `--attempt-dir`,
`--recording`, `--start`, `--duration` and `--label before|after`. It checks
ownership and duration, decodes the export and retains selection/checksum
files. Inspect the frames too. Keep the originals.

Report the outcome, steps to repeat, evidence, code-review opinion and a
draft GitHub comment. Record the actual model, tokens, cost and elapsed time.
Mark unavailable usage or cost as unknown rather than estimating it without
a source.

The final PR review must include actionable inline file:line findings in its
review payload, or explicitly say "no issues found" and list the changed
files, callers and behavior checked.
Keep the code-review opinion separate from unverified runtime claims.

## Start head automatically after base

`submit_report` finishes the target attempt, not the investigation. After the
base report, continue with ordinary shell tools. Its screen capability is
revoked, so start a fresh head investigator through the existing CLI:

1. Use the supplied supervisor launch context. For each supervisor command,
   set `AO_RUN_FILE` to `supervisorRunFile` and `AO_DATA_DIR` to
   `supervisorDataDir` in that subprocess only. Run `supervisorCommand testing
   evidence <attemptId>` and read the receipts under `evidenceDir`. Wait for a
   `cleanup` receipt whose JSON says `state: complete`, with no leftovers,
   and retained recording metadata. If cleanup fails, report the blocker;
   never change the warm checkout while the base target remains alive.
2. Run this skill's `scripts/prepare.py` with `--repository <checkoutPath>`
   and `--commit <recorded-head-sha>`. This is app launch preparation, not a
   repository build/test suite. Reuse the exact warm checkout and recipe.
3. Save the quoted issue snapshot and a head prompt outside the repository,
   under the retained evidence directory. The prompt must name the base/head
   SHAs, base run/attempt IDs, retained base report and evidence directory.
   Tell head to replay the base report's exact numbered steps and inputs
   verbatim, using its own target CLI wrapper and fixture directory for
   attempt-owned paths. It must then export media from both attempts, write
   ONE combined `review-payload.json` and `review-preview.md`, report the
   preview artifact and request approval. Invoke `supervisorCommand testing
   start --project <projectId> --recipe <recipeId> --issue-url <pr-url>
   --issue-file <snapshot-file> --commit <recorded-head-sha> --prompt-file
   <head-prompt-file> --timeout <timeoutSeconds> --json`, carrying forward
   nonempty `--agent`, `--model` and `--effort` from the launch context.
   Retain the returned run, attempt and worker IDs before ending this turn.
   On failure, report `partial` with the error; do not claim a comparison or
   start duplicate investigators. The head investigator must not start
   another head attempt.

## Finish with an approved review or comment

Use `submit_report` to save the verdict and finalize the target recording,
then perform the handoff above for a PR base attempt. Continue once after head,
using retained evidence from both attempts. For an issue, continue after its
single attempt. Wait for the retained recording and metadata
before exporting media. Keep publication files in the session artifact
directory, outside the repository.

1. Preserve the originals. Export 1-3 clips of at most 15 seconds each and key
   screenshots showing the trigger and result. Decode the clips and inspect
   their frames and screenshots before proposing publication. Record source
   intervals, any omitted gaps, and SHA-256 checksums alongside the exports.
   Never claim a fix or observation window beyond the evidence you observed.

2. Write `review-payload.json` and `review-preview.md`. For a PR, the JSON
   contains `commit_id` (the reviewed head), `body`, `event`, and `comments`
   with one `{path, line, side, body}` object per actionable diff finding.
   Anchor lines to the reviewed diff (`RIGHT` for new lines, `LEFT` for old).
   Use `REQUEST_CHANGES` for required fixes, `COMMENT` for limited or
   inconclusive results, or `APPROVE` when the evidence supports approval.
   GitHub disallows approval or change requests on your own PR; preview a
   `COMMENT` with the code-review opinion in its body in that case. The
   preview shows the destination, event, exact text and inline file:line
   findings, with local screenshots and clips embedded. Include the exact
   evidence-comment text too: the pinned CLI uploads attachments through a
   comment. For an issue, prepare a comment body without PR-review fields.
   Name the running app commit and reviewed head; disclose any difference.
   Show matching before/after screenshots and clips side by side in the
   preview, with base/head SHAs and the observed result in each column.
   Label before as broken only if reproduced, and after as fixed only if the
   same trigger reached the expected result. Otherwise label it partial or
   still broken; do not imply an unavailable comparison succeeded.

3. Present the preview to the user and report it as an AO artifact. WAIT for
   explicit approval of the destination, event, text and selected media.
   Do not upload or post before approval. If these change, get approval for
   the revised preview. Keep publication pending while waiting.

4. After approval, recheck the PR head before any upload or post; a changed
   head needs a revised review and approval. Set
   `testing_gh="$HOME/.litmus/bin/gh-2.102.0"`; verify its binary checksum
   against the accompanying `gh-2.102.0.json` and read `--help` for attachment
   support. If the pinned CLI is unavailable, report the blocker.
   For a PR, post the approved evidence comment with
   `"$testing_gh" pr comment <pr> --repo <owner/repo> --body-file <file>`
   and repeated `--attach <file>` flags. Read back its hosted media URLs and
   replace only the approved local media references in `review-payload.json`.
   Post a real review with inline comments using
   `"$testing_gh" api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input review-payload.json`.
   For an issue, use
   `"$testing_gh" issue comment <issue> --repo <owner/repo> --body-file <file> --attach <file>`.

5. Save the returned review ID (or issue-comment ID), URL and reviewed SHA.
   Re-read the hosted review/comment and inline comments with the pinned
   CLI's `api` and compare their text with the approved payload, allowing only media-URL
   replacement. Download every hosted attachment and compare its SHA-256
   with the reviewed export. Record the readback results in the final AO
   report. If text, media or publication cannot be verified, say so and keep
   completion pending; do not retry a post without checking for duplicates.
