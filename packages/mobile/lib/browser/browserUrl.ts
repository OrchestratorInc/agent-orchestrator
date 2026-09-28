import type { ServerConfig } from "../config";

const WEB_SCHEME = /^https?:\/\//i;
const ANY_SCHEME = /^[a-z][a-z0-9+.-]*:/i;
const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);
const PREVIEW_FILES_RE = /^\/api\/v1\/sessions\/[^/]+\/preview\/files(?:\/|$)/;

function normalizeServerHost(host: string): string {
	return host
		.trim()
		.replace(/^[a-z][a-z0-9+.-]*:\/\//i, "")
		.replace(/\/+$/, "");
}

function httpBase(cfg: Pick<ServerConfig, "secure" | "host" | "httpPort">): string {
	return `${cfg.secure ? "https" : "http"}://${normalizeServerHost(cfg.host)}:${cfg.httpPort}`;
}

export type BrowserInputResult =
	| { ok: true; url: URL }
	| { ok: false; reason: "empty" | "unsupported_scheme" | "invalid_url"; message: string };

export function isHttpUrl(raw: string | undefined): boolean {
	if (!raw) return false;
	try {
		const url = new URL(raw);
		return url.protocol === "http:" || url.protocol === "https:";
	} catch {
		return false;
	}
}

export function normalizeBrowserInput(input: string, aoHost: string): BrowserInputResult {
	const trimmed = input.trim();
	if (!trimmed) return { ok: false, reason: "empty", message: "Enter a URL to open." };
	if (ANY_SCHEME.test(trimmed) && !WEB_SCHEME.test(trimmed)) {
		return { ok: false, reason: "unsupported_scheme", message: "Only HTTP and HTTPS URLs can be opened." };
	}

	const withScheme = WEB_SCHEME.test(trimmed) ? trimmed : `https://${trimmed}`;
	try {
		const url = new URL(withScheme);
		if (url.protocol !== "http:" && url.protocol !== "https:") {
			return { ok: false, reason: "unsupported_scheme", message: "Only HTTP and HTTPS URLs can be opened." };
		}
		if (!url.hostname) return { ok: false, reason: "invalid_url", message: "Enter a valid URL." };
		return { ok: true, url: rewriteLoopbackUrl(url, aoHost) };
	} catch {
		return { ok: false, reason: "invalid_url", message: "Enter a valid URL." };
	}
}

export function rewriteLoopbackUrl(url: URL, aoHost: string): URL {
	if (!isLoopbackHost(url.hostname)) return url;
	const host = normalizeServerHost(aoHost);
	if (!host) return url;
	const next = new URL(url.href);
	next.hostname = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
	return next;
}

export function isLoopbackHost(hostname: string): boolean {
	return LOOPBACK_HOSTS.has(hostname.toLowerCase());
}

export function shouldAttachPreviewAuth(rawUrl: string | undefined, cfg: ServerConfig): boolean {
	if (!rawUrl) return false;
	try {
		const url = new URL(rawUrl);
		if (url.protocol !== "http:" && url.protocol !== "https:") return false;
		const base = new URL(httpBase(cfg));
		return url.origin === base.origin && PREVIEW_FILES_RE.test(url.pathname);
	} catch {
		return false;
	}
}

export function displayBrowserUrl(rawUrl: string): string {
	if (!rawUrl) return "";
	try {
		const url = new URL(rawUrl);
		if (url.protocol !== "http:" && url.protocol !== "https:") return rawUrl;
		const path = `${url.pathname}${url.search}${url.hash}`;
		return `${url.hostname.replace(/^www\./, "")}${path === "/" ? "" : path}`;
	} catch {
		return rawUrl;
	}
}
