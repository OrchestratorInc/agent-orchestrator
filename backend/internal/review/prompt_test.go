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
		"Review every queued PR and record a result for each one",
		"Complete every review task in the queue autonomously",
		"Do not ask the user whether to continue to the next PR",
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1)",
		"* 2. https://github.com/o/r/pull/2 (head commit sha2, run run-2)",
		"Do these steps in order for each PR",
		"gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input - --jq '.id' <<'AO_REVIEW_EOF'\n{",
		"ao review submit --session mer-1 --run <run-id> --verdict <approved|changes_requested> --review-id <id-from-step-1> --body - <<'AO_REVIEW_EOF'\n<your full review markdown>\nAO_REVIEW_EOF\n",
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
	if !strings.Contains(prompt, "implementation pull request(s) owned by worker session mer-1") || !strings.Contains(system, "implementation PRs owned by an AO worker") {
		t.Fatalf("native review prompts must identify their owned implementation scope:\n%s\n%s", prompt, system)
	}
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

// Review text reaches the submit commands through a quoted heredoc, so the
// reviewer never shell-escapes it. Read-only sandboxes that cannot create a
// here-document temp file fall back to the printf pipe, and a failing submit
// is retried at most twice instead of looping.
func TestReviewPromptSubmitsThroughHeredocWithBoundedFallback(t *testing.T) {
	prompt, _ := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", RunID: "run-1"})
	for _, want := range []string{
		"Do not shell-escape anything inside it",
		"Put the closing AO_REVIEW_EOF alone on its own line with no indentation",
		"never write a line that is exactly AO_REVIEW_EOF inside the review text",
		"cannot create a temp file for the here document",
		"`printf '%s' '<the same text>' | <the same command without the heredoc>`",
		"replace every single quote in the text with '\\''",
		"If step 1 produced no review id, leave out `--review-id <id-from-step-1>` entirely",
		"Do not retry a failing submit command more than twice",
		"If step 2 still fails, stop and report the command's error output",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, stale := range []string{"do not use a heredoc", "Keep the JSON on one line", "--reviews -"} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("review prompt still contains %q:\n%s", stale, prompt)
		}
	}
}
