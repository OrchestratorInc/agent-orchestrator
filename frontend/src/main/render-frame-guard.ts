import { isAgentPageUrl } from "../shared/agent-page-url";

/** The access prefix of a cloud artifact file URL, or undefined for any other URL. */
export function cloudArtifactPrefix(url: URL): string | undefined {
	return /^(.*\/api\/cloud\/v1\/artifact-files\/[^/]+\/)/.exec(url.pathname)?.[1];
}

/**
 * Whether a subframe showing an agent page may not navigate to targetUrl.
 * The page's scripts can set their own frame's location; a frame that is a
 * render, or an HTML artifact framed in the chat, stays on that page (hash
 * changes only). A cloud artifact file may open another file of its session,
 * under the same access prefix, and nothing else.
 */
export function blocksRenderFrameNavigation(currentUrl: string, targetUrl: string): boolean {
	let currentParsed: URL;
	try {
		currentParsed = new URL(currentUrl);
	} catch {
		return false;
	}
	const prefix = cloudArtifactPrefix(currentParsed);
	if (prefix !== undefined) {
		try {
			const target = new URL(targetUrl);
			return target.origin !== currentParsed.origin || !target.pathname.startsWith(prefix);
		} catch {
			return true;
		}
	}
	if (!isAgentPageUrl(currentUrl)) return false;
	const current = new URL(currentUrl);
	try {
		const target = new URL(targetUrl);
		return target.origin !== current.origin || target.pathname !== current.pathname || target.search !== current.search;
	} catch {
		return true;
	}
}
