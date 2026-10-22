package chat

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestExtractAOSummary(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"double quoted", `ao summary "Fixing the login redirect"`, "Fixing the login redirect"},
		{"single quoted", `ao summary 'Fixing the login redirect'`, "Fixing the login redirect"},
		{"unquoted words", `ao summary Fixing the login redirect`, "Fixing the login redirect"},
		{"redirect and pipe after the text", `ao summary "Cloning pi-mono and inspecting its architecture" 2>&1 | tail -2; cat notes`, "Cloning pi-mono and inspecting its architecture"},
		{"chained command after the text", `ao summary "Inspecting the GPUI scaffold" && cd /Users/me/.ao/dev/summaries`, "Inspecting the GPUI scaffold"},
		{"semicolon after the text", `ao summary "Reading the config"; echo done`, "Reading the config"},
		{"escaped quote", `ao summary "Renaming the \"main\" branch"`, `Renaming the "main" branch`},
		{"apostrophe inside double quotes", `ao summary "Fixing the user's profile page"`, "Fixing the user's profile page"},
		{"leading whitespace", "  ao summary \"Adding tests\"  ", "Adding tests"},
		{"multibyte within the character limit", `ao summary "` + strings.Repeat("修", domain.MaxCardSummaryRunes) + `"`, strings.Repeat("修", domain.MaxCardSummaryRunes)},
		{"over the character limit", `ao summary "` + strings.Repeat("修", domain.MaxCardSummaryRunes+1) + `"`, ""},
		{"unterminated quote", `ao summary "Fixing the login`, ""},
		{"no text", `ao summary`, ""},
		{"only a redirect", `ao summary > /dev/null`, ""},
		{"different command", `ao summarize "Fixing the login"`, ""},
		{"not ao", `echo ao summary "Fixing the login"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAOSummary(tt.cmd); got != tt.want {
				t.Fatalf("extractAOSummary(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}
