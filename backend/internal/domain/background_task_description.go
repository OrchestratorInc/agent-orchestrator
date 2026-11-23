package domain

import (
	"regexp"
	"strings"
	"unicode"
)

const taskRedactedNamePattern = `(?:[A-Z_][A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS|PWD|CREDENTIAL)|KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS|PWD|CREDENTIAL|AUTHORIZATION)`
const taskRedactedValuePattern = `(?:"[^"]*"|'[^']*'|[^\s;&|"']+)`

// Matches stay on one line: a command keeps its line breaks, and a bare
// TOKEN= must not swallow the next line. Descriptions are already one line.
var taskWindowsSetCredential = regexp.MustCompile(`(?i)(\bset[ \t]+")(` + taskRedactedNamePattern + `)[ \t]*=[ \t]*[^"\n]*(")`)
var taskCredentialAssignment = regexp.MustCompile(`(?i)(--[a-z0-9_-]+|\b` + taskRedactedNamePattern + `)[ \t]*=[ \t]*(?:(?:Bearer|Basic|token)[ \t]+)?` + taskRedactedValuePattern)
var taskCredentialFlag = regexp.MustCompile(`(?i)(--[a-z0-9_-]*(?:key|token|secret|password|passwd|pass|pwd|credential)\b)([ \t]*=[ \t]*|[ \t]+)` + taskRedactedValuePattern)
var taskBasicOrTokenCredential = regexp.MustCompile(`(?i)(\bAuthorization[ \t]*:[ \t]*(?:Basic|token)[ \t]+)[A-Za-z0-9._~+/=-]+`)
var taskBearerCredential = regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]+`)
var taskURLCredential = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@?#]+@`)
var taskUserCredential = regexp.MustCompile(`(?i)((?:^|\s)(?:-u|--user)(?:[ \t]+|=))(?:"[^"]*:[^"]*"|'[^']*:[^']*'|[^\s;&|"']*:[^\s;&|"']*)`)

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
	text = redactTaskCredentials(strings.Join(strings.Fields(text), " "))
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
		// Joiners stay, as in output, since emoji and some scripts need them.
		if (unicode.Is(unicode.Cf, r) && r != '\u200c' && r != '\u200d') || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			return -1
		}
		return r
	}, text)
	return redactTaskCredentials(text)
}

// redactTaskCredentials covers obvious credential forms, not arbitrary secrets.
func redactTaskCredentials(text string) string {
	text = taskWindowsSetCredential.ReplaceAllString(text, "$1$2=[redacted]$3")
	text = taskCredentialFlag.ReplaceAllStringFunc(text, func(match string) string {
		parts := taskCredentialFlag.FindStringSubmatch(match)
		switch strings.ToLower(parts[1]) {
		case "--sort-key", "--primary-key", "--bypass", "--monkey":
			return match
		}
		return parts[1] + parts[2] + "[redacted]"
	})
	text = taskCredentialAssignment.ReplaceAllStringFunc(text, func(match string) string {
		name := taskCredentialAssignment.FindStringSubmatch(match)[1]
		// Flags were handled above. Match their whole assignment here so an
		// exempt --sort-key=name cannot be mistaken for the variable KEY=name.
		if strings.HasPrefix(name, "--") && !strings.EqualFold(name, "--authorization") {
			return match
		}
		return name + "=[redacted]"
	})
	text = taskBasicOrTokenCredential.ReplaceAllString(text, "${1}[redacted]")
	text = taskBearerCredential.ReplaceAllString(text, "Bearer [redacted]")
	text = taskURLCredential.ReplaceAllString(text, "${1}[redacted]@")
	return taskUserCredential.ReplaceAllString(text, "${1}[redacted]")
}
