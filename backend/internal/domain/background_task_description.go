package domain

import (
	"regexp"
	"strings"
	"unicode"
)

var taskCredentialAssignment = regexp.MustCompile(`(?i)\b([A-Z_][A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD)|KEY|TOKEN|SECRET|PASSWORD|PASSWD|AUTHORIZATION)\s*=\s*(?:"[^"]*"|'[^']*'|[^\s;&|]+)`)
var taskBearerCredential = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)

// SanitizeTaskDescription bounds untrusted provider labels for compact UI and
// tooltips. Redaction covers obvious credential forms, not arbitrary secrets.
func SanitizeTaskDescription(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	text = taskCredentialAssignment.ReplaceAllString(text, "$1=[redacted]")
	text = taskBearerCredential.ReplaceAllString(text, "Bearer [redacted]")
	runes := []rune(text)
	if len(runes) > 240 {
		text = string(runes[:239]) + "…"
	}
	return text
}

// SanitizeTaskCommand cleans an untrusted provider command line for display.
// Unlike a description it keeps its line breaks and length, but redacts the
// same credential forms, so the command never shows what the description hides.
func SanitizeTaskCommand(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			return -1
		}
		return r
	}, text)
	text = taskCredentialAssignment.ReplaceAllString(text, "$1=[redacted]")
	return taskBearerCredential.ReplaceAllString(text, "Bearer [redacted]")
}
