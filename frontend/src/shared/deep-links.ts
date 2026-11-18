// ao-app:// session deep links: ao-app://sessions/{projectId}/{sessionId},
// optionally followed by /artifacts/{path}. External tools (e.g. the ao-bridge
// dashboard) render these so a click opens the desktop app directly on that
// session.
//
// IPC channel the main process uses to hand the raw URL to the renderer shell,
// which re-reads it as its in-app ao:// form and resolves it through
// renderer/lib/session-links — so deep links and ao:// links share one parser.
// Lives here (not in main/) so main, preload, and renderer share one constant
// without crossing bundle boundaries.
export const OPEN_SESSION_DEEP_LINK_CHANNEL = "app:open-session-deep-link";

// Routing predicate only: true for any ao-app://sessions/... URL with a
// well-formed target path, including artifact routes. Parsing and validation
// against the workspace lists are the shell's job (session-links.ts), which
// keeps the two link schemes in agreement (e.g. ids containing an encoded
// slash are rejected for both). Anything else — billing, sign-in callback,
// unknown hosts — keeps its own main-process handling.
export function isSessionDeepLink(raw: string): boolean {
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		return false;
	}
	if (url.protocol !== "ao-app:" || url.hostname !== "sessions") return false;
	return url.pathname.split("/").filter((segment) => segment.length > 0).length >= 2;
}

// Canonical form handed to the shell: lowercase scheme (WHATWG parsing gives
// this for free), and no query, hash, or trailing slash — the in-app ao://
// parser rejects all three, while this predicate accepts them. Null for
// anything isSessionDeepLink rejects.
export function normalizeSessionDeepLink(raw: string): string | null {
	if (!isSessionDeepLink(raw)) return null;
	const url = new URL(raw);
	url.search = "";
	url.hash = "";
	url.pathname = url.pathname.replace(/\/+$/, "") || "/";
	return url.toString();
}
