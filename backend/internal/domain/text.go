package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SanitizeControlChars removes control characters that are unsafe to deliver
// into a live terminal pane, while preserving the whitespace that legitimate
// multi-line text relies on (newline, carriage return, tab).
//
// Any text that reaches an agent's PTY must pass through here. The session
// runtime pastes messages straight into the live pane, so an unfiltered escape
// sequence (cursor control, screen clear, OSC) embedded in attacker-influenced
// content — a GitHub reviewer comment, a CI job log tail — would be interpreted
// by the terminal instead of read as plain text. Both the HTTP send endpoint
// and the lifecycle nudge path share this one definition so neither can drift
// into delivering raw control bytes.
func SanitizeControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

// MaxCardSummaryRunes bounds the one-line summary an agent sets on its card.
const MaxCardSummaryRunes = 200

// NormalizeCardSummary returns s as a single clean line, or "" when nothing
// usable is left or it exceeds MaxCardSummaryRunes. The CLI, HTTP endpoint and
// event stream all go through it, so the same text is accepted or rejected
// however the agent delivered it.
func NormalizeCardSummary(s string) string {
	// Bidi overrides and zero-width characters can make a card read differently
	// from what it says, so they go too.
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, SanitizeControlChars(s))
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxCardSummaryRunes {
		return ""
	}
	return s
}
