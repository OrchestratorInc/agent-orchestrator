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
	{regexp.MustCompile(`(?im)^.*\\+"` + triageSensitiveFieldPattern + `\\+"\s*:.*$`), `[redacted-secret]`},
	{regexp.MustCompile(`(?i)((?:"` + triageSensitiveFieldPattern + `"|'` + triageSensitiveFieldPattern + `')\s*:\s*)(?:` + triageQuotedValue + `|[^\s,}\]]+)`), `${1}"[redacted-secret]"`},
	{regexp.MustCompile(`(?i)(\bauthorization\s*:\s*)[^\r\n]+`), `${1}[redacted-secret]`},
	// Values that open with backslashes and a quote are handled by redactEscapedAssignments.
	{regexp.MustCompile(`(?i)((?:\b|--)` + triageSensitiveFieldPattern + `\s*(?:[:=]\s*|\s+))(?:` + triageQuotedValue + `|(?:\\+[^\s"\\';&)},\]]|[^\s"\\';&)},\]])[^\s"';&)},\]]*|\\+(\s|[;&)},\]]|$))`), `${1}[redacted-secret]${2}`},
	{regexp.MustCompile(`(?i)(\b(?:pairing(?:[_ -]?code)?|code)\s*[:= -]?\s*)[0-9]{6,8}\b`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]+`), `${1}[redacted-secret]`},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+`), `[redacted-secret]`},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`), `[redacted-secret]`},
	{regexp.MustCompile(`(?i)(?:\b[a-z]:[/\\]+users[/\\]+|/(?:users|home)/)[^/\\\s"'<>),;]+`), `~`},
	{regexp.MustCompile(`(?i)\$HOME\b`), `~`},
}

var triageEscapedAssignment = regexp.MustCompile(`(?i)(?:\b|--)` + triageSensitiveFieldPattern + `\\*["']?\s*(?:[:=]\s*|\s+)(\\+["'])`)

// redactEscapedAssignments redacts quoted values nested inside an already
// quoted string, such as an error logged by slog. The value closes at the
// same escape depth it opened with, which a regexp cannot match. An
// unclosed value is redacted to the end of the line.
func redactEscapedAssignments(text string) string {
	var out strings.Builder
	for {
		m := triageEscapedAssignment.FindStringSubmatchIndex(text)
		if m == nil {
			break
		}
		closing := text[m[2]:m[3]]
		lineEnd := strings.IndexByte(text[m[1]:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += m[1]
		}
		end, rest := lineEnd, ""
		for i := m[1]; i < lineEnd; {
			j := strings.Index(text[i:lineEnd], closing)
			if j < 0 {
				break
			}
			if j += i; text[j-1] != '\\' {
				end, rest = j+len(closing), closing
				break
			}
			i = j + 1
		}
		out.WriteString(text[:m[1]] + "[redacted-secret]" + rest)
		text = text[end:]
	}
	return out.String() + text
}

func redactTriage(text, home string) string {
	if home != "" {
		for _, path := range []string{strings.ReplaceAll(home, `\`, `\\`), home, strings.ReplaceAll(home, `\`, "/")} {
			text = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(path)).ReplaceAllLiteralString(text, "~")
		}
	}
	text = redactEscapedAssignments(text)
	for _, r := range triageRedactions {
		text = r.pattern.ReplaceAllString(text, r.replacement)
	}
	return text
}
