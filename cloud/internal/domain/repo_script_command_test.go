package domain

import "testing"

func TestRepoScriptCommandRoundTrips(t *testing.T) {
	for _, command := range []string{"make dev", "docker compose up -d\n# done", "echo ')'\n"} {
		stored := RepoScriptCommand("voice-calling-ai", command)
		dir, inner, ok := ParseRepoScriptCommand(stored)
		if !ok || dir != "voice-calling-ai" || inner != command {
			t.Fatalf("ParseRepoScriptCommand(%q) = %q %q %v", stored, dir, inner, ok)
		}
	}
	if _, inner, ok := ParseRepoScriptCommand("make dev"); ok || inner != "make dev" {
		t.Fatalf("a main repository step parsed as a repository step")
	}
	step := ProjectScriptStep{Command: "make dev", Repository: "https://github.com/acme/voice-calling-ai"}
	if got := step.ShellCommand("https://github.com/acme/alice"); got != RepoScriptCommand("voice-calling-ai", "make dev") {
		t.Fatalf("ShellCommand = %q", got)
	}
}
