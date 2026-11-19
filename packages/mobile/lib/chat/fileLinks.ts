/**
 * Chat file links: `[browser-view-host.ts:1748](frontend/src/main/browser-view-host.ts:1748)`.
 *
 * Agents cite code as Markdown links whose destination is a path, optionally
 * with an editor location (`:12`, `:12:4`, `#L12`, `#L12-L20`). These rules
 * mirror the desktop's (`frontend/src/renderer/lib/workspace-file-path.ts` and
 * `external-link-policy.ts`) so a link opens on the phone when it opens in the
 * desktop's Files panel; the phone also takes a known extensionless file cited
 * at a line (`Dockerfile:12`). Recognizing a link grants nothing: the daemon
 * resolves the path and refuses anything outside the session.
 */

export type ChatFileReference = { path: string; line?: number };

const EXTENSIONLESS_FILES = new Set([
	"Brewfile", "BUILD", "Caddyfile", "CODEOWNERS", "Containerfile", "Dockerfile", "Gemfile", "Jenkinsfile",
	"Justfile", "justfile", "LICENSE", "Makefile", "makefile", "GNUmakefile", "Procfile", "Rakefile", "Vagrantfile", "WORKSPACE",
]);

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
	// Before any URL parsing: `file:///a/b/../c` would come back as `/a/c`.
	if (decode(trimmed).split(/[\\/]/).includes("..")) return undefined;
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
	const line = fileReferenceLine(trimmed);
	// A bare name with a location (`Dockerfile:12`) only for files that are known
	// to have no extension: `localhost:3000` has the same shape and is a server.
	const looksLikeFile = path.includes("/") || /^\.[^/]+$/.test(path) || /\.[a-z\d][a-z\d._-]*$/i.test(path) || (line != null && EXTENSIONLESS_FILES.has(path));
	if (!looksLikeFile) return undefined;
	return line == null ? { path } : { path, line };
}

/** A path the daemon must place: absolute, home-relative, or a Windows drive path. */
export function isAbsoluteFileReference(path: string): boolean {
	return path.startsWith("/") || path === "~" || path.startsWith("~/") || /^[A-Za-z]:\//.test(path);
}

/**
 * A bare name (`README.md`, `api.ts:12`) has no folder to say it is a file:
 * `example.com` has the same shape. Like the desktop, it is a file only when
 * the session has one by that name.
 */
export function isBareFileReference(path: string): boolean {
	return !path.includes("/") && !isAbsoluteFileReference(path);
}

/** The workspace path a bare name names: the exact path, else the one file with that name. */
export function matchBareFile(name: string, paths: readonly string[]): string | null {
	if (paths.includes(name)) return name;
	let found: string | null = null;
	for (const path of paths) {
		if (path.slice(path.lastIndexOf("/") + 1) !== name) continue;
		if (found) return null;
		found = path;
	}
	return found;
}

/**
 * The workspace path an absolute path names, from candidates the worktree has:
 * the longest one the absolute path ends with. For a backend that cannot place
 * an absolute path itself (Orchestrator Cloud).
 */
export function matchAbsoluteFile(absolute: string, candidates: readonly string[]): string | null {
	let best: string | null = null;
	for (const candidate of candidates) {
		if ((absolute === candidate || absolute.endsWith(`/${candidate}`)) && (!best || candidate.length > best.length)) best = candidate;
	}
	return best;
}
