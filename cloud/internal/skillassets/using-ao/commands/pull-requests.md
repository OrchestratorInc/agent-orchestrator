# Pull requests and reviews in a cloud sandbox

Workers do not push with personal credentials. The sandbox provides two socket
helpers (their exact invocations are in the `$AO_PULL_REQUEST_HELP` and
`$AO_REVIEW_HELP` environment variables) plus two `ao` commands.

## Opening a pull request

Commit your work on the session branch (`$AO_SESSION_BRANCH` — the branch the
workspace was checked out on; do not create a different one), then run the
command described in `$AO_PULL_REQUEST_HELP`:

```
curl --unix-socket $AO_PULL_REQUEST_SOCKET -X POST http://localhost/pull-request \
  -H 'Content-Type: application/json' \
  -d '{"branch":"<pushed branch name>","title":"<PR title>","body":"<PR body>"}'
```

The control plane pushes the branch and opens the PR against the project's
configured default branch, and the PR is attributed to this session automatically (it
shows up in `ao list` for your orchestrator).

## ao claim-pr — attach an existing PR

```
ao claim-pr <number-or-url>
```

Use when a PR for this work already exists (for example, opened in an earlier
session) and this session should own it. Prints `claimed PR #<n> <url>`.

## ao review trigger — start an AO review

```
ao review trigger
```

Use when asked to review this session's PR or to request an AO review. It
starts AO's reviewer on every open, non-draft PR this session owns, in its own
reviewer terminal that the human watches in the desktop app. Prints one line
per PR: `started AO review of PR #<n> <url>`, or `AO review of PR #<n> is
already running <url>`. The reviewer's findings arrive in this session as PR
review comments. Do not review the PR yourself instead.

## Submitting an AO review verdict

Only when the prompt asked this session to review a PR: run the command in
`$AO_REVIEW_HELP` with the review run id from the prompt and a verdict of
`approved` or `changes_requested`.
