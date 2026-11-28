package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestDetectTerminalActivity(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   domain.ActivityState
		ok     bool
	}{
		{
			name:   "idle composer",
			output: "\x1b[1m›\x1b[0m \x1b[2mWrite tests for @filename\x1b[0m\n\ngpt-5.6-sol low · ~/project\n",
			want:   domain.ActivityIdle,
			ok:     true,
		},
		{
			name:   "working composer",
			output: "• Working (2m 10s • esc to interrupt)\n› Add tests\n\ngpt-5.6-sol low · ~/project\n",
			want:   domain.ActivityActive,
			ok:     true,
		},
		{
			name:   "approval picker",
			output: "› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n",
			want:   domain.ActivityWaitingInput,
			ok:     true,
		},
		{
			name:   "assistant text",
			output: "The symbol is:\n›\nnot a composer footer\n",
		},
		{
			name:   "empty pane output",
			output: "",
		},
		{
			name:   "single line pane output",
			output: "› ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := (&Plugin{}).DetectTerminalActivity(tt.output)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DetectTerminalActivity() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func readCodexFixture(t *testing.T, name string) string {
	t.Helper()
	output, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

// Fixtures are codex-cli 0.146.0 TUI renders taken from the openai/codex
// rust-v0.146.0 insta snapshots (status_widget_and_approval_modal,
// network_exec_prompt, mcp_server_elicitation_approval_form_without_schema,
// request_user_input_footer_wrap, image_generation_begin_restores_working_status). Codex replaces the working
// status line with the approval modal, so exec_approval.txt is what a running
// turn looks like while an approval is pending.
func TestDetectTerminalActivityCapturedCodexFrames(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    domain.ActivityState
		ok      bool
	}{
		{"exec approval during a running turn", "exec_approval.txt", domain.ActivityWaitingInput, true},
		{"network approval", "network_approval.txt", domain.ActivityWaitingInput, true},
		{"resumed generation", "active_generation.txt", domain.ActivityActive, true},
		{"mcp elicitation form", "mcp_elicitation.txt", domain.ActivityWaitingInput, true},
		{"agent question with options", "user_input_question.txt", domain.ActivityWaitingInput, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := (&Plugin{}).DetectTerminalActivity(readCodexFixture(t, tt.fixture))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DetectTerminalActivity(%s) = (%q, %v), want (%q, %v)", tt.fixture, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// Codex styles the selected picker row, "›" included, bold (the accent style
// in rust-v0.146.0's selection_popup_common.rs). A capture taken before the
// picker's hint row is drawn, or in a viewport too short to show it, is not a
// recognized picker; the bold-marker fallback must not then read the selected
// option as the current prompt and report idle or active.
func TestDetectTerminalActivityFailsClosedOnStyledPartialPicker(t *testing.T) {
	styled := strings.Replace(readCodexFixture(t, "exec_approval.txt"), "› 1.", "\x1b[1m›\x1b[0m 1.", 1)
	partial := strings.Replace(styled, "Press enter to confirm or esc to cancel", "", 1)
	tests := []struct {
		name   string
		output string
	}{
		{"styled picker without its hint row", partial},
		{"styled picker without its hint row under a stale working line", strings.Replace(partial, "\x1b[1m›", "• Working (3s • esc to interrupt)\n\x1b[1m›", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := (&Plugin{}).DetectTerminalActivity(tt.output); ok {
				t.Fatalf("DetectTerminalActivity() = (%q, true), want no signal", got)
			}
		})
	}
	if got, ok := (&Plugin{}).DetectTerminalActivity(styled); got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("complete styled picker = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

// A live approval picker must read as waiting even with a working line above
// it: the picker is checked first, so an approval can never become active.
func TestDetectTerminalActivityPrefersApprovalPickerOverWorkingLine(t *testing.T) {
	output := "• Working (6m 13s • esc to interrupt)\n" + readCodexFixture(t, "exec_approval.txt")
	got, ok := (&Plugin{}).DetectTerminalActivity(output)
	if got != domain.ActivityWaitingInput || !ok {
		t.Fatalf("DetectTerminalActivity() = (%q, %v), want (%q, true)", got, ok, domain.ActivityWaitingInput)
	}
}

func TestInspectTerminalSurfaceSeparatesCodexWorkFromComposer(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantWork   ports.TerminalSurfaceWorkState
		wantEditor ports.TerminalComposerState
	}{
		{
			name:       "idle empty composer",
			output:     "\x1b[1m›\x1b[0m \x1b[2mWrite tests for @filename\x1b[0m\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "current Codex placeholder without dim styling",
			output:     "› Ask Codex to do anything\n\nGPT-6-Sol medium · ~/project\n? for shortcuts\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "text appended to Codex placeholder remains a draft",
			output:     "› Ask Codex to do anything else\n\nGPT-6-Sol medium · ~/project\n? for shortcuts\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerDraft,
		},
		{
			name:       "idle empty composer when constrained viewport hides footer",
			output:     "\x1b[2m• \x1b[0mE2E_ROUNDTRIP_TWO\n\n\n\x1b[1m›\x1b[0m\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "plain transcript prompt without footer is not current chrome",
			output:     "The response ended with an example:\n›\n",
			wantWork:   ports.TerminalSurfaceWorkUnknown,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "active empty composer when constrained viewport hides footer",
			output:     "\x1b[2m• Working (4s • esc to interrupt)\x1b[0m\n\n\x1b[1m›\x1b[0m\n",
			wantWork:   ports.TerminalSurfaceWorkActive,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name: "idle empty composer above a two-row footer",
			output: "\x1b[1m›\x1b[m \x1b[2mAsk Codex to do anything\x1b[m\n\n" +
				"  \x1b[38;2;246;226;183mGPT-6.1-Sol low\x1b[m · \x1b[38;2;171;223;167m~/project\x1b[m\n" +
				"  \x1b[1m←\x1b[m for agents · \x1b[1m?\x1b[m for shortcuts    ⚠ \x1b[38;2;196;167;103m3 warnings\x1b[m · \x1b[1mf2\x1b[m to view\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name: "idle draft above a two-row footer",
			output: "\x1b[1m›\x1b[m Keep this draft\n\n" +
				"  \x1b[38;2;246;226;183mGPT-6.1-Sol low\x1b[m · \x1b[38;2;171;223;167m~/project\x1b[m\n" +
				"  \x1b[1m?\x1b[m for shortcuts    ⚠ 3 warnings · \x1b[1mf2\x1b[m to view\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerDraft,
		},
		{
			name:       "idle draft",
			output:     "› Keep this draft\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerDraft,
		},
		{
			name:       "active wording inside draft is not current chrome",
			output:     "› Quote esc to interrupt here\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerDraft,
		},
		{
			name:       "active empty composer",
			output:     "• Working (2m 10s • esc to interrupt)\n› \x1b[2mAdd tests\x1b[0m\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkActive,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name: "old active row in transcript is not current chrome",
			output: "• Working (2m 10s • esc to interrupt)\nThe work finished.\n" +
				"› \x1b[2mAdd tests\x1b[0m\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "approval picker",
			output:     "› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n",
			wantWork:   ports.TerminalSurfaceWorkWaitingInput,
			wantEditor: ports.TerminalComposerUnknown,
		},
		{
			name: "approval picker with normal footer still visible",
			output: "Run this command?\n› 1. Approve once\n  2. Deny\n" +
				"Press enter to confirm or esc to go back\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkWaitingInput,
			wantEditor: ports.TerminalComposerUnknown,
		},
		{
			name:       "approval picker with a non-first selected option",
			output:     "Run this command?\n  1. Approve once\n› 2. Deny\nPress enter to confirm or esc to go back\n",
			wantWork:   ports.TerminalSurfaceWorkWaitingInput,
			wantEditor: ports.TerminalComposerUnknown,
		},
		{
			name: "completed approval picker above the current composer is idle",
			output: "Run this command?\n› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n" +
				"› \x1b[2mAdd tests\x1b[0m\n\ngpt-5.6-sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		// Codex v0.162 draws the current composer as "»" and keeps "›" for
		// submitted prompts and picker rows (frames captured from v0.162.1).
		{
			name: "v0.162 idle empty composer below a submitted prompt",
			output: "› Remember the code word FOXTROT for later.\n\n• OK\n\n" +
				"\x1b[1m\x1b[38;2;186;130;255m»\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m\n\n" +
				"  \x1b[38;2;246;226;183mGPT-6.1-Sol ultra fast\x1b[39m · \x1b[38;2;171;223;167m~/project\x1b[39m\n" +
				"  \x1b[1m?\x1b[0m for shortcuts    ⚠ 2 warnings · f2 to view\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name: "v0.162 idle draft",
			output: "\x1b[1m\x1b[38;2;186;130;255m»\x1b[0m Keep this draft\n\n" +
				"  \x1b[38;2;246;226;183mGPT-6.1-Sol ultra fast\x1b[39m · \x1b[38;2;171;223;167m~/project\x1b[39m\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerDraft,
		},
		{
			name:       "v0.162 active empty composer",
			output:     "• Working (2m 10s • esc to interrupt)\n» \x1b[2mAsk Codex to do anything\x1b[0m\n\nGPT-6.1-Sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkActive,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "v0.162 idle empty composer when constrained viewport hides footer",
			output:     "\x1b[2m• \x1b[0mE2E_ROUNDTRIP_TWO\n\n\n\x1b[1m»\x1b[0m\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
		{
			name:       "v0.162 approval picker",
			output:     "Run this command?\n› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n",
			wantWork:   ports.TerminalSurfaceWorkWaitingInput,
			wantEditor: ports.TerminalComposerUnknown,
		},
		{
			name: "v0.162 completed approval picker above the current composer is idle",
			output: "Run this command?\n› 1. Approve once\n  2. Deny\nPress enter to confirm or esc to go back\n" +
				"» \x1b[2mAsk Codex to do anything\x1b[0m\n\nGPT-6.1-Sol low · ~/project\n",
			wantWork:   ports.TerminalSurfaceWorkIdle,
			wantEditor: ports.TerminalComposerEmpty,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (&Plugin{}).InspectTerminalSurface(tt.output)
			if got.Work != tt.wantWork || got.Composer != tt.wantEditor {
				t.Fatalf("InspectTerminalSurface() = %+v, want work=%v composer=%v", got, tt.wantWork, tt.wantEditor)
			}
		})
	}
}

func TestInspectTerminalSurfaceOnlyProvesAnUnstartedConversationOnInitialFrame(t *testing.T) {
	header := "╭────────────────────────╮\n│ >_ OpenAI Codex (v0.147.0) │\n╰────────────────────────╯\n\nTip: Try the Desktop app.\n"
	footer := "\n\ngpt-5.6-sol low · ~/project\n"
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "initial empty composer",
			output: header + "\n\x1b[1m›\x1b[0m \x1b[2mSummarize recent commits\x1b[0m" + footer,
			want:   true,
		},
		{
			name:   "initial composer with unsent draft",
			output: header + "\n\x1b[1m›\x1b[0m Keep this draft" + footer,
			want:   true,
		},
		{
			name: "completed turn",
			output: header + "\n› Say hello\n\n• Hello\n\n" +
				"\x1b[1m›\x1b[0m \x1b[2mSummarize recent commits\x1b[0m" + footer,
		},
		{
			name:   "partial frame without provider header",
			output: "\x1b[1m›\x1b[0m \x1b[2mSummarize recent commits\x1b[0m" + footer,
		},
		{
			name:   "active turn",
			output: header + "\n• Working (4s • esc to interrupt)\n\x1b[1m›\x1b[0m" + footer,
		},
		{
			name:   "v0.162 initial empty composer",
			output: header + "\n\x1b[1m»\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m" + footer,
			want:   true,
		},
		{
			name:   "v0.162 completed turn",
			output: header + "\n› Say hello\n\n• Hello\n\n\x1b[1m»\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m" + footer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (&Plugin{}).InspectTerminalSurface(tt.output)
			if got.NativeConversationNotStarted != tt.want {
				t.Fatalf("NativeConversationNotStarted = %v, want %v; observation=%+v", got.NativeConversationNotStarted, tt.want, got)
			}
		})
	}
}

// Codex's PermissionRequest hook records waiting_input, and Codex installs no
// post-tool-use hook that could clear it before Stop. The adapter must opt into
// terminal reconciliation while waiting, or an approved turn stays needs_input.
func TestCodexReconcilesTerminalActivityWhileWaiting(t *testing.T) {
	detector, ok := any(&Plugin{}).(ports.WaitingTerminalActivityDetector)
	if !ok {
		t.Fatal("codex does not implement ports.WaitingTerminalActivityDetector")
	}
	if !detector.ContinuouslyDetectTerminalActivityWhileWaiting() {
		t.Fatal("ContinuouslyDetectTerminalActivityWhileWaiting() = false, want true")
	}
}

// Captured from Codex 0.161.0: with a warnings badge the hint row under the
// model row carries a " · " of its own. The dim placeholder must still read as
// an empty composer, or the session can never be switched away from Codex.
func TestInspectTerminalSurfaceIgnoresWarningsBadgeBelowComposer(t *testing.T) {
	screen := strings.Join([]string{
		"• The second codeword is KIWI-42.",
		"",
		"\x1b[1m›\x1b[m \x1b[2mAsk Codex to do anything\x1b[m",
		"",
		"  \x1b[38;2;246;226;183mGPT-6.1-Sol xhigh\x1b[m · \x1b[38;2;171;223;167m/tmp/worktrees/handoff/handoff-2\x1b[m · \x1b[38;2;137;180;250mContinue claude source\x1b[m",
		"  \x1b[1m?\x1b[m for shortcuts                    ⚠ \x1b[38;2;196;167;103m3 warnings\x1b[m · \x1b[1mf2\x1b[m to view",
	}, "\n")
	got := (&Plugin{}).InspectTerminalSurface(screen)
	if got.Composer != ports.TerminalComposerEmpty || got.Work != ports.TerminalSurfaceWorkIdle {
		t.Fatalf("observation = %+v, want an idle agent with an empty composer", got)
	}

	draft := strings.Replace(screen, "\x1b[2mAsk Codex to do anything\x1b[m", "fix the importer", 1)
	if got := (&Plugin{}).InspectTerminalSurface(draft); got.Composer != ports.TerminalComposerDraft {
		t.Fatalf("observation = %+v, want real composer text to remain a draft", got)
	}
}
