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
		"After every PR has its own GitHub review from step 1",
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

// The worker hears about a review only through the reviewer's inline GitHub
// comments, each forwarded as a required change. Required findings must be
// inline, and optional suggestions must not be.
func TestReviewPromptRequiresInlineCommentsForRequiredChanges(t *testing.T) {
	prompt, system := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", RunID: "run-1"})
	for _, want := range []string{
		"The worker receives only your inline comments, never the summary",
		"Put every finding that requires a change in \"comments\" as its own inline comment",
		"including design-level findings",
		"Leave optional or nice-to-have suggestions out of \"comments\"",
		"Omit \"comments\" only when nothing needs to change.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(system, "Every finding that requires a change must be its own inline comment") {
		t.Fatalf("reviewer system prompt must require inline findings:\n%s", system)
	}
	if strings.Contains(prompt, "omit the field for a review with no inline comments") {
		t.Fatalf("review prompt still makes inline comments optional:\n%s", prompt)
	}
}
