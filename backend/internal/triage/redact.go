package triage

import (
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const redacted = "[redacted]"

var (
	knownCredential = regexp.MustCompile(`\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]{12,}|glpat-[A-Za-z0-9_-]{12,}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[A-Za-z0-9_-]{20,}|AKIA[A-Z0-9]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
	authValue       = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[^\s,;"'<>]+`)
	credentialValue = regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_]*_)?(?:api[_-]?key|(?:secret[_-]?)?access[_-]?key(?:[_-]?id)?|access[_-]?token|refresh[_-]?token|token|password|passwd|client[_-]?secret|secret|authorization|cookie|set-cookie|private[_-]?key)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
	privateHome     = regexp.MustCompile(`(?i)(?:/Users/[^/\s"'<>]+|/home/[^/\s"'<>]+|[a-z]:[\\/]Users[\\/][^\\/\s"'<>]+|~[/\\])[^\s"'<>]*`)
	urlValue        = regexp.MustCompile("(?i)\\b[a-z][a-z0-9+.-]*://[^\\s<>\"'`]+")
	privateKeyBlock = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )?PRIVATE KEY-----.*?(?:-----END (?:[A-Z0-9]+ )?PRIVATE KEY-----|$)`)
	keySeparators   = regexp.MustCompile(`[^a-z0-9]`)
)

// Redact removes known credential patterns and private local locations. It is
// a best-effort filter, not a guarantee that arbitrary text contains no secrets.
func Redact(text string) string {
	text = privateKeyBlock.ReplaceAllString(text, redacted)
	text = urlValue.ReplaceAllStringFunc(text, redactURL)
	text = knownCredential.ReplaceAllString(text, redacted)
	text = authValue.ReplaceAllString(text, "$1 "+redacted)
	text = credentialValue.ReplaceAllString(text, "${1}"+redacted)
	text = privateHome.ReplaceAllString(text, "[private-path]")
	// Account for an explicitly configured home outside the common OS paths.
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 && strings.Contains(text, home) {
		if pathRE, err := regexp.Compile(regexp.QuoteMeta(home) + `(?:[/\\][^\s"'<>]*)?`); err == nil {
			text = pathRE.ReplaceAllString(text, "[private-path]")
		}
	}
	return text
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return redacted
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	ip := net.ParseIP(strings.Split(host, "%")[0])
	if u.Scheme == "file" || !strings.Contains(host, ".") && ip == nil || strings.HasSuffix(host, ".localhost") || host == "localhost.localdomain" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".internal") || host == "0.0.0.0" || ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return "[local-url]"
	}
	if u.User != nil {
		u.User = url.User(redacted)
	}
	query := u.Query()
	changed := u.User != nil
	for key := range query {
		if credentialKey(key) {
			query.Set(key, redacted)
			changed = true
		}
	}
	if changed {
		u.RawQuery = query.Encode()
		return u.String()
	}
	return raw
}

func credentialKey(key string) bool {
	key = keySeparators.ReplaceAllString(strings.ToLower(key), "")
	for _, secret := range []string{"apikey", "accesskey", "accesstoken", "refreshtoken", "password", "passwd", "secret", "authorization", "cookie", "privatekey", "credential"} {
		if strings.Contains(key, secret) {
			return true
		}
	}
	return key == "token" || strings.HasSuffix(key, "token") || key == "secret" || strings.HasSuffix(key, "secret")
}

// Values have already crossed decodeJSON, so only JSON types reach this walk.
func redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return Redact(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			if credentialKey(key) {
				out[Redact(key)] = redacted
			} else {
				out[Redact(key)] = redactValue(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = redactValue(val)
		}
		return out
	}
	return value
}
