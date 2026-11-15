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

export function isInlineArtifactUrl(url: string | URL): boolean {
	const parsed = parse(url);
	return parsed?.protocol === "http:" && parsed.hostname.startsWith("ao-inline-artifact.") && parsed.hostname.endsWith(".localhost");
}

export function isAgentPageUrl(url: string | URL): boolean {
	const parsed = parse(url);
	if (parsed?.protocol !== "http:") return false;
	return isInlineArtifactUrl(parsed) || (LOOPBACK_NAMES.has(parsed.hostname) && PAGE_ROUTE.test(parsed.pathname));
}

export function isOwnPageRequest(page: URL, request: URL): boolean {
	if (request.protocol !== "http:") return false;
	if (isInlineArtifactUrl(page)) return request.host === page.host;
	const scope = PAGE_ROUTE.exec(page.pathname)?.[0];
	if (!scope || !LOOPBACK_NAMES.has(request.hostname) || request.port !== page.port) return false;
	return scope.endsWith("/") ? request.pathname.startsWith(scope) : request.pathname === scope;
}
