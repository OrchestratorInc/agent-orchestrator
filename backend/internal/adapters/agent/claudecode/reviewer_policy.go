package claudecode

import (
	"regexp"
	"strings"
)

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

var ReviewerDisallowedTools = []string{
	"mcp__*",
	"Edit",
	"Write",
	"NotebookEdit",
	"Bash(git push:*)",
	"Bash(git commit:*)",
	"Bash(gh pr merge:*)",
	"AskUserQuestion",
}

const reviewerSubmitJSONLiteral = `'[^']*(?:(?:'\\''|'"'"')[^']*)*'`

var reviewerSubmitCommandPattern = regexp.MustCompile(`^printf '%s' ` + reviewerSubmitJSONLiteral + ` \| (?:` +
	`gh api --method POST repos/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pulls/[0-9]+/reviews --input - --jq '\.id'` +
	`|ao review submit --session (?P<session>[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*) --reviews -)$`)

func ReviewerSubmitCommandAllowed(command, workerSessionID string) bool {
	m := reviewerSubmitCommandPattern.FindStringSubmatch(strings.TrimSpace(command))
	if m == nil || workerSessionID == "" {
		return false
	}
	session := m[reviewerSubmitCommandPattern.SubexpIndex("session")]
	return session == "" || session == workerSessionID
}
