package kiro

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityStateTreatsPreToolUseAsActive(t *testing.T) {
	for _, event := range []string{"pre-tool-use", "permission-request", "post-tool-use", "permission-resolved"} {
		got, ok := DeriveActivityState(event, []byte(`{}`))
		if got != domain.ActivityActive || !ok {
			t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, true)", event, got, ok, domain.ActivityActive)
		}
	}
}

func TestKiroPreToolUseHookMapsToActiveSignal(t *testing.T) {
	for _, spec := range kiroManagedHooks {
		if spec.Event == "preToolUse" && spec.Command != kiroHookCommandPrefix+"pre-tool-use" {
			t.Fatalf("preToolUse command = %q, want %q", spec.Command, kiroHookCommandPrefix+"pre-tool-use")
		}
	}
}

func TestDetectTerminalActivityReportsApproval(t *testing.T) {
	output := "shell requires approval\n❯ Yes, single permission\n  Trust, always allow in this session\n  No\n"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("DetectTerminalActivity(approval) = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

func TestDetectTerminalActivityReportsClassicApproval(t *testing.T) {
	output := "Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]:"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("DetectTerminalActivity(classic) = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

func TestDetectTerminalActivityRejectsTrustFlagText(t *testing.T) {
	output := "kiro-cli chat --trust-all-tools\nAllows the model to use any tool without confirmation\n"
	if got, ok := (&Plugin{}).DetectTerminalActivity(output); ok {
		t.Fatalf("DetectTerminalActivity(trust help) = (%q, true), want no signal", got)
	}
}

func TestDetectTerminalActivityRejectsNonPrompts(t *testing.T) {
	// Neither line carries approval controls: the first is a status line,
	// the second a trust-state note. Neither may read as waiting.
	for _, output := range []string{
		"No approval is required.\n",
		"shell not trusted\n",
	} {
		if got, ok := (&Plugin{}).DetectTerminalActivity(output); ok {
			t.Fatalf("DetectTerminalActivity(%q) = (%q, true), want no signal", output, got)
		}
	}
}

func TestDetectTerminalActivitySkipsAnsweredPrompt(t *testing.T) {
	// The echoed answer proves the decision was already made: an answered
	// prompt alone must not re-arm waiting_input.
	output := "Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]: y\n"
	if got, ok := (&Plugin{}).DetectTerminalActivity(output); ok {
		t.Fatalf("DetectTerminalActivity(answered) = (%q, true), want no signal", got)
	}
}

func TestDetectTerminalActivitySkipsSeparateLineAnswer(t *testing.T) {
	// The classic prompt prints newlines before reading the answer, so the
	// choice lands on its own later line. An answered prompt must not
	// re-arm waiting_input even though the marker line itself is bare.
	output := "Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]:\n\n[ao] > y\n"
	if got, ok := (&Plugin{}).DetectTerminalActivity(output); ok {
		t.Fatalf("DetectTerminalActivity(separate-line answer) = (%q, true), want no signal", got)
	}
}

func TestDetectTerminalActivitySeparateLineAnswerStaysIdle(t *testing.T) {
	// Finished turn with the separate-line answer layout: idle wins and the
	// answered prompt below it never flips the session back to waiting.
	output := "Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]:\n\n[ao] > y\n" +
		"✓ Tool ran successfully\n" +
		"ask a question or describe a task\n"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityIdle || !ok {
		t.Fatalf("DetectTerminalActivity(finished turn, separate-line answer) = (%q, %v), want (%q, true)", got, ok, domain.ActivityIdle)
	}
}

func TestDetectTerminalActivityHandlesNoTabToEdit(t *testing.T) {
	// Captured Kiro 2.4.1 dialog shape: the deny control carries a key hint.
	// Replaying the pending dialog must still report waiting_input.
	output := "shell requires approval\n  Yes, single permission\n  Trust, always allow in this session\n  No (Tab to edit)\n"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("DetectTerminalActivity(No Tab to edit) = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

func TestDetectTerminalActivityFinishedTurnStaysIdle(t *testing.T) {
	// A finished turn keeps its idle composer even though scrollback retains
	// the answered prompt and an older approval bar: nothing below the
	// answered decision may flip the session back to waiting_input.
	output := "shell requires approval\n❯ Yes, single permission\n  Trust, always allow in this session\n  No\n" +
		"Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]: y\n" +
		"✓ Tool ran successfully\n" +
		"ask a question or describe a task\n"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityIdle || !ok {
		t.Fatalf("DetectTerminalActivity(finished turn) = (%q, %v), want (%q, true)", got, ok, domain.ActivityIdle)
	}
}

func TestDetectTerminalActivityAnsweredPromptShadowsOlderBar(t *testing.T) {
	// Without an idle marker, an answered prompt still proves the older bar
	// is history: the scan stops instead of matching stale UI.
	output := "shell requires approval\n❯ Yes, single permission\n  Trust, always allow in this session\n  No\n" +
		"Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]: n\n"
	if got, ok := (&Plugin{}).DetectTerminalActivity(output); ok {
		t.Fatalf("DetectTerminalActivity(answered over old bar) = (%q, true), want no signal", got)
	}
}

func TestDetectTerminalActivityApprovalNewestMarkerWins(t *testing.T) {
	approval := "shell requires approval\n❯ Yes, single permission\n  Trust, always allow in this session\n  No\n"
	idle := "ask a question or describe a task\n"
	if got, ok := (&Plugin{}).DetectTerminalActivity(idle + "\n" + approval); got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("approval after idle = (%q, %v), want waiting_input", got, ok)
	}
	if got, ok := (&Plugin{}).DetectTerminalActivity(approval + "\n" + idle); got != domain.ActivityIdle || !ok {
		t.Fatalf("idle after approval = (%q, %v), want idle", got, ok)
	}
}

func TestRemoveKiroLegacyPreToolUse(t *testing.T) {
	entries := []kiroHookEntry{
		{Command: kiroHookCommandPrefix + "permission-request"},
		{Command: "custom hook"},
	}
	kept := removeKiroLegacyPreToolUse(entries, "preToolUse")
	if len(kept) != 1 || kept[0].Command != "custom hook" {
		t.Fatalf("kept = %#v, want only custom hook", kept)
	}
	if got := removeKiroLegacyPreToolUse(entries, "stop"); len(got) != 2 {
		t.Fatalf("non-preToolUse event stripped entries: %#v", got)
	}
}
