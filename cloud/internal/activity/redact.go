package activity

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// redactedMarker replaces anything that looks like a credential.
const redactedMarker = "[REDACTED]"

type redaction struct {
	pattern     *regexp.Regexp
	replacement string
}

var redactions = []redaction{
	// Whole header values: "Authorization: Worker abc", "Cookie: a=b; c=d".
	{regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|cookie|set-cookie)(\s*[:=]\s*)[^\n]*`), "${1}${2}" + redactedMarker},
	// Bearer and basic credentials anywhere in text.
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), "${1} " + redactedMarker},
	// key=value and "key": "value" pairs whose key names a secret. A value
	// already masked is matched exactly, so masking twice keeps what follows
	// it ("password": [REDACTED]}). A double-quoted value is read past its
	// backslash escapes, so a JSON string holding JSON (an AWS Secrets
	// Manager SecretString, "{\"password\":\"x\"}") is masked whole rather
	// than up to its first escaped quote; one whose escapes do not close it
	// is masked up to its next quote.
	{regexp.MustCompile(`(?i)("?(?:[a-z0-9_-]*(?:token|secret|password|passwd|api[_-]?key|private[_-]?key|session[_-]?key|credential)[a-z0-9_-]*)"?\s*[:=]\s*)(\[REDACTED\]|"(?:[^"\\]|\\.)*"|"[^"]*"|'[^']*'|[^\s,;&"']+)`), "${1}" + redactedMarker},
	// URL userinfo: scheme://user:password@host.
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), "${1}" + redactedMarker + "@"},
	// Well-known token shapes.
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,}|sk_(?:live|test)_[A-Za-z0-9]{10,}|whsec_[A-Za-z0-9]{10,}|ao_local_[A-Za-z0-9_-]{8,}|AKIA[0-9A-Z]{16})\b`), redactedMarker},
	// JSON Web Tokens.
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), redactedMarker},
	// PEM private key blocks, including a truncated one.
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), redactedMarker},
}

// Redact masks credentials in free text: authorization headers, secret-named
// key/value pairs, URL passwords and well-known token formats. It is applied
// to every string that reaches the activity trail or a startup-error detail,
// on top of callers never passing secrets in the first place.
func Redact(text string) string {
	if text == "" {
		return text
	}
	for _, rule := range redactions {
		text = rule.pattern.ReplaceAllString(text, rule.replacement)
	}
	return text
}

// Excerpt returns a redacted excerpt of text no longer than limit bytes. When
// text is longer it keeps the tail, where a failing command prints its error,
// and marks the cut. The result is always valid UTF-8 with control characters
// other than newline and tab removed.
func Excerpt(text string, limit int) string {
	text = Redact(cleanControl(text))
	if limit <= 0 || len(text) <= limit {
		return text
	}
	const marker = "...\n"
	if limit <= len(marker) {
		return strings.ToValidUTF8(text[len(text)-limit:], "")
	}
	tail := text[len(text)-(limit-len(marker)):]
	// Drop a split leading rune.
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return marker + tail
}

// StripControl returns text as valid UTF-8 without terminal escape sequences
// or control characters other than newline and tab, and without redacting
// it: for callers that redact with their own patterns first (Redact masks a
// quoted value it cannot fully read, which can hide the rest of a pair from a
// later pass).
func StripControl(text string) string {
	return cleanControl(text)
}

// Truncate returns text cut to at most limit bytes, keeping the beginning (for
// structured text whose first lines matter most) on a UTF-8 boundary. It does
// not redact; callers pass already-redacted parts.
func Truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	const marker = "\n..."
	if limit <= len(marker) {
		return strings.ToValidUTF8(text[:limit], "")
	}
	// A rune split by the cut becomes an invalid sequence, which is dropped.
	return strings.ToValidUTF8(text[:limit-len(marker)], "") + marker
}

// TailLines returns the last n non-empty lines of text, redacted and bounded
// to limit bytes.
func TailLines(lines []string, n, limit int) string {
	kept := make([]string, 0, n)
	for index := len(lines) - 1; index >= 0 && len(kept) < n; index-- {
		line := strings.TrimRight(lines[index], "\r\n ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return Excerpt(strings.Join(kept, "\n"), limit)
}

func cleanControl(text string) string {
	text = strings.ToValidUTF8(text, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return -1
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, stripANSI(text))
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

func stripANSI(text string) string {
	if !strings.Contains(text, "\x1b") {
		return text
	}
	return ansiPattern.ReplaceAllString(text, "")
}
