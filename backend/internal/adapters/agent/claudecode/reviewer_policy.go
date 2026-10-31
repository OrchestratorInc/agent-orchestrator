package claudecode

import (
	"regexp"
	"strings"
)

// ReviewerAllowedTools is the read-only tool allowlist an AO code reviewer runs
// with, in a terminal pane or a Chat. The reviewer runs headless (no human to
// approve prompts) but must stay read-only, so instead of bypassPermissions —
// which skips the permission system entirely and ignores allow/deny rules — it
// launches in a mode where these rules are honored: allow rules auto-approve
// without prompting, so the reviewer can read the checkout and run the few
// commands it needs (git diff/log/show to inspect the PR, gh pr view/diff/checks
// to read PR metadata, printf to pipe review JSON into the downstream commands
// without writing a worktree file, and `ao review submit` to record the
// verdict). Claude Code ≥ 2.1.257 still prompts on Bash commands its analyzer
// cannot verify statically even when an allow rule matches; AO answers those
// prompts with ReviewerSubmitCommandAllowed (the PermissionRequest hook for a
// terminal pane, the ACP permission policy for a Chat) so the reviewer never
// stalls. That answer is also the only route for the `gh api --method POST
// .../reviews` call that posts the review: a blanket Bash(gh:*) allow rule
// would admit merges, closes, and arbitrary API mutations that a prefix deny
// list cannot enumerate (flag spellings, flag order, graphql).
var ReviewerAllowedTools = []string{
	"Read",
	"Grep",
	"Glob",
	"Bash(printf:*)",
	"Bash(gh pr view:*)",
	"Bash(gh pr diff:*)",
	"Bash(gh pr checks:*)",
	"Bash(git diff:*)",
	"Bash(git log:*)",
	"Bash(git show:*)",
	"Bash(git status:*)",
	"Bash(ao review submit:*)",
}

// ReviewerDisallowedTools hard-denies the write paths as defense in depth, so a
// misbehaving model cannot edit files or move the branch even if a future
// allowlist entry would otherwise admit it.
var ReviewerDisallowedTools = []string{
	"Edit",
	"Write",
	"NotebookEdit",
	"Bash(git push:*)",
	"Bash(git commit:*)",
	// The reviewer must never merge. No allow rule admits it, so this only
	// documents the intent and survives a future broader gh allow entry.
	"Bash(gh pr merge:*)",
	// No human answers a headless reviewer. A Chat client that renders forms
	// would otherwise turn this tool into a question that waits forever.
	"AskUserQuestion",
}

// reviewerSubmitJSONLiteral is a single-quoted shell literal. An embedded single
// quote may use either common escape: quote-backslash-quote-quote, or a
// double-quoted quote between closing and reopening quotes. Neither can expand
// anything, and models pick either one for apostrophes in review text.
const reviewerSubmitJSONLiteral = `'[^']*(?:(?:'\\''|'"'"')[^']*)*'`

// reviewerSubmitCommandPattern matches the exact command shapes the review
// prompt dictates: a single-quoted JSON literal fed through `printf '%s'` into
// either the GitHub review POST or `ao review submit`. Claude Code ≥ 2.1.257
// prompts on any Bash command its analyzer cannot verify statically, and allow
// rules never match such commands, so the headless reviewer would hang. The
// shape is safe to auto-allow: `printf '%s'` performs no format interpretation
// and a single-quoted operand cannot expand or run anything.
var reviewerSubmitCommandPattern = regexp.MustCompile(`^printf '%s' ` + reviewerSubmitJSONLiteral + ` \| (?:` +
	`gh api --method POST repos/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pulls/[0-9]+/reviews --input - --jq '\.id'` +
	`|ao review submit --session (?P<session>[A-Za-z0-9_-]+) --reviews -)$`)

// ReviewerSubmitCommandAllowed reports whether a reviewer's Bash command is one
// of the exact submit shapes the review prompt dictates. The `ao review submit`
// shape must name workerSessionID; an empty workerSessionID means a broken
// launch (the launcher always sets it), so nothing is admitted.
func ReviewerSubmitCommandAllowed(command, workerSessionID string) bool {
	m := reviewerSubmitCommandPattern.FindStringSubmatch(strings.TrimSpace(command))
	if m == nil || workerSessionID == "" {
		return false
	}
	session := m[reviewerSubmitCommandPattern.SubexpIndex("session")]
	return session == "" || session == workerSessionID
}
