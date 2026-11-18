/** A published artifact is a web document, independent of the author's session. */
export function sharedArtifactURL(raw: string): string | null {
	try {
		const url = new URL(raw);
		if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash
			|| !/^\/artifact\/[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(url.pathname)) return null;
		return url.href;
	} catch {
		return null;
	}
}

/** Only this exact app route can request the isolated published-document viewer. */
export function parseSharedArtifactLink(raw: string): string | null {
	try {
		const link = new URL(raw);
		if (link.protocol !== "ao-app:" || link.hostname !== "shared-artifact" || link.username || link.password
			|| link.port || (link.pathname !== "" && link.pathname !== "/") || link.hash
			|| link.searchParams.getAll("url").length !== 1
			|| [...link.searchParams.keys()].some((key) => key !== "url")) return null;
		return sharedArtifactURL(link.searchParams.get("url") ?? "");
	} catch {
		return null;
	}
}

/** Suppress the app-launch action inside the app; this never changes access checks. */
export function sharedArtifactAppURL(raw: string): string | null {
	const valid = sharedArtifactURL(raw);
	return valid ? `${valid}?inApp=1` : null;
}
