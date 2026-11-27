// The quota refusals the daemon's ParseConversationUsageLimit recognises
// (backend/internal/domain/usage_limit.go). Keep the two lists in step.
const USAGE_LIMIT_PREFIXES = [
	"You've hit your",
	"You've reached your",
	"You're out of usage credits",
	"Your org is out of usage · add funds to continue",
	"Your org is out of usage · contact your admin",
	"Your seat type doesn't include usage credits",
	"Your seat type doesn't include usage",
	"Your usage allocation has been disabled by your admin",
	"Your group's usage limit is set to $0",
	"Fable 5 requires usage credits",
	"You're out of extra usage",
	"Your seat type doesn't include extra usage",
];
const USAGE_LIMIT_MESSAGE = /^(?:usage limit (?:reached|exceeded)\b|(?:session|weekly|daily) usage limit (?:reached|exceeded)\b|the Claude account has no available quota\b)/i;

/**
 * The provider's quota refusal, when a turn failed on one.
 *
 * A local daemon recognises these itself and marks the turn. A cloud session's
 * turn arrives with only its error, often the transport's whole failure
 * (`ACP prompt: {"message":"Internal error: You've hit your…"}: stderr`), so the
 * sentence is read out of the JSON-RPC error before matching. Only known
 * refusals match, never a message that merely mentions a limit.
 */
export function usageLimitSentence(error: string): string | undefined {
	const raw = error.trim();
	for (const candidate of [raw, jsonRpcMessage(raw)]) {
		const sentence = candidate?.trim();
		if (!sentence) continue;
		const plain = sentence.replace(/’/g, "'");
		if (USAGE_LIMIT_MESSAGE.test(plain) || USAGE_LIMIT_PREFIXES.some((prefix) => plain.startsWith(prefix))) return sentence;
	}
	return undefined;
}

// The first "message" string in the text, less the "Internal error: " a
// JSON-RPC -32603 answer puts ahead of the provider's own words.
function jsonRpcMessage(raw: string): string | undefined {
	const match = /"message"\s*:\s*("(?:\\.|[^"\\])*")/.exec(raw);
	if (!match?.[1]) return undefined;
	try {
		const value = JSON.parse(match[1]) as unknown;
		return typeof value === "string" ? value.replace(/^internal error:\s*/i, "") : undefined;
	} catch {
		return undefined;
	}
}
