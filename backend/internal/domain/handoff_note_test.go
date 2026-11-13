package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHandoffNoteFitsAWindowsCommandLine(t *testing.T) {
	if limit := handoffNoteCharLimit("windows"); limit*2 > 32_767 {
		t.Fatalf("Windows note limit %d leaves no room on a 32,767-unit command line", limit)
	}
	saved := handoffNoteMaxChars
	handoffNoteMaxChars = handoffNoteCharLimit("windows")
	t.Cleanup(func() { handoffNoteMaxChars = saved })
	note := BuildHandoffNote(HandoffNoteInput{Turns: []HandoffTurn{{UserRequest: strings.Repeat("a", 40_000)}}})
	if got := utf8.RuneCountInString(note); got > handoffNoteMaxChars {
		t.Fatalf("note is %d characters, want at most %d", got, handoffNoteMaxChars)
	}
}

// The desktop's note names the agents, keeps every field, and points at
// ao session history for the whole conversation.
func TestBuildHandoffNoteCarriesTheSessionAndPointsAtItsHistory(t *testing.T) {
	note := BuildHandoffNote(HandoffNoteInput{
		SwitchID: "switch-1", SourceHarness: HarnessClaudeCode, TargetHarness: HarnessCodex,
		SessionTitle: "Importer", Branch: "feature/importer",
		Artifacts: []SessionArtifactFile{{Path: "plan.md", Size: 3}}, ArtifactDir: "/data/a",
		HistoryFiles: []HandoffHistoryFile{{Harness: HarnessKimi, Path: "/h/kimi.jsonl", Read: "Read the last lines."}},
		Turns:        []HandoffTurn{{UserRequest: "ship it", AgentResponse: "shipped", Harness: HarnessClaudeCode}},
	})
	for _, want := range []string{
		HandoffNotePrefix + ` switch-id="switch-1">`, "You are codex, taking over this Orchestrator-managed session from claude-code.",
		"Title: Importer", "Git branch: feature/importer", "- plan.md (3 bytes", "- kimi: `/h/kimi.jsonl`. Read the last lines.",
		"Assistant (claude-code):\nshipped", "## Earlier history\nRun `ao session history` to read the conversation in full",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note is missing %q:\n%s", want, note)
		}
	}
}
