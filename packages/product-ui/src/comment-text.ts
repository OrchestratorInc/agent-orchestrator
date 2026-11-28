/**
 * A review comment's preview text, shared by the desktop's Reviews tab and the
 * phone's PR page (which imports this file directly, so it stays free of React
 * and the DOM).
 */

/**
 * A comment's words line by line, as plain text: HTML comments and tags,
 * heading, list and quote markers, and emphasis go; rules and code fences are
 * not lines of words.
 */
export function commentTextLines(body: string): string[] {
	return body
		.replace(/<!--[\s\S]*?-->/g, "")
		// Only tag-shaped spans outside inline code are HTML: `Map<string, number>`
		// and `a < b && c > d` are words to keep.
		.split(/(`[^`\n]*`)/)
		.map((part, index) => index % 2 === 1 ? part : part.replace(/<\/?[A-Za-z][\w-]*(?:\s[^<>]*)?\/?>/g, ""))
		.join("")
		.split("\n")
		.map((line) => {
			const raw = line.trim();
			// Fences and rules go before any marker is stripped, while they still look like
			// themselves: a suggestion block previews as its code, not as "suggestion".
			if (/^(?:```|~~~)/.test(raw) || /^(?:[-*_=]\s*){3,}$/.test(raw)) return "";
			// A GitHub alert's "> [!NOTE]" marker is its type, not its text.
			if (/^(?:>\s*)?\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$/i.test(raw)) return "";
			return raw
				// A quoted line can carry its own heading or list marker ("> ## Reviews paused").
				.replace(/^(?:>\s*)+/, "")
				.replace(/^(?:#{1,6}\s+|[-*+]\s+(?:\[[ xX]\]\s+)?)/, "")
				// Emphasis is stripped outside inline code only; `_` must sit at a word edge, so snake_case stays.
				.split(/(`[^`\n]*`)/)
				.map((part, index) => index % 2 === 1 ? part.slice(1, -1) : part
					.replace(/\*\*|__/g, "")
					.replace(/(^|[^\w])_([^_\n]+?)_(?!\w)/g, "$1$2")
					.replace(/`/g, ""))
				.join("")
				.replace(/\s+/g, " ")
				.trim();
		})
		.filter(Boolean);
}

/**
 * A review summary's one-line preview. The verdict already shows beside it, so a
 * body that opens by restating it ("## Changes requested", "Approved. The fix…")
 * previews from what comes after.
 */
export function summaryPreview(lines: string[], verdictLabels: string[]): string {
	const [first = "", ...rest] = lines;
	const labels = [...new Set(verdictLabels.map((label) => label.trim()).filter(Boolean))];
	for (const label of labels) {
		const lead = new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?:\\s*[:.!—–-]\\s*|\\s*$)`, "i");
		if (!lead.test(first)) continue;
		const remainder = first.replace(lead, "").trim();
		if (remainder) return remainder.charAt(0).toUpperCase() + remainder.slice(1);
		return rest[0] ?? first;
	}
	return first;
}
