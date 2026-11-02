package chat

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestExtractAOSummary(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		want     string
		wantDone bool
	}{
		{"double quoted", `ao summary "Fixing the login redirect"`, "Fixing the login redirect", false},
		{"single quoted", `ao summary 'Fixing the login redirect'`, "Fixing the login redirect", false},
		{"unquoted words", `ao summary Fixing the login redirect`, "Fixing the login redirect", false},
		{"redirect and pipe after the text", `ao summary "Cloning pi-mono and inspecting its architecture" 2>&1 | tail -2; cat notes`, "Cloning pi-mono and inspecting its architecture", false},
		{"chained command after the text", `ao summary "Inspecting the GPUI scaffold" && cd /Users/me/.ao/dev/summaries`, "Inspecting the GPUI scaffold", false},
		{"semicolon after the text", `ao summary "Reading the config"; echo done`, "Reading the config", false},
		{"escaped quote", `ao summary "Renaming the \"main\" branch"`, `Renaming the "main" branch`, false},
		{"apostrophe inside double quotes", `ao summary "Fixing the user's profile page"`, "Fixing the user's profile page", false},
		{"leading whitespace", "  ao summary \"Adding tests\"  ", "Adding tests", false},
		{"multibyte within the character limit", `ao summary "` + strings.Repeat("修", domain.MaxCardSummaryRunes) + `"`, strings.Repeat("修", domain.MaxCardSummaryRunes), false},
		{"over the character limit", `ao summary "` + strings.Repeat("修", domain.MaxCardSummaryRunes+1) + `"`, "", false},
		{"unterminated quote", `ao summary "Fixing the login`, "", false},
		{"no text", `ao summary`, "", false},
		{"only a redirect", `ao summary > /dev/null`, "", false},
		{"final summary", `ao summary --done "Fixed the login redirect"`, "Fixed the login redirect", true},
		{"final summary flag last", `ao summary "Fixed the login redirect" --done`, "Fixed the login redirect", true},
		{"help flag", `ao summary --help`, "", false},
		{"other flag beside the text", `ao summary -v "Fixing the login"`, "", false},
		{"variable in double quotes", `ao summary "Fixing $TARGET"`, "", false},
		{"unquoted variable", `ao summary Fixing $TARGET`, "", false},
		{"backticks in double quotes", "ao summary \"Fixing the `render` path\"", "", false},
		{"variable in single quotes", `ao summary 'Fixing $TARGET'`, "Fixing $TARGET", false},
		{"escaped dollar", `ao summary "Saving \$5 a month"`, "Saving $5 a month", false},
		{"different command", `ao summarize "Fixing the login"`, "", false},
		{"not ao", `echo ao summary "Fixing the login"`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, done := extractAOSummary(tt.cmd); got != tt.want || done != tt.wantDone {
				t.Fatalf("extractAOSummary(%q) = %q, %v; want %q, %v", tt.cmd, got, done, tt.want, tt.wantDone)
			}
		})
	}
}
