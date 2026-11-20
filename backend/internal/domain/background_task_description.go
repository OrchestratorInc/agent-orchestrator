package domain

import (
	"regexp"
	"strings"
	"unicode"
)

const taskRedactedNamePattern = `(?:[A-Z_][A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS|PWD|CREDENTIAL)|KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS|PWD|CREDENTIAL|AUTHORIZATION)`
const taskRedactedValuePattern = `(?:"[^"]*"|'[^']*'|[^\s;&|"']+)`

var taskWindowsSetCredential = regexp.MustCompile(`(?i)(\bset\s+")(` + taskRedactedNamePattern + `)\s*=\s*[^"]*(")`)
var taskCredentialAssignment = regexp.MustCompile(`(?i)\b(` + taskRedactedNamePattern + `)\s*=\s*(?:(?:Bearer|Basic|token)\s+)?` + taskRedactedValuePattern)
var taskCredentialFlag = regexp.MustCompile(`(?i)(--(?:(?:api|access|auth|client|private|secret|encryption|signing|refresh|bearer)[-_])?(?:key|token|secret|password|passwd|pass|pwd|credential)\b(?:(?:\s*=\s*)|\s+))` + taskRedactedValuePattern)
var taskBasicOrTokenCredential = regexp.MustCompile(`(?i)(\bAuthorization\s*:\s*(?:Basic|token)\s+)[A-Za-z0-9._~+/=-]+`)
var taskBearerCredential = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
var taskURLCredential = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@?#:]*:[^/\s@?#]*@`)
var taskUserCredential = regexp.MustCompile(`(?i)((?:^|\s)(?:-u|--user)(?:\s+|=))(?:"[^"]*:[^"]*"|'[^']*:[^']*'|[^\s;&|"']*:[^\s;&|"']*)`)

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
	text = taskWindowsSetCredential.ReplaceAllString(text, "$1$2=[redacted]$3")
	text = taskCredentialAssignment.ReplaceAllString(text, "$1=[redacted]")
	text = taskCredentialFlag.ReplaceAllString(text, "${1}[redacted]")
	text = taskBasicOrTokenCredential.ReplaceAllString(text, "${1}[redacted]")
	text = taskBearerCredential.ReplaceAllString(text, "Bearer [redacted]")
	text = taskURLCredential.ReplaceAllString(text, "${1}[redacted]@")
	text = taskUserCredential.ReplaceAllString(text, "${1}[redacted]")
	runes := []rune(text)
	if len(runes) > 240 {
		text = string(runes[:239]) + "…"
	}
	return text
}
