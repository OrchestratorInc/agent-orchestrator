package domain

import (
	"strings"
	"testing"
)

func TestSanitizeControlChars(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text unchanged", in: "hello world", want: "hello world"},
		{name: "keeps newline tab carriage return", in: "a\nb\tc\rd", want: "a\nb\tc\rd"},
		{name: "strips ansi escape byte leaving harmless residue", in: "before\x1b[2Jafter", want: "before[2Jafter"},
		{name: "strips nul and bell", in: "x\x00y\az", want: "xyz"},
		{name: "strips osc sequence bytes", in: "\x1b]0;title\a", want: "]0;title"},
		{name: "empty stays empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeControlChars(tt.in); got != tt.want {
				t.Fatalf("SanitizeControlChars(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeCardSummary(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"collapses whitespace", "  Fixing \n the   bug ", "Fixing the bug"},
		{"strips escapes", "Fixing\x1b[2J it", "Fixing[2J it"},
		{"strips bidi override", "Reading \u202etxt.exe", "Reading txt.exe"},
		{"strips zero width", "Fix\u200bing", "Fixing"},
		{"strips line separator", "one\u2028two", "onetwo"},
		{"too long", strings.Repeat("a", MaxCardSummaryRunes+1), ""},
		{"empty", "  ", ""},
	}
	for _, tt := range tests {
		if got := NormalizeCardSummary(tt.in); got != tt.want {
			t.Errorf("%s: NormalizeCardSummary(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}
