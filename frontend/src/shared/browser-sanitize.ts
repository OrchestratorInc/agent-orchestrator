// URL and title projections that are safe to keep in agent output, tool logs,
// and transcripts. Shared by the Electron browser host and the AO Chrome
// extension so both runtimes redact the same way.

// Safe-to-retain URL form: keep the location useful while removing values that
// commonly carry credentials or one-time handoff material.
export function sanitizeBrowserURL(raw: string): string {
	if (!raw) return "";
	if (raw === "about:blank") return raw;
	try {
		const url = new URL(raw);
		if (!["http:", "https:", "file:"].includes(url.protocol)) {
			return `${url.protocol}[redacted]`;
		}
		url.username = "";
		url.password = "";
		url.hash = "";
		for (const name of [...url.searchParams.keys()]) {
			url.searchParams.set(name, "[redacted]");
		}
		return url.href;
	} catch {
		const withoutFragment = raw.split("#", 1)[0] ?? "";
		const withoutQuery = withoutFragment.split("?", 1)[0] ?? "";
		return withoutQuery
			.replace(/^([a-z][a-z\d+.-]*:\/\/)[^/@\s]+@/i, "$1")
			.slice(0, 2_000);
	}
}

export function sanitizeBrowserTitle(raw: string): string {
	const title = raw.trim();
	if (
		/^(?:[a-z][a-z\d+.-]*:\/\/|(?:about|mailto|data|javascript|blob):)/i.test(
			title,
		)
	) {
		return sanitizeBrowserURL(title);
	}
	return sanitizeURLsInText(raw);
}

export function sanitizeURLsInText(raw: string): string {
	return raw.replace(/\b(?:https?|file):\/\/[^\s<>"']+/gi, (match) =>
		sanitizeBrowserURL(match),
	);
}
