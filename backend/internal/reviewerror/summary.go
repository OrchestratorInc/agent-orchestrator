// Package reviewerror limits provider failures exposed as review run bodies.
// It is not a general-purpose secret detector or a replacement for protecting
// the original conversation error and provider archives.
package reviewerror

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxBytes bounds the summary, not its JSON or terminal-escaped encoding.
	MaxBytes      = 1024
	maxInputBytes = 16 * 1024
	// Fallback is the existing explanation for a turn with no usable failure.
	Fallback = "reviewer Chat turn ended without submitting a result"
	withheld = "provider error details withheld"
)

// Prefer withholding suspicious messages to guessing where a credential ends.
// These cover labeled credentials, URLs, request/diagnostic dumps and common
// token formats. Arbitrary unlabeled secrets in otherwise normal prose cannot
// be reliably distinguished from model names or other useful error text.
var unsafeMessage = regexp.MustCompile(`(?i)(authorization|proxy-authorization|bearer\s|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|token\s*[:=]|password|passwd|secret|cookie|credential|https?://|[a-z][a-z0-9+.-]*://|www\.|request\s*(body|headers?|payload|[:=])|response\s*(body|headers?|payload|[:=])|diagnostics?\s*[:=]|(headers?|payload|prompt|debug)\s*[:=]|additional[ _-]?details|stack\s*trace|-----BEGIN|\b(sk-|gh[pousr]_|github_pat_|AKIA)[a-z0-9_-]+|[a-z0-9_+/=-]{40,}|[{}\[\]\\])`)

// Eligible excludes successful and submitted results, even if their status is
// later changed to failed. Callers retain ownership/settlement guards as well.
func Eligible(status, verdict, githubReviewID string) bool {
	return status == "failed" && verdict == "" && githubReviewID == ""
}

// Summary selects only status, a known error classification and message from
// JSON envelopes; unknown fields are never rendered. Plain provider prose is
// supported because Pi has already discarded structured metadata upstream.
// Malformed/oversized input is not copied as a prefix. The function is intended
// to be idempotent so old bodies can be filtered at API and CLI boundaries too.
func Summary(raw string) string {
	if len(raw) > maxInputBytes || !utf8.ValidString(raw) {
		return Fallback
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Fallback
	}
	message, kind, status := raw, "", 0
	payload := strings.TrimPrefix(raw, "Codex error: ")
	if strings.HasPrefix(payload, "{") {
		var envelope struct {
			Status  int    `json:"status"`
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			return Fallback
		}
		status, kind, message = envelope.Status, envelope.Type, envelope.Message
		if envelope.Error != nil {
			kind, message = envelope.Error.Type, envelope.Error.Message
		}
	} else if payload != raw || strings.HasPrefix(raw, "[") || raw == "null" || strings.HasPrefix(raw, `"`) {
		return Fallback
	}
	// Normalize accepted whitespace before matching labels, so tabs and Unicode
	// spaces cannot turn into a newly sensitive label on a second pass. Check
	// forbidden controls on the original message rather than erasing them.
	unsafeControls := strings.ContainsFunc(message, func(r rune) bool {
		return unicode.IsControl(r) && !unicode.IsSpace(r)
	})
	message = strings.Join(strings.Fields(message), " ")
	if unsafeControls || unsafeMessage.MatchString(message) {
		message = withheld
	}
	if message == "" {
		message = Fallback
	}
	// Selected prose can resemble an input wrapper on the next API/CLI pass.
	// Disambiguate it without introducing a trusted/sanitization-bypass marker.
	if strings.HasPrefix(message, `"`) || message == "null" || strings.HasPrefix(message, "Codex error: ") {
		message = "Provider error: " + message
	}
	prefix := ""
	if status >= 100 && status <= 599 {
		prefix = fmt.Sprintf("HTTP %d: ", status)
	}
	// Classifications are identifiers, not arbitrary provider text. Unknown
	// ones are omitted rather than becoming another channel for raw payloads.
	switch kind {
	case "invalid_request_error", "authentication_error", "permission_error", "not_found_error", "rate_limit_error", "quota_exceeded", "insufficient_quota", "server_error", "api_error", "overloaded_error":
		prefix += kind + ": "
	}
	return bound(prefix + message)
}

func bound(s string) string {
	if len(s) <= MaxBytes {
		return s
	}
	const marker = "…"
	end := MaxBytes - len(marker)
	for !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + marker
}
