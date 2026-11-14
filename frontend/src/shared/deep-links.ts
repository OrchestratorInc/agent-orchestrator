export const OPEN_SESSION_DEEP_LINK_CHANNEL = "app:open-session-deep-link";

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

export function normalizeSessionDeepLink(raw: string): string | null {
	if (!isSessionDeepLink(raw)) return null;
	const url = new URL(raw);
	url.search = "";
	url.hash = "";
	url.pathname = url.pathname.replace(/\/+$/, "") || "/";
	return url.toString();
}
