package cline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func readClineFixture(t *testing.T, name string) string {
	t.Helper()
	output, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestDetectTerminalActivityClineFrames(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    domain.ActivityState
	}{
		{"completed turn at empty composer", "idle_composer.txt", domain.ActivityIdle},
		{"active generation", "active_generation.txt", domain.ActivityActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := (&Plugin{}).DetectTerminalActivity(readClineFixture(t, tt.fixture))
			if got != tt.want || !ok {
				t.Fatalf("DetectTerminalActivity(%s) = (%q, %v), want (%q, true)", tt.fixture, got, ok, tt.want)
			}
		})
	}
}

func TestDetectTerminalActivityUsesNewestMarker(t *testing.T) {
	idle := readClineFixture(t, "idle_composer.txt")
	active := readClineFixture(t, "active_generation.txt")
	tests := []struct {
		name   string
		output string
		want   domain.ActivityState
	}{
		{"generation after old composer", idle + "\n" + active, domain.ActivityActive},
		{"composer after old generation", active + "\n" + idle, domain.ActivityIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := (&Plugin{}).DetectTerminalActivity(tt.output)
			if got != tt.want || !ok {
				t.Fatalf("DetectTerminalActivity() = (%q, %v), want (%q, true)", got, ok, tt.want)
			}
		})
	}
}

func TestDetectTerminalActivityRejectsTranscriptText(t *testing.T) {
	output := "The docs call the placeholder Ask anything... and the toggle Plan / Act (Tab).\n"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if ok {
		t.Fatalf("DetectTerminalActivity(transcript) = (%q, true), want no signal", got)
	}
}

func TestDetectTerminalActivityReportsToolApproval(t *testing.T) {
	got, ok := (&Plugin{}).DetectTerminalActivity(readClineFixture(t, "tool_approval.txt"))
	if got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("DetectTerminalActivity(tool approval) = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

func TestDetectTerminalActivityApprovalNewestMarkerWins(t *testing.T) {
	idle := readClineFixture(t, "idle_composer.txt")
	active := readClineFixture(t, "active_generation.txt")
	approval := readClineFixture(t, "tool_approval.txt")
	tests := []struct {
		name   string
		output string
		want   domain.ActivityState
	}{
		{"approval after old composer", idle + "\n" + approval, domain.ActivityWaitingInput},
		{"composer after old approval", approval + "\n" + idle, domain.ActivityIdle},
		{"generation after old approval", approval + "\n" + active, domain.ActivityActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := (&Plugin{}).DetectTerminalActivity(tt.output)
			if got != tt.want || !ok {
				t.Fatalf("DetectTerminalActivity() = (%q, %v), want (%q, true)", got, ok, tt.want)
			}
		})
	}
}

func TestDetectTerminalActivityIgnoresApprovalFooterAlone(t *testing.T) {
	// The "Auto-approve all disabled" footer also shows during normal work in
	// manual-approval mode; without the approve/deny dialog it must not read
	// as waiting.
	output := "❯ Ask anything...\nClaude Opus 5 (medium) $0.05  ○ Plan ● Act (Tab)\n⏵⏵ Auto-approve all disabled (Shift+Tab)\n"
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityIdle || !ok {
		t.Fatalf("DetectTerminalActivity(footer only) = (%q, %v), want (%q, true)", got, ok, domain.ActivityIdle)
	}
}

func TestDetectTerminalActivityIgnoresApprovalTextWithoutControls(t *testing.T) {
	// Only the dialog's own structure counts. Assistant/tool output that quotes
	// the question, or prose mentioning both answers, has no dialog controls and
	// must not become authoritative waiting_input while a tool is running.
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "quoted question",
			output: "I'll run the focused adapter tests.\nThe prompt says Approve tool call?\n",
		},
		{
			name:   "approve and deny prose",
			output: "I'll approve the change and deny the request if the tests fail.\nRunning go test ./...\n",
		},
		{
			name:   "question without controls",
			output: "Approve tool call?\nrun_commands\n  $ go test ./internal/adapters/agent/cline\n",
		},
		{
			name:   "controls without question",
			output: "  [y] approve  [n] deny\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := (&Plugin{}).DetectTerminalActivity(tt.output); ok {
				t.Fatalf("DetectTerminalActivity() = (%q, true), want no signal", got)
			}
		})
	}
}

func TestDeriveActivityStateTreatsPreToolUseAsActive(t *testing.T) {
	// Regression test for #6414: PreToolUse fires after approval (upstream
	// cline/cline#7446) or with no prompt under auto-approve, so it must
	// derive active — never sticky waiting_input.
	for _, event := range []string{"pre-tool-use", "permission-request", "post-tool-use", "permission-resolved"} {
		got, ok := DeriveActivityState(event, []byte(`{}`))
		if got != domain.ActivityActive || !ok {
			t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, true)", event, got, ok, domain.ActivityActive)
		}
	}
}

func TestClinePreToolUseHookResolvesApproval(t *testing.T) {
	// PreToolUse must resolve, not just report active: the terminal detector can
	// already have written waiting_input from the dialog, and lifecycle
	// precedence suppresses plain tool events against that sticky state. Only a
	// turn-boundary event (permission-resolved) clears it; the full sequence is
	// covered in lifecycle's TestToolPrecedence_ClinePreToolUseClearsObservedApproval.
	for _, spec := range clineManagedHooks {
		if spec.Event != "PreToolUse" {
			continue
		}
		if spec.Subcommand != "permission-resolved" {
			t.Fatalf("PreToolUse subcommand = %q, want %q", spec.Subcommand, "permission-resolved")
		}
		state, ok := DeriveActivityState(spec.Subcommand, nil)
		if state != domain.ActivityActive || !ok {
			t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, true)", spec.Subcommand, state, ok, domain.ActivityActive)
		}
		return
	}
	t.Fatal("missing managed PreToolUse hook")
}

func TestClineManagedHooksClearCompletedTurns(t *testing.T) {
	want := map[string]string{
		"TaskComplete": "stop",
	}
	for _, spec := range clineManagedHooks {
		if subcommand, ok := want[spec.Event]; ok {
			if spec.Subcommand != subcommand {
				t.Fatalf("%s subcommand = %q, want %q", spec.Event, spec.Subcommand, subcommand)
			}
			delete(want, spec.Event)
		}
	}
	for event := range want {
		t.Errorf("missing managed %s hook", event)
	}
}
