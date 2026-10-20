package cli

import (
	"regexp"
	"strings"
)

const triageSecretKey = `(?:api[_-]?key|token|secret|password|passwd|authorization|access[_-]?token|refresh[_-]?token|auth)`
const triageQuotedValue = `(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`

var triageRedactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/?#]+@`), `${1}***@`},
	{regexp.MustCompile(`(?i)([?&]` + triageSecretKey + `=)(?:` + triageQuotedValue + `|[^&\s#)]+)`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?i)((?:"` + triageSecretKey + `"|'` + triageSecretKey + `')\s*:\s*)(?:` + triageQuotedValue + `|[^\s,}\]]+)`), `${1}"[redacted-secret]"`},
	{regexp.MustCompile(`(?i)(\bauthorization\s*:\s*)[^\r\n]+`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?i)(\b(?:pairing[_ -]?(?:code|password)|[A-Z0-9_-]*` + triageSecretKey + `[A-Z0-9_-]*)\s*[:=]\s*)(?:` + triageQuotedValue + `|[^\s"';&)]+)`), `${1}[redacted-secret]`},
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
