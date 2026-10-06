package review

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewTextsIncludesMultiPRQueue(t *testing.T) {
	spec := launchSpec()
	spec.RunID = "run-2"
	spec.PRURL = "https://github.com/o/r/pull/2"
	spec.TargetSHA = "sha2"
	spec.ReviewIndex = 1
	spec.ReviewQueue = []ports.ReviewTask{
		{RunID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
		{RunID: "run-2", PRURL: "https://github.com/o/r/pull/2", TargetSHA: "sha2"},
	}

	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"AO created 2 review tasks",
		"Review every queued PR, then submit all results together",
		"Complete every review task in the queue autonomously",
		"Do not ask the user whether to continue to the next PR",
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1)",
		"* 2. https://github.com/o/r/pull/2 (head commit sha2, run run-2)",
		"Review every PR below, then record all results with one command",
		"printf '%s'",
		"do not use a heredoc",
		"ao review submit --session mer-1 --reviews -",
		`"reviews": [`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// AO's reviewer files every required change as a finding in AO and never posts
// to the PR itself: AO posts one summary comment and delivers the findings, so
// no provider review thread exists for the worker's replies to re-enter (#6300).
func TestReviewPromptFilesRequiredChangesAsAOFindings(t *testing.T) {
	prompt, system := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", RunID: "run-1"})
	for _, want := range []string{
		`"findings": [ { "path": "<file>", "line": <n>, "body": "<finding>" } ]`,
		"Every change the worker must make is its own entry in \"findings\", including design-level findings",
		"Leave optional or nice-to-have suggestions out of \"findings\"",
		"\"changes_requested\" needs at least one finding. \"approved\" has no \"findings\".",
		"Do not post, reply, or resolve anything on the PR yourself.",
		"ao review submit --session mer-1 --reviews -",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, banned := range []string{"gh api", "githubReviewId", "inline comment", "gh pr review"} {
		if strings.Contains(prompt, banned) || strings.Contains(system, banned) {
			t.Fatalf("reviewer texts still mention %q:\nprompt:\n%s\nsystem:\n%s", banned, prompt, system)
		}
	}
	if !strings.Contains(system, "Every change the worker must make must be its own finding") {
		t.Fatalf("reviewer system prompt must require findings:\n%s", system)
	}
	if !strings.Contains(system, "Never post, reply, or resolve anything on the pull request yourself") {
		t.Fatalf("reviewer system prompt must forbid provider writes:\n%s", system)
	}
}
