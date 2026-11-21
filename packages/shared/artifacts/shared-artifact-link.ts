/** The production page that shows published artifacts. */
export const ARTIFACT_VIEWER_ORIGIN = "https://orchestrator.inc";

/**
 * The origins whose artifact pages the app opens: production, plus one
 * configured https origin (a staging or local viewer), when it is one. Any
 * other site could otherwise show its own page in a window or screen the app
 * titles as Orchestrator's.
 */
export function artifactViewerOrigins(configured?: string | null): readonly string[] {
	const origins = [ARTIFACT_VIEWER_ORIGIN];
	try {
		const extra = configured ? new URL(configured) : null;
		if (extra && extra.protocol === "https:" && !extra.username && !extra.password
			&& (extra.pathname === "/" || extra.pathname === "") && !extra.search && !extra.hash
			&& !origins.includes(extra.origin)) origins.push(extra.origin);
	} catch {
		// Not a URL: production only.
	}
	return origins;
}

/** An artifact page's URL on any https host: the shape alone, no origin check. */
function artifactPageURL(raw: string): URL | null {
	try {
		const url = new URL(raw);
		if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash
			|| !/^\/artifact\/[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(url.pathname)) return null;
		return url;
	} catch {
		return null;
	}
}

/** A published artifact's page on one of origins; null for anything else. */
export function sharedArtifactURL(raw: string, origins: readonly string[] = artifactViewerOrigins()): string | null {
	const url = artifactPageURL(raw);
	return url && origins.includes(url.origin) ? url.href : null;
}

/** Only this exact app route can request the isolated published-document viewer. */
export function parseSharedArtifactLink(raw: string, origins: readonly string[] = artifactViewerOrigins()): string | null {
	try {
		const link = new URL(raw);
		if (link.protocol !== "ao-app:" || link.hostname !== "shared-artifact" || link.username || link.password
			|| link.port || (link.pathname !== "" && link.pathname !== "/") || link.hash
			|| link.searchParams.getAll("url").length !== 1
			|| [...link.searchParams.keys()].some((key) => key !== "url")) return null;
		return sharedArtifactURL(link.searchParams.get("url") ?? "", origins);
	} catch {
		return null;
	}
}

/** Suppress the app-launch action inside the app; this never changes access checks. */
export function sharedArtifactAppURL(raw: string, origins: readonly string[] = artifactViewerOrigins()): string | null {
	const valid = sharedArtifactURL(raw, origins);
	return valid ? `${valid}?inApp=1` : null;
}

/**
 * A published artifact's link ID, from its page URL; null for anything else.
 * It names the link only: opening one checks the origin again.
 */
export function sharedArtifactLinkId(raw: string): string | null {
	const url = artifactPageURL(raw);
	return url ? url.pathname.slice("/artifact/".length) : null;
}
