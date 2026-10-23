package domain

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ConversationFailureKind names why a provider refused a turn, in terms of what
// the user can do about it.
type ConversationFailureKind string

const (
	// FailureOutOfCredit means the provider account has no money or prepaid quota.
	FailureOutOfCredit ConversationFailureKind = "outOfCredit"
	// FailureAuth means the provider rejected the credential.
	FailureAuth ConversationFailureKind = "auth"
	// FailureRateLimited means the provider asked AO to slow down.
	FailureRateLimited ConversationFailureKind = "rateLimited"
	// FailureOverloaded means the provider is temporarily unable to serve.
	FailureOverloaded ConversationFailureKind = "overloaded"
	// FailureContextTooLong means the conversation no longer fits the model.
	FailureContextTooLong ConversationFailureKind = "contextTooLong"
	// FailureModelUnavailable means the selected model cannot be used.
	FailureModelUnavailable ConversationFailureKind = "modelUnavailable"
	// FailureNetwork means the request never reached the provider.
	FailureNetwork ConversationFailureKind = "network"
)

// ConversationFailure is a recognized provider failure. Detail is the provider's
// own sentence with transport wrapping removed; it is empty when the raw message
// was already plain text.
type ConversationFailure struct {
	Kind   ConversationFailureKind `json:"kind"`
	Detail string                  `json:"detail,omitempty"`
}

// Harnesses that report a structured error name are trusted before any text.
var failureByErrorName = map[string]ConversationFailureKind{
	"provider.quota": FailureOutOfCredit,
	"provider.auth":  FailureAuth,
}

// Ordered: an OpenAI "insufficient_quota" arrives as a 429, so credit must win
// over rate limiting, and an overloaded 529 must not read as a network fault.
var failurePatterns = []struct {
	kind    ConversationFailureKind
	pattern *regexp.Regexp
}{
	{FailureOutOfCredit, regexp.MustCompile(`(?i)insufficient[ _](?:account[ _])?(?:funds|credits?|balance|quota)|credit balance is too low|out of credits?|no credits? (?:left|remaining)|payment required|billing (?:hard )?limit|` + httpStatus("402"))},
	{FailureAuth, regexp.MustCompile(`(?i)invalid[ _](?:x-)?api[ _-]?key|incorrect api key|api key (?:is )?(?:invalid|missing|not valid|expired)|authentication[ _](?:failed|error|required)|unauthori[sz]ed|` + httpStatus("401") + `|invalid[ _]token|token (?:has )?expired`)},
	{FailureContextTooLong, regexp.MustCompile(`(?i)context[ _](?:length|window)[ _]exceeded|maximum context length|prompt is too long|exceeds the (?:model'?s? )?context|context window|too many (?:input )?tokens|input is too long`)},
	{FailureModelUnavailable, regexp.MustCompile(`(?i)model[ _]not[ _]found|model .{0,80}(?:does not exist|not found|is not (?:available|supported))|unknown model|unsupported model|do(?:es)? not have access to (?:the )?model`)},
	{FailureRateLimited, regexp.MustCompile(`(?i)rate[ _-]?limit|too many requests|` + httpStatus("429"))},
	{FailureOverloaded, regexp.MustCompile(`(?i)overloaded|` + httpStatus("529") + `|` + httpStatus("503") + `|service unavailable|temporarily unavailable|server is busy|at capacity`)},
	{FailureNetwork, regexp.MustCompile(`(?i)ECONNREFUSED|ECONNRESET|ETIMEDOUT|ENOTFOUND|EAI_AGAIN|fetch failed|socket hang up|connection (?:refused|reset|error)|network error|unable to connect|(?:request|response|headers?) timed out`)},
}

// httpStatus matches a status code only where it reads as one, so a port, line
// number, or token count containing the digits does not.
func httpStatus(code string) string {
	return `(?:^|\b(?:status|code|HTTP)[ :=]*|\()` + code + `\b`
}

// ClassifyConversationFailure recognizes provider failures a user can act on.
// Unknown failures return nil so the raw message still speaks for itself.
func ClassifyConversationFailure(message string) *ConversationFailure {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	detail, errorName := unwrapFailureMessage(message)
	kind, ok := failureByErrorName[errorName]
	if !ok {
		for _, candidate := range failurePatterns {
			if candidate.pattern.MatchString(message) {
				kind, ok = candidate.kind, true
				break
			}
		}
	}
	if !ok {
		return nil
	}
	if detail == message {
		detail = ""
	}
	return &ConversationFailure{Kind: kind, Detail: detail}
}

type wrappedFailure struct {
	Message string `json:"message"`
	Data    struct {
		ProviderData struct {
			ErrorName string `json:"errorName"`
		} `json:"providerData"`
	} `json:"data"`
}

// Transport framing around a provider's sentence: leading wrappers and the
// trailing endpoint, trace, and request ids a user cannot act on.
var (
	failurePrefix  = regexp.MustCompile(`(?i)^(?:internal error:|upstream request failed:|stream disconnected before completion:|unexpected status \d{3}(?: [A-Za-z ]+)?:)\s*`)
	failureTrailer = regexp.MustCompile(`(?i),?\s*(?:url: \S+|cf-ray: \S+|request[ _]id: \S+)(?:,.*)?$|\s*\(request[ _]id: [^)]*\)|\s*For more information, pass .verbose: true. .*$`)
	failureSpaces  = regexp.MustCompile(`\s{2,}`)
)

// unwrapFailureMessage peels a JSON-RPC error object, such as
// `ACP session/prompt: {"code":-32603,"message":"Internal error: ..."}`, and
// transport framing down to the provider's sentence and structured error name.
func unwrapFailureMessage(message string) (string, string) {
	detail, errorName := message, ""
	if start := strings.Index(message, "{"); start >= 0 {
		var wrapped wrappedFailure
		if err := json.Unmarshal([]byte(message[start:]), &wrapped); err == nil && wrapped.Message != "" {
			detail, errorName = wrapped.Message, wrapped.Data.ProviderData.ErrorName
		}
	}
	for {
		trimmed := failurePrefix.ReplaceAllString(strings.TrimSpace(detail), "")
		if trimmed == detail {
			break
		}
		detail = trimmed
	}
	detail = failureTrailer.ReplaceAllString(detail, "")
	detail = failureSpaces.ReplaceAllString(strings.TrimSpace(detail), " ")
	if detail == "" {
		detail = message
	}
	return detail, errorName
}
