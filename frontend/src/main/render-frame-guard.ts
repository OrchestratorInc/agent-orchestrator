import { isAgentPageUrl } from "../shared/agent-page-url";

export function cloudArtifactPrefix(url: URL): string | undefined {
	return /^(.*\/api\/cloud\/v1\/artifact-files\/[^/]+\/)/.exec(url.pathname)?.[1];
}

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
