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
		"Review every queued PR, submitting each result with its own run id",
		"Complete every review task in the queue autonomously",
		"Do not ask the user whether to continue to the next PR",
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1)",
		"* 2. https://github.com/o/r/pull/2 (head commit sha2, run run-2)",
		"ao review submit",
		"--body -",
		"--comment-path",
		"one command per queued PR",
		"Never post to GitHub yourself",
		"Never use a heredoc",
		"single-line finding",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, legacy := range []string{"gh api", "--reviews -", "githubReviewId"} {
		if strings.Contains(prompt, legacy) {
			t.Fatalf("prompt still references legacy contract %q:\n%s", legacy, prompt)
		}
	}
}

func TestReviewSystemPromptOverridesInheritedHygieneRules(t *testing.T) {
	system := reviewSystemPrompt()
	for _, want := range []string{
		"there is no human in this session to approve anything",
		"those rules do not apply in this sandbox",
		"emit it exactly as the task shows",
		"it is the only channel that can deliver the review",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
}

// The worker hears about a review only through the reviewer's inline GitHub
// comments, each forwarded as a required change. Required findings must be
// inline, and optional suggestions must not be.
func TestReviewPromptRequiresInlineCommentsForRequiredChanges(t *testing.T) {
	prompt, system := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", RunID: "run-1"})
	if !strings.Contains(prompt, "implementation pull request(s) owned by worker session mer-1") || !strings.Contains(system, "implementation PRs owned by an AO worker") {
		t.Fatalf("native review prompts must identify their owned implementation scope:\n%s\n%s", prompt, system)
	}
	for _, want := range []string{
		"The worker receives only your inline findings, never the summary",
		"Put every finding that requires a change in an inline finding",
		"including design-level findings",
		"Leave optional or nice-to-have suggestions out of the inline findings",
		"Omit the `--comment-*` flags entirely when nothing needs to change.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(system, "Every finding that requires a change must be its own inline finding") {
		t.Fatalf("reviewer system prompt must require inline findings:\n%s", system)
	}
	if strings.Contains(prompt, "omit the field for a review with no inline comments") {
		t.Fatalf("review prompt still makes inline comments optional:\n%s", prompt)
	}
}
