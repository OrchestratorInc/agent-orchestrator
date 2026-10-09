import { isAgentPageUrl } from "../shared/agent-page-url";

/**
 * Whether a subframe showing an agent page may not navigate to targetUrl.
 * The page's scripts can set their own frame's location; a frame that is a
 * render, or an HTML artifact framed in the chat, stays on that page (hash
 * changes only).
 */
export function blocksRenderFrameNavigation(currentUrl: string, targetUrl: string): boolean {
	if (!isAgentPageUrl(currentUrl)) return false;
	const current = new URL(currentUrl);
	try {
		const target = new URL(targetUrl);
		return target.origin !== current.origin || target.pathname !== current.pathname || target.search !== current.search;
	} catch {
		return true;
	}
}
