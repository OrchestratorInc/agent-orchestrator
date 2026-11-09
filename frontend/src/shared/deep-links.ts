// ao-app:// session deep links: ao-app://sessions/{projectId}/{sessionId}.
// External tools (e.g. the ao-bridge dashboard) render these so a click opens
// the desktop app directly on that session. Both segments are URL-decoded.
//
// IPC channel the main process uses to hand a decoded link target to the
// renderer shell, which validates it against its workspace list and navigates.
// Lives here (not in main/) so main, preload, and renderer share one constant
// without crossing bundle boundaries.
export const OPEN_SESSION_DEEP_LINK_CHANNEL = "app:open-session-deep-link";

export type SessionDeepLinkTarget = {
	projectId: string;
	sessionId: string;
};

// Returns null for everything that is not a session link — including billing
// (ao-app://billing) and sign-in callback (ao-app://callback) URLs, which keep
// their own handlers in main.ts / cloud-auth.ts.
export function parseSessionDeepLink(raw: string): SessionDeepLinkTarget | null {
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		return null;
	}
	if (url.protocol !== "ao-app:" || url.hostname !== "sessions") return null;
	const segments = url.pathname.split("/").filter((segment) => segment.length > 0);
	if (segments.length !== 2) return null;
	try {
		const projectId = decodeURIComponent(segments[0]);
		const sessionId = decodeURIComponent(segments[1]);
		if (!projectId.trim() || !sessionId.trim()) return null;
		return { projectId, sessionId };
	} catch {
		// Malformed percent-encoding (e.g. %ZZ) — not a session link.
		return null;
	}
}
