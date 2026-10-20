package cli

import (
	"regexp"
	"strings"
)

const triageSensitiveFieldPattern = `(?:client_?secret|refresh_?token|access_?token|id_?token|private_?key|pairing[_ -]?(?:code|password)|session[_ -]?secret|[A-Z0-9_-]*(?:TOKEN|SECRET|PASSWORD|API[_-]?KEY)[A-Z0-9_-]*|passwd|authorization|auth)`
const triageQuotedValue = `(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`

var triageRedactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/?#]+@`), `${1}***@`},
	{regexp.MustCompile(`(?i)([?&]` + triageSensitiveFieldPattern + `=)(?:` + triageQuotedValue + `|[^&\s#)]+)`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?im)^.*\\"` + triageSensitiveFieldPattern + `\\"\s*:.*$`), `[redacted-secret]`},
	{regexp.MustCompile(`(?i)((?:"` + triageSensitiveFieldPattern + `"|'` + triageSensitiveFieldPattern + `')\s*:\s*)(?:` + triageQuotedValue + `|[^\s,}\]]+)`), `${1}"[redacted-secret]"`},
	{regexp.MustCompile(`(?i)(\bauthorization\s*:\s*)[^\r\n]+`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?i)((?:\b|--)` + triageSensitiveFieldPattern + `\s*(?:[:=]\s*|\s+))(?:` + triageQuotedValue + `|[^\s"';&)},\]]+)`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?i)(\b(?:pairing(?:[_ -]?code)?|code)\s*[:= -]?\s*)[0-9]{6,8}\b`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]+`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+`), `[redacted-secret]`},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`), `[redacted-secret]`},
	{regexp.MustCompile(`(?i)(?:\b[a-z]:[/\\]+users[/\\]+|/(?:users|home)/)[^/\\\s"'<>),;]+`), `~`},
	{regexp.MustCompile(`(?i)\$HOME\b`), `~`},
}

func redactTriage(text, home string) string {
	if home != "" {
		for _, path := range []string{strings.ReplaceAll(home, `\`, `\\`), home, strings.ReplaceAll(home, `\`, "/")} {
			text = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(path)).ReplaceAllLiteralString(text, "~")
		}
	}
	for _, r := range triageRedactions {
		text = r.pattern.ReplaceAllString(text, r.replacement)
	}
	return text
}
