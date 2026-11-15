import { getApiBaseUrl } from "./api-client";

const ABSOLUTE_SRC = /^[a-z][a-z0-9+.-]*:/i;

export function isAbsoluteMarkdownAssetSrc(src: string): boolean {
	return ABSOLUTE_SRC.test(src) || src.startsWith("/");
}

function normalizeSegments(joined: string): string {
	const stack: string[] = [];
	for (const segment of joined.split("/")) {
		if (segment === "" || segment === ".") continue;
		if (segment === "..") {
			stack.pop();
			continue;
		}
		stack.push(segment);
	}
	return stack.join("/");
}

export function resolveMarkdownAssetPath(markdownFilePath: string, rawSrc: string): string {
	const withoutQueryOrFragment = rawSrc.replace(/[?#].*$/, "");
	let decodedPath = withoutQueryOrFragment;
	try {
		decodedPath = decodeURIComponent(withoutQueryOrFragment);
	} catch {
	}
	const lastSlash = markdownFilePath.lastIndexOf("/");
	const markdownDir = lastSlash === -1 ? "" : markdownFilePath.slice(0, lastSlash);
	const joined = markdownDir ? `${markdownDir}/${decodedPath}` : decodedPath;
	return normalizeSegments(joined);
}

export function buildWorkspaceBlobUrl(sessionId: string, path: string, version: number, apiBaseUrl = getApiBaseUrl()): string {
	const query = new URLSearchParams({ path, side: "after", v: String(version) });
	return `${apiBaseUrl.replace(/\/+$/, "")}/api/v1/sessions/${encodeURIComponent(sessionId)}/workspace/file/blob?${query}`;
}

export function resolveArtifactImageSrc(artifactOrigin: string, markdownFilePath: string, rawSrc: string | undefined): string | undefined {
	if (!rawSrc) return undefined;
	if (isAbsoluteMarkdownAssetSrc(rawSrc)) return rawSrc;
	const assetPath = resolveMarkdownAssetPath(markdownFilePath, rawSrc);
	if (!assetPath) return undefined;
	return `${artifactOrigin}/${assetPath.split("/").map(encodeURIComponent).join("/")}`;
}

export function resolveMarkdownImageSrc(
	sessionId: string,
	markdownFilePath: string,
	rawSrc: string | undefined,
	version: number,
	apiBaseUrl?: string,
): string | undefined {
	if (!rawSrc) return undefined;
	if (isAbsoluteMarkdownAssetSrc(rawSrc)) return rawSrc;
	return buildWorkspaceBlobUrl(sessionId, resolveMarkdownAssetPath(markdownFilePath, rawSrc), version, apiBaseUrl);
}
