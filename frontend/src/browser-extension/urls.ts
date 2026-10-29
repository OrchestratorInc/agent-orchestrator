// URL rules for agent navigation, matching AO's Browser panel: explicit
// http(s) URLs, localhost forms, or bare hostnames; never local files.

import { BridgeError } from "./protocol";

const LOCALHOST = /^(localhost|127(?:\.\d{1,3}){3}|0\.0\.0\.0|\[::1\])(?::\d+)?(?:[/?#]|$)/i;

function looksLikeHost(raw: string): boolean {
	const host = raw.split(/[/?#]/, 1)[0] ?? "";
	if (host === "") return false;
	if (host.startsWith("[") && host.includes("]")) return true;
	if (/:\d+$/.test(host)) return true;
	return host.includes(".");
}

export function normalizeAgentURL(input: string): string {
	const raw = input.trim();
	if (!raw) throw new BridgeError("URL_REQUIRED", "url is required");
	if (/^[a-zA-Z]:[\\/]/.test(raw) || raw.startsWith("/") || /^file:/i.test(raw)) {
		throw new BridgeError("BROWSER_URL_FORBIDDEN", "Agent browser commands cannot open local files");
	}
	const explicit = /^[a-z][a-z\d+.-]*:/i.test(raw) && !LOCALHOST.test(raw) && !/^[^/:]+:\d+/.test(raw);
	if (explicit && !/^https?:\/\//i.test(raw)) {
		throw new BridgeError("BROWSER_URL_FORBIDDEN", "Agent browser commands support only http(s) URLs");
	}
	if (!/^https?:\/\//i.test(raw) && !LOCALHOST.test(raw) && !looksLikeHost(raw)) {
		throw new BridgeError("INVALID_URL", "ao browser open requires an explicit http(s) URL or hostname");
	}
	const candidate = /^https?:\/\//i.test(raw) ? raw : `${LOCALHOST.test(raw) ? "http" : "https"}://${raw}`;
	let url: URL;
	try {
		url = new URL(candidate);
	} catch {
		throw new BridgeError("INVALID_URL", "ao browser open requires an explicit http(s) URL or hostname");
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") {
		throw new BridgeError("BROWSER_URL_FORBIDDEN", "Agent browser commands support only http(s) URLs");
	}
	return url.href;
}

export function originOf(raw: string | undefined): string {
	try {
		const url = new URL(raw ?? "");
		return url.protocol === "http:" || url.protocol === "https:" ? url.origin : "";
	} catch {
		return "";
	}
}
