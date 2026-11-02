// Agent pages framed in the chat: renders and HTML artifacts, served on the
// daemon's page routes or on an artifact's own inline origin. One definition
// for the renderer, which frames them, and the main process, which guards them.

const PAGE_ROUTE = /^\/api\/v1\/sessions\/[^/]+\/(renders\/[^/]+$|artifact-files\/)/;
const LOOPBACK_NAMES = new Set(["127.0.0.1", "localhost", "[::1]"]);

function parse(url: string | URL): URL | undefined {
	if (url instanceof URL) return url;
	try {
		return new URL(url);
	} catch {
		return undefined;
	}
}

/**
 * Whether a URL is on an inline-artifact origin, ao-inline-artifact.<session>.localhost.
 * The chat frames such a page with allow-same-origin, which is only safe on
 * an origin that is not the app's own, so nothing else is accepted.
 */
export function isInlineArtifactUrl(url: string | URL): boolean {
	const parsed = parse(url);
	return parsed?.protocol === "http:" && parsed.hostname.startsWith("ao-inline-artifact.") && parsed.hostname.endsWith(".localhost");
}

/** Whether a frame URL is an agent page: a render or an HTML artifact framed in the chat. */
export function isAgentPageUrl(url: string | URL): boolean {
	const parsed = parse(url);
	if (parsed?.protocol !== "http:") return false;
	return isInlineArtifactUrl(parsed) || (LOOPBACK_NAMES.has(parsed.hostname) && PAGE_ROUTE.test(parsed.pathname));
}

/**
 * Whether a request from agent page `page` to this computer stays on the
 * page's own files: its own inline origin, or its own route on the daemon
 * (the render itself, or its session's artifact files). The daemon's API,
 * other sessions' origins and every other local port are not the page's.
 */
export function isOwnPageRequest(page: URL, request: URL): boolean {
	if (request.protocol !== "http:") return false;
	if (isInlineArtifactUrl(page)) return request.host === page.host;
	const scope = PAGE_ROUTE.exec(page.pathname)?.[0];
	if (!scope || !LOOPBACK_NAMES.has(request.hostname) || request.port !== page.port) return false;
	// A render is one document; an artifact's scope is its session's artifact files.
	return scope.endsWith("/") ? request.pathname.startsWith(scope) : request.pathname === scope;
}
