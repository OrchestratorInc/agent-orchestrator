export type UpdateFailureCategory =
	| "network"
	| "signature"
	| "permission"
	| "disk_space"
	| "not_found"
	| "not_supported"
	| "unknown";

export type UpdatePhase = "check" | "download";

export type UpdateTrigger = "automatic" | "manual";

export type UpdateOutcome = {
	event: "ao.updates.failed" | "ao.updates.downloaded" | "ao.renderer.update_unsupported" | "ao.renderer.update_check_succeeded";
	phase: UpdatePhase;
	trigger: UpdateTrigger;
	error_category?: UpdateFailureCategory;
	to_version?: string;
	differential_eligible?: boolean;
	transfer_mode?: "differential" | "full";
	fallback?: boolean;
	transferred_bytes?: number;
	target_bytes?: number;
};

export function isNetErrorMessage(message: string | undefined): boolean {
	return /^net::/i.test(message ?? "");
}

export function updateFailureCategory(message: string | undefined): UpdateFailureCategory {
	const text = (message ?? "").toLowerCase();
	if (text === "") return "unknown";
	if (/enotfound|econnreset|econnrefused|etimedout|net::|socket|dns|network|502|503|504/.test(text)) {
		return "network";
	}
	if (/signature|code sign|notariz|checksum|sha512|integrity|not trusted/.test(text)) {
		return "signature";
	}
	if (/eacces|eperm|permission|denied|read-only|readonly/.test(text)) return "permission";
	if (/enospc|no space|disk full/.test(text)) return "disk_space";
	if (/404|not found/.test(text)) return "not_found";
	if (/unsupported|not supported|cannot update/.test(text)) return "not_supported";
	return "unknown";
}

export function updateFailureOutcome(
	message: string | undefined,
	phase: UpdatePhase,
	trigger: UpdateTrigger,
	toVersion: string | undefined,
): UpdateOutcome {
	return {
		event: "ao.updates.failed",
		phase,
		trigger,
		error_category: updateFailureCategory(message),
		...(toVersion ? { to_version: toVersion } : {}),
	};
}

export const RELEASE_NOTES_MAX_CHARS = 4000;

export function boundPlainReleaseNotes(notes: string | null | undefined): string | undefined {
	const text = (notes ?? "").trim();
	if (text === "") return undefined;
	return text.length > RELEASE_NOTES_MAX_CHARS
		? `${text.slice(0, RELEASE_NOTES_MAX_CHARS).trimEnd()}…`
		: text;
}

const HTML_ANCHOR_PATTERN = /<a\b((?:"[^"]*"|'[^']*'|[^'">])*)>([\s\S]*?)<\/a>/gi;
const HTML_BLOCK_PATTERN = /<(?:p|ul|ol|li|div|h[1-6])(?:\s[^>]*)?>/i;

function htmlAnchorToMarkdown(_match: string, attributes: string, inner: string): string {
	const text = inner.replace(/<[^>]*>/g, "").trim();
	const href = /\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')/i.exec(attributes);
	const url = (href?.[1] ?? href?.[2] ?? "").replace(/&amp;/gi, "&");
	if (text === "" || text.startsWith("@") || !/^https:\/\/[^\s()]+$/.test(url)) return text;
	return `[${text.replace(/[[\]]/g, "\\$&")}](${url})`;
}

export function normalizeReleaseNotes(
	notes: string | Array<{ version?: string; note?: string | null }> | null | undefined,
): string | undefined {
	const raw = typeof notes === "string"
		? notes
		: Array.isArray(notes)
			? notes.map((entry) => entry?.note ?? "").join("\n\n")
			: "";
	const isHtml = HTML_BLOCK_PATTERN.test(raw);
	const markdown = isHtml
		? raw
			.replace(HTML_ANCHOR_PATTERN, htmlAnchorToMarkdown)
			.replace(/<li(?:\s[^>]*)?>/gi, "\n- ")
			.replace(/<h[1-6](?:\s[^>]*)?>/gi, "\n### ")
			.replace(/<\/?(?:strong|b)(?:\s[^>]*)?>/gi, "**")
			.replace(/<\/?code(?:\s[^>]*)?>/gi, "`")
		: raw;
	const text = markdown
		.replace(/<\/(p|div|li|ul|ol|h[1-6]|tr)>/gi, "\n")
		.replace(/<br\s*\/?>/gi, "\n")
		.replace(/<[^>]*>/g, "")
		.replace(/&nbsp;/gi, " ")
		.replace(/&amp;/gi, "&")
		.replace(/&lt;/gi, "<")
		.replace(/&gt;/gi, ">")
		.replace(/&quot;/gi, '"')
		.replace(/&#39;/gi, "'")
		.replace(/[ \t]+/g, " ")
		.replace(/\n{3,}/g, "\n\n")
		.split("\n")
		.map((line) => line.trim())
		.join("\n")
		.trim();
	return boundPlainReleaseNotes(isHtml ? text.replace(/^(- .*)\n\n(?=- )/gm, "$1\n") : text);
}
