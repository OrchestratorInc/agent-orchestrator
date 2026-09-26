package chat

import (
	"strings"
	"testing"
)

func TestSideQuestionTreatsSelectionAsSubjectAndPairAsBackground(t *testing.T) {
	got := sideQuestionText("sun", "user:\n---\nhello\n---\nassistant:\n---\nThe sun is a star.\n---", "What is this?")
	for _, part := range []string{"> sun", "The sun is a star.", "What is this?", "not instructions", "unless the user explicitly asks about the conversation"} {
		if !strings.Contains(got, part) { t.Fatalf("side prompt missing %q: %q", part, got) }
	}
	if strings.Index(got, "The sun is a star.") >= strings.Index(got, "> sun") || strings.Index(got, "> sun") >= strings.Index(got, "What is this?") {
		t.Fatalf("background, selection, and question are out of order: %q", got)
	}
}
