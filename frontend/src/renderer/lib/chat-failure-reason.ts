// Why a chat turn failed, read from the error it shows. The provider's message
// never leaves the app: telemetry gets only one of these fixed reasons, so the
// dashboard can tell a rate limit from a network drop from a crashed agent.

export const CHAT_FAILURE_REASONS = [
	"quota_exceeded",
	"rate_limited",
	"provider_overloaded",
	"context_too_long",
	"model_unavailable",
	"auth_required",
	"network_error",
	"timed_out",
	"provider_server_error",
	"agent_not_installed",
	"agent_exited",
	"content_refused",
] as const;

export type ChatFailureReason = (typeof CHAT_FAILURE_REASONS)[number];

// Checked in order; the first match wins. Specific causes come before the
// generic ones they would also match: an exceeded quota is reported as a 429,
// and an overloaded provider as a 5xx.
const PATTERNS: readonly [RegExp, ChatFailureReason][] = [
	[/insufficient[_ ]quota|exceeded your current quota|credit balance|billing[_ ]hard[_ ]limit|billing quota|payment required|\b402\b/i, "quota_exceeded"],
	[/\b429\b|rate[ _-]?limit|too many requests/i, "rate_limited"],
	[/\b529\b|overloaded|over capacity|server is busy|high demand/i, "provider_overloaded"],
	[/context (?:length|window)|maximum context|prompt is too long|input is too long|too many tokens|token limit/i, "context_too_long"],
	[/model[^.\n]{0,60}(?:not found|does not exist|not available|unavailable|not supported|is invalid)|unknown model|invalid model|unsupported model/i, "model_unavailable"],
	[/\b401\b|\b403\b|unauthori[sz]ed|forbidden|invalid (?:api|x-api)[ -]?key|authentication|not (?:logged|signed) in|log ?in again|sign ?in again|token (?:has )?expired/i, "auth_required"],
	[/ECONNRESET|ECONNREFUSED|ENOTFOUND|EAI_AGAIN|ENETUNREACH|fetch failed|socket hang up|network error|connection (?:reset|refused|closed|error|lost)|getaddrinfo|could not resolve host/i, "network_error"],
	[/timed? ?out|timeout|deadline exceeded|ETIMEDOUT/i, "timed_out"],
	[/\b50[0234]\b|internal server error|bad gateway|service unavailable|gateway timeout|api_error|server error/i, "provider_server_error"],
	[/command not found|is not recognized as an internal or external command|not installed|ENOENT|no such file or directory/i, "agent_not_installed"],
	[/exited (?:with|unexpectedly)|process (?:exited|died|crashed|ended|terminated)|signal: killed|killed by signal|crashed|terminated unexpectedly|broken pipe|EPIPE/i, "agent_exited"],
	[/content policy|usage policy|safety system|flagged (?:as|by)|violates? (?:our|the) (?:terms|policy)/i, "content_refused"],
];

/** The fixed reason for a failed turn's error text, or undefined when none fits. */
export function chatFailureReason(error: string | undefined): ChatFailureReason | undefined {
	if (!error) return undefined;
	for (const [pattern, reason] of PATTERNS) {
		if (pattern.test(error)) return reason;
	}
	return undefined;
}
