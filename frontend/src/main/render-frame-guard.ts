const RENDER_PATH = /^\/api\/v1\/sessions\/[^/]+\/renders\/[^/]+$/;

/**
 * Whether a subframe showing an agent render may not navigate to targetUrl.
 * The render's scripts can set their own frame's location; a frame that is a
 * render stays on that render (hash changes only).
 */
export function blocksRenderFrameNavigation(currentUrl: string, targetUrl: string): boolean {
	let current: URL;
	try {
		current = new URL(currentUrl);
	} catch {
		return false;
	}
	if (!RENDER_PATH.test(current.pathname)) return false;
	try {
		const target = new URL(targetUrl);
		return target.origin !== current.origin || target.pathname !== current.pathname || target.search !== current.search;
	} catch {
		return true;
	}
}
