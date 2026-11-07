package hermes

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// These rows are styled screens captured from Hermes v0.22.5 in its default
// modern layout through AO's pty-host, trimmed to the transcript's last reply.

const modernTranscript = "\x1b[38;5;230mReply to: earlier task\x1b[m\n\x1b[38;5;214m" + nativeTranscriptRule + "\x1b[m\n"

func modernRule(width int) string {
	return "\x1b[38;5;102m" + strings.Repeat("─", width) + "\x1b[m"
}

// modernTopRule is the composer's top rule with the session title at its end.
func modernTopRule(width int, title string) string {
	return "\x1b[38;5;102m" + strings.Repeat("─", width-len(title)-3) + " \x1b[38;5;178m" + title + "\x1b[38;5;102m ─\x1b[m"
}

// modernBusyRule is the top rule while a turn runs: spinner, activity, time.
func modernBusyRule(width int, frame, activity, title string) string {
	left := "── " + frame + " " + activity + " · 5s "
	fill := width - len([]rune(left)) - len(title) - 3
	return "\x1b[38;5;102m── \x1b[38;5;214;1m" + frame + " \x1b[m" + activity + "\x1b[38;5;101m · 5s\x1b[38;5;102m " +
		strings.Repeat("─", fill) + " \x1b[38;5;178m" + title + "\x1b[38;5;102m ─\x1b[m"
}

const (
	modernIdlePrompt = "\x1b[38;5;230m❯ \x1b[38;5;136;3mAsk anything, or type / for commands…\x1b[m"
	modernDraft      = "\x1b[38;5;230m❯ \x1b[mhalf typed draft"
	modernBusyPrompt = "\x1b[38;5;136;3m☤ ❯ type to redirect this turn · Tab queues it for after\x1b[m"
	// Below 64 columns the busy row shows the state icon without ❯.
	modernCompactBusyRow = "\x1b[38;5;136;3m☤ type to redirect this turn · Tab queues it for after\x1b[m"
	modernIdleFooter     = "\x1b[38;5;250m ☤ \x1b[38;5;220;1mfake-model\x1b[38;5;101;22m · 13/128K · \x1b[38;5;108;1m[░░░░░░░░░░]\x1b[38;5;101;22m \x1b[38;5;108;1m0%\x1b[38;5;101;22m · ◷ 25.0s · ↑ 0 t/s · 58s · ⏲ 25s · ✓ 11s\x1b[m"
	modernHintFooter     = "\x1b[38;5;250m ☤ \x1b[38;5;220;1mfake-model\x1b[38;5;101;22m · ctx -- · [░░░░░░░░░░] -- · 10s · ⏲ 0s · …/repo)\x1b[m  \x1b[38;5;220;1m/\x1b[38;5;101;22m commands · \x1b[38;5;220;1m@\x1b[38;5;101;22m files · \x1b[38;5;220;1m!\x1b[38;5;101;22m shell\x1b[m"
	modernBusyFooter     = "\x1b[38;5;250m ☤ \x1b[38;5;220;1mfake-model\x1b[38;5;101;22m · 0/128K · \x1b[38;5;108;1m[░░░░░░░░░░]\x1b[38;5;101;22m \x1b[38;5;108;1m0%\x1b[38;5;101;22m · 26s · ⏱ 5s\x1b[m               \x1b[38;5;220;1menter\x1b[38;5;101;22m interrupt · \x1b[38;5;220;1mtab\x1b[38;5;101;22m queue · \x1b[38;5;220;1mctrl+c\x1b[38;5;101;22m stop\x1b[m"
	modernCompactFooter  = "\x1b[38;5;250m ☤ \x1b[38;5;220;1mfake-model\x1b[38;5;101;22m · \x1b[38;5;108;1m0%\x1b[38;5;101;22m · 1m · …/repo)\x1b[m"
)

func TestModernLayoutComposerActivity(t *testing.T) {
	for _, tt := range []struct {
		name  string
		rows  []string
		want  domain.ActivityState
		sends bool
	}{
		{"wide idle", []string{modernTopRule(110, "Lab title 2"), modernIdlePrompt, modernRule(110), modernIdleFooter}, domain.ActivityIdle, true},
		{"wide idle with key hints", []string{modernTopRule(110, "Lab title 2"), modernIdlePrompt, modernRule(110), modernHintFooter}, domain.ActivityIdle, true},
		{"wide idle before a title", []string{modernRule(110), modernIdlePrompt, modernRule(110), modernIdleFooter}, domain.ActivityIdle, true},
		{"wide idle with a wide title character", []string{modernTopRule(109, "計画"), modernIdlePrompt, modernRule(110), modernIdleFooter}, domain.ActivityIdle, true},
		{"wide busy", []string{modernBusyRule(110, "⠸", "( •_•)>⌐■-■ processing...", "Lab title 2"), modernBusyPrompt, modernRule(110), modernBusyFooter}, domain.ActivityActive, false},
		{"wide busy with typed guidance", []string{modernBusyRule(110, "⠴", "Working…", "Lab title 2"), modernDraft, modernRule(110), modernBusyFooter}, domain.ActivityActive, false},
		{"wide draft", []string{modernTopRule(110, "Lab title 2"), modernDraft, modernRule(110), modernIdleFooter}, domain.ActivityBlocked, false},
		{"compact idle", []string{"", modernTopRule(60, "Lab title 2"), modernIdlePrompt, modernCompactFooter}, domain.ActivityIdle, true},
		{"compact busy", []string{modernBusyRule(60, "⠋", "(⌐■_■) musing...", "Lab title 2"), modernCompactBusyRow, modernCompactFooter}, domain.ActivityActive, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			screen := modernTranscript + strings.Join(tt.rows, "\n") + "\n"
			if got, ok := New().DetectTerminalActivity(screen); !ok || got != tt.want {
				t.Fatalf("DetectTerminalActivity = %s/%v, want %s", got, ok, tt.want)
			}
			if err := New().ValidateTerminalMessage("task", screen); (err == nil) != tt.sends {
				t.Fatalf("message admission = %v, want allowed %v", err, tt.sends)
			}
		})
	}
}

// The footer line is chrome, not a busy row, only when it starts one column in.
func TestModernFooterDoesNotHideCompactBusyIcon(t *testing.T) {
	rule := strings.Repeat("─", 40)
	if got, ok := New().DetectTerminalActivity("transcript\n" + rule + "\n☤ msg=interrupt · /queue · Ctrl+C cancel"); !ok || got != domain.ActivityActive {
		t.Fatalf("compact busy icon = %s/%v, want active", got, ok)
	}
}
