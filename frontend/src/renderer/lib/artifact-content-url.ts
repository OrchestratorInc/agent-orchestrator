import { getApiBaseUrl } from "./api-client";

export function artifactContentUrl(sessionId: string, path: string, rawUrl?: string): string | undefined {
	if (!rawUrl) return undefined;
	try {
		const { hostname } = new URL(rawUrl);
		if (!hostname.startsWith("ao-preview-artifact.") || !hostname.endsWith(".localhost")) return rawUrl;
		const baseUrl = getApiBaseUrl();
		// Reject dot segments before URL normalization can leave the artifact route.
		if (!baseUrl || !path || path.startsWith("/") || path.includes("\\") || path.split("/").some((part) => part === "." || part === "..")) return undefined;
		const escapedPath = path.split("/").map(encodeURIComponent).join("/");
		return `${baseUrl}/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${escapedPath}?source=artifact&raw=true`;
	} catch {
		return undefined;
	}
}
