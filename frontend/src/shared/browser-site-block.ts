// Recognises a site refusing AO's Browser panel, so the panel can offer to
// continue in the person's own Chrome. Only signals that sites send on
// purpose count; page text is never matched. A miss shows nothing and the
// browser menu's Open in Chrome still works, while a false alarm would nag on
// a page that works.

/** Why a site will not work here. */
export type BrowserSiteBlockReason =
	/** Bot protection keeps challenging this browser (Cloudflare). */
	| "verification"
	/** Bot protection refused this browser outright. */
	| "blocked"
	/** Google refuses to sign in from an embedded browser. */
	| "signin";

export type BrowserSiteBlock = { reason: BrowserSiteBlockReason; host: string };

/** What a finished top-level page response says about the site. */
export type BrowserSiteResponse =
	/** A Cloudflare challenge page. Most pass on their own within seconds. */
	| { kind: "challenge"; host: string }
	| { kind: "blocked"; block: BrowserSiteBlock }
	| { kind: "page"; host: string };

type Headers = Record<string, string | string[] | undefined>;

function header(headers: Headers | undefined, name: string): string | undefined {
	if (!headers) return undefined;
	for (const [key, value] of Object.entries(headers)) {
		if (key.toLowerCase() !== name) continue;
		return (Array.isArray(value) ? value.join(",") : value)?.trim().toLowerCase();
	}
	return undefined;
}

/**
 * Classify a finished main-frame response. Sources: Cloudflare marks every
 * challenge page `cf-mitigated: challenge`; DataDome tags its responses
 * `x-datadome`/`x-dd-b`; Akamai Bot Manager denies with a 403 from AkamaiGHost;
 * Google answers OAuth in an embedded browser with `disallowed_useragent`.
 */
export function classifySiteResponse(input: { url: string; statusCode: number; headers?: Headers }): BrowserSiteResponse | undefined {
	let url: URL;
	try {
		url = new URL(input.url);
	} catch {
		return undefined;
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
	const host = url.hostname;
	const mitigated = header(input.headers, "cf-mitigated");
	if (mitigated === "challenge") return { kind: "challenge", host };
	if (mitigated) return { kind: "blocked", block: { reason: "blocked", host } };
	const denied = input.statusCode === 403 || input.statusCode === 429;
	if (denied && (header(input.headers, "x-datadome") !== undefined || header(input.headers, "x-dd-b") !== undefined)) {
		return { kind: "blocked", block: { reason: "blocked", host } };
	}
	if (input.statusCode === 403 && header(input.headers, "server")?.includes("akamaighost")) {
		return { kind: "blocked", block: { reason: "blocked", host } };
	}
	if (host === "accounts.google.com" && (input.statusCode === 403 || /disallowed_useragent/i.test(url.search) || url.pathname.includes("/signin/rejected"))) {
		return { kind: "blocked", block: { reason: "signin", host } };
	}
	return { kind: "page", host };
}

/** Pages that can be handed to another browser: ordinary web pages only. */
export function canOpenInUserBrowser(rawUrl: string): boolean {
	try {
		const url = new URL(rawUrl);
		if (url.protocol !== "http:" && url.protocol !== "https:") return false;
		// AO's own preview origins only work inside AO.
		return !/^ao-preview-[0-9a-f]{32}\.localhost$/.test(url.hostname);
	} catch {
		return false;
	}
}
