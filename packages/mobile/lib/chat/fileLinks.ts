/**
 * Chat file links: `[browser-view-host.ts:1748](frontend/src/main/browser-view-host.ts:1748)`.
 *
 * Agents cite code as Markdown links whose destination is a path, optionally
 * with an editor location (`:12`, `:12:4`, `#L12`, `#L12-L20`). These rules
 * mirror the desktop's (`frontend/src/renderer/lib/workspace-file-path.ts` and
 * `external-link-policy.ts`) so a link opens on the phone exactly when it
 * opens in the desktop's Files panel. Recognizing a link grants nothing: the
 * daemon resolves the path and refuses anything outside the session.
 */

export type ChatFileReference = { path: string; line?: number };

const LOCATION_SUFFIX = /(?:#L\d+(?:C\d+)?(?:-L?\d+(?:C\d+)?)?|:\d+(?::\d+)?)$/i;

function decode(value: string): string {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}

/** The first line named by a location suffix, if any. */
export function fileReferenceLine(reference: string): number | undefined {
	const decoded = decode(reference.trim());
	const match = decoded.match(/#L(\d+)(?:C\d+)?(?:-L?\d+(?:C\d+)?)?$/i) ?? decoded.match(/:(\d+)(?::\d+)?$/);
	if (!match) return undefined;
	const line = Number(match[1]);
	return Number.isSafeInteger(line) && line > 0 ? line : undefined;
}

/**
 * The file a chat link destination names, or undefined for a web link, an
 * in-page anchor, another scheme (mailto:, ao://), or a bare word that does
 * not look like a file.
 */
export function chatFileReference(href: string): ChatFileReference | undefined {
	const trimmed = href.trim();
	if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("//")) return undefined;
	let path = trimmed;
	if (/^file:\/\//i.test(path)) {
		try {
			path = new URL(path).pathname;
			// file:///C:/path is the URL form of a Windows drive path.
			if (/^\/[A-Za-z]:\//.test(path)) path = path.slice(1);
		} catch {
			return undefined;
		}
	} else if (/^[A-Za-z][A-Za-z\d+.-]*:/.test(path.replace(LOCATION_SUFFIX, "")) && !/^[A-Za-z]:[\\/]/.test(path)) {
		// A scheme (https:, mailto:, ao:). The location suffix is stripped first
		// so `README.md:12` is not mistaken for a "README.md:" scheme.
		return undefined;
	}
	path = decode(path).replace(LOCATION_SUFFIX, "").replace(/\\/g, "/").replace(/^\.\//, "").trim();
	if (!path || path.startsWith("//") || path.split("/").includes("..")) return undefined;
	const looksLikeFile = path.includes("/") || /^\.[^/]+$/.test(path) || /\.[a-z\d][a-z\d._-]*$/i.test(path);
	if (!looksLikeFile) return undefined;
	const line = fileReferenceLine(trimmed);
	return line == null ? { path } : { path, line };
}

/** A path the daemon must place: absolute, home-relative, or a Windows drive path. */
export function isAbsoluteFileReference(path: string): boolean {
	return path.startsWith("/") || path === "~" || path.startsWith("~/") || /^[A-Za-z]:\//.test(path);
}
