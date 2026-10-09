package main

import (
	"os"
	"testing"
)

func TestDisableInteractivePromptsSetsDefaults(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	os.Unsetenv("GIT_TERMINAL_PROMPT")
	t.Setenv("GCM_INTERACTIVE", "")
	os.Unsetenv("GCM_INTERACTIVE")

	disableInteractivePrompts()

	if got := os.Getenv("GIT_TERMINAL_PROMPT"); got != "0" {
		t.Fatalf("GIT_TERMINAL_PROMPT = %q, want 0", got)
	}
	if got := os.Getenv("GCM_INTERACTIVE"); got != "never" {
		t.Fatalf("GCM_INTERACTIVE = %q, want never", got)
	}
}

func TestDisableInteractivePromptsKeepsExplicitValues(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "1")

	disableInteractivePrompts()

	if got := os.Getenv("GIT_TERMINAL_PROMPT"); got != "1" {
		t.Fatalf("GIT_TERMINAL_PROMPT = %q, want the explicit 1", got)
	}
}
