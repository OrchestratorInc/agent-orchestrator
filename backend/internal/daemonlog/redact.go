package daemonlog

import (
	"bytes"
	"os"
	"regexp"
	"sync"
)

const redacted = "[redacted]"

// redactions scrub credentials from a record before it reaches disk. They
// mirror the desktop app's main.log redaction (frontend/src/main/local-logs.ts)
// so both files follow one rule: keep URLs and messages readable, drop the
// secret parts. Stderr is left as is for development.
var redactions = []struct {
	re   *regexp.Regexp
	repl []byte
}{
	// Credentials embedded in a URL: scheme://user:pass@host.
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@"']+(?::[^/\s@"']*)?@`), []byte("${1}" + redacted + "@")},
	// Authorization headers, quoted or not, with or without a scheme word.
	{regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?(?:(?:bearer|token|basic)\s+)?)[^\s"',]+`), []byte("${1}" + redacted)},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[a-z0-9._~+/=-]+`), []byte("${1}" + redacted)},
	// Secret-bearing query parameters, including signed-URL signatures.
	{regexp.MustCompile(`(?i)([?&](?:api[_-]?key|key|token|access[_-]?token|refresh[_-]?token|id[_-]?token|secret|client[_-]?secret|password|auth|code|sig|signature|x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&\s"'#]+`), []byte("${1}" + redacted)},
	// key=value / key: value pairs whose key names a secret (slog text attrs,
	// env dumps, JSON). The key must end in the secret word, so counters such
	// as input_tokens=12 stay readable.
	{regexp.MustCompile(`(?i)(\b[a-z0-9_.-]*(?:api[_-]?key|token|secret|password|passwd)["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s"',}&]+)`), []byte("${1}" + redacted)},
	// Well-known token shapes that can appear anywhere in a message.
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`), []byte(redacted)},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`), []byte(redacted)},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{8,}`), []byte(redacted)},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+`), []byte(redacted)},
}

var homeDir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return ""
	}
	return home
})

// redact returns p with credentials removed and the user's home directory
// shortened to "~", so a log a user attaches to a report does not carry their
// account name.
func redact(p []byte) []byte {
	return redactWithHome(p, homeDir())
}

func redactWithHome(p []byte, home string) []byte {
	out := p
	for _, r := range redactions {
		out = r.re.ReplaceAll(out, r.repl)
	}
	if home != "" {
		out = bytes.ReplaceAll(out, []byte(home), []byte("~"))
	}
	return out
}
