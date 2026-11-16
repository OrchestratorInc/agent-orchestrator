import { getApiBaseUrl } from "./api-client";

/** `savedVersion` reads that kept version of the file instead of its current content. */
export function artifactContentUrl(sessionId: string, path: string, rawUrl?: string, savedVersion?: number): string | undefined {
	if (!rawUrl) return undefined;
	const versioned = (url: string) => savedVersion === undefined ? url : `${url}${url.includes("?") ? "&" : "?"}artifactVersion=${savedVersion}`;
	try {
		const { hostname } = new URL(rawUrl);
		if (!hostname.startsWith("ao-preview-artifact.") || !hostname.endsWith(".localhost")) return versioned(rawUrl);
		const baseUrl = getApiBaseUrl();
		// Reject dot segments before URL normalization can leave the artifact route.
		if (!baseUrl || !path || path.startsWith("/") || path.includes("\\") || path.split("/").some((part) => part === "." || part === "..")) return undefined;
		const escapedPath = path.split("/").map(encodeURIComponent).join("/");
		return versioned(`${baseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${escapedPath}?source=artifact&raw=true`);
	} catch {
		return undefined;
	}
}
