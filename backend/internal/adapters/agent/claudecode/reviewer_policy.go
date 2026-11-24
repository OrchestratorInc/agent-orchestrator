package claudecode

import (
	"regexp"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// ReviewerAllowedTools is the read-only tool allowlist an AO code reviewer runs
// with, in a terminal pane or a Chat. The reviewer runs headless (no human to
// approve prompts) but must stay read-only, so instead of bypassPermissions —
// which skips the permission system entirely and ignores allow/deny rules — it
// launches in a mode where these rules are honored: allow rules auto-approve
// without prompting, so the reviewer can read the checkout and run the few
// commands it needs (gh pr view/diff/checks to read PR metadata, and printf to
// pipe review JSON into the downstream commands without writing a worktree
// file). Every other call reaches AO, which answers it with
// ReviewerSubmitCommandAllowed (the PermissionRequest hook for a terminal pane,
// the ACP permission policy for a Chat) so the reviewer never stalls. Claude
// Code ≥ 2.1.257 also prompts on Bash commands its analyzer cannot verify
// statically even when an allow rule matches.
//
// Some commands are deliberately left off:
//   - git diff/log/show: their diff option --output=<file> writes a file in any
//     flag position (and abbreviated), so a prefix allow rule would admit
//     writes. Claude Code's own read-only check approves the plain reads in
//     default mode and prompts on --output, which AO then denies.
//   - `ao review submit`: a prefix rule would admit any --session and any
//     --reviews file, skipping the worker-session pin the matcher enforces.
//   - `gh api --method POST .../reviews`: a blanket Bash(gh:*) rule would admit
//     merges, closes, and arbitrary API mutations that a prefix deny list
//     cannot enumerate (flag spellings, flag order, graphql).
//
// Both submit commands therefore go through the matcher only.
var ReviewerAllowedTools = []string{
	"Read",
	"Grep",
	"Glob",
	"Bash(printf:*)",
	"Bash(gh pr view:*)",
	"Bash(gh pr diff:*)",
	"Bash(gh pr checks:*)",
	"Bash(git status:*)",
}

// ReviewerDisallowedTools hard-denies the write paths as defense in depth, so a
// misbehaving model cannot edit files or move the branch even if a future
// allowlist entry would otherwise admit it.
var ReviewerDisallowedTools = []string{
	// A reviewer needs no MCP tool, and the user's Claude config may allow one
	// that writes; an allow rule would approve it without reaching AO.
	"mcp__*",
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

// Both transports accept only the same two downstream commands.
const reviewerSubmitCommand = `(?:` + ports.ReviewPostCommandPattern +
	`|ao review submit --session (?P<session>[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*) ` + ports.ReviewRunArgsPattern + `)`

var reviewerSubmitPipe = regexp.MustCompile(`^printf '%s' ` + reviewerSubmitJSONLiteral + ` \| ` + reviewerSubmitCommand + `$`)
var reviewerSubmitHeredoc = regexp.MustCompile(`^` + reviewerSubmitCommand + ` <<'` + ports.ReviewSubmitHeredocDelimiter + `'\n(?P<body>(?s:.*\n)?)` + ports.ReviewSubmitHeredocDelimiter + `\n?$`)

// ReviewerSubmitCommandAllowed validates the whole shell command for both a
// reviewer pane and Chat. A missing worker id indicates a broken launch.
func ReviewerSubmitCommandAllowed(command, workerSessionID string) bool {
	if workerSessionID == "" {
		return false
	}
	command = strings.TrimSpace(command)
	if m := reviewerSubmitPipe.FindStringSubmatch(command); m != nil {
		session := m[reviewerSubmitPipe.SubexpIndex("session")]
		return session == "" || session == workerSessionID
	}
	m := reviewerSubmitHeredoc.FindStringSubmatch(command)
	if m == nil {
		return false
	}
	// An interior terminator would end the body before the validated end.
	for _, line := range strings.Split(m[reviewerSubmitHeredoc.SubexpIndex("body")], "\n") {
		if line == ports.ReviewSubmitHeredocDelimiter {
			return false
		}
	}
	session := m[reviewerSubmitHeredoc.SubexpIndex("session")]
	return session == "" || session == workerSessionID
}
