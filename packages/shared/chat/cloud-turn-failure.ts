/**
 * Why an AO Cloud turn failed, in the agent's own words.
 *
 * Cloud workers before the control plane cleaned this up stored a failed ACP
 * prompt as `ACP prompt: <JSON-RPC error>: <the adapter's stderr>`: the
 * envelope, the adapter's log lines and Node warnings. A usage limit is read
 * out of that by usage-limit.ts; this reads any other failure's reason, so the
 * chat never shows the dump.
 */

const ACP_PROMPT = "ACP prompt:";

// A Node runtime warning, which explains nothing about the turn.
const NODE_NOISE = /^\(node:\d+\)|^\(Use `node --trace-/;

/** The reason a Cloud turn's error text gives; other text comes back as written. */
export function cloudTurnFailureMessage(error: string): string {
	const text = error.trim();
	if (!text.startsWith(ACP_PROMPT)) return text;
	const rest = text.slice(ACP_PROMPT.length).trim();
	const envelope = rest.startsWith("{") ? leadingJsonObject(rest) : undefined;
	if (envelope) {
		const message = acpErrorMessage(envelope);
		if (message) return message;
	}
	// The agent gave no reason of its own (it exited): its stderr is the only
	// one, less the runtime's warnings.
	return rest.split("\n").filter((line) => line.trim() !== "" && !NODE_NOISE.test(line.trim())).join("\n").replace(/:\s*$/, "").trim();
}

/** A JSON-RPC error's own explanation, without the generic "Internal error" lead. */
function acpErrorMessage(envelope: Record<string, unknown>): string {
	let message = typeof envelope.message === "string" ? envelope.message.trim() : "";
	if (message.startsWith("Internal error:")) message = message.slice("Internal error:".length).trim();
	else if (message === "Internal error") message = "";
	if (message) return message;
	const data = envelope.data;
	const details = data && typeof data === "object" && !Array.isArray(data) ? (data as Record<string, unknown>).details : undefined;
	return typeof details === "string" ? details.trim() : "";
}

/** The JSON object `text` starts with, read up to its closing brace; undefined when it is not one. */
function leadingJsonObject(text: string): Record<string, unknown> | undefined {
	let depth = 0;
	let inString = false;
	for (let index = 0; index < text.length; index++) {
		const char = text[index];
		if (inString) {
			if (char === "\\") index++;
			else if (char === "\"") inString = false;
			continue;
		}
		if (char === "\"") inString = true;
		else if (char === "{") depth++;
		else if (char === "}" && --depth === 0) {
			try {
				const value = JSON.parse(text.slice(0, index + 1)) as unknown;
				return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
			} catch {
				return undefined;
			}
		}
	}
	return undefined;
}
