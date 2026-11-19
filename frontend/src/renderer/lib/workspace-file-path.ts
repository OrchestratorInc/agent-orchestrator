import type { WorkspaceFileSummary } from "../hooks/useSessionWorkspaceFiles";

function normalizeWorkspacePath(path: string): string {
	return path.trim().replace(/^\.\//, "").replace(/\\/g, "/");
}

/** A path with its percent escapes decoded, or as it is when it holds a malformed one. */
export function decodePath(path: string): string {
	try {
		return decodeURIComponent(path);
	} catch {
		return path;
	}
}

function hasNonFileScheme(reference: string): boolean {
	const trimmed = reference.trim();
	return (
		trimmed.startsWith("//") ||
		(/^[A-Za-z][A-Za-z\d+.-]*:/.test(trimmed) &&
			!/^file:\/\//i.test(trimmed) &&
			!/^\/?[A-Za-z]:[\\/]/.test(trimmed))
	);
}

/** Remove markdown/editor location syntax while retaining the referenced file. */
export function normalizeWorkspaceFileReference(reference: string): string {
	let path = reference.trim();
	if (/^file:\/\//i.test(path)) {
		try {
			path = new URL(path).pathname;
			// file:///C:/path is the URL form of a Windows drive path.
			if (/^\/[A-Za-z]:\//.test(path)) path = path.slice(1);
		} catch {
			return "";
		}
	}
	path = decodePath(path)
		.replace(/#L\d+(?:C\d+)?(?:-L?\d+(?:C\d+)?)?$/i, "")
		.replace(/:\d+(?::\d+)?$/, "");
	return normalizeWorkspacePath(path);
}

/** Extract the first line from common editor/file-link location suffixes. */
export function workspaceFileReferenceLine(reference: string): number | undefined {
	const decoded = decodePath(reference.trim());
	const match = decoded.match(/#L(\d+)(?:C\d+)?(?:-L?\d+(?:C\d+)?)?$/i)
		?? decoded.match(/:(\d+)(?::\d+)?$/);
	if (!match) return undefined;
	const line = Number(match[1]);
	return Number.isSafeInteger(line) && line > 0 ? line : undefined;
}

function fileBasename(path: string): string {
	const slash = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
	return slash >= 0 ? path.slice(slash + 1) : path;
}

/** Resolve a file reference only when it identifies a known workspace file. */
export function findWorkspaceFilePath(
	rawPath: string,
	paths: readonly string[],
): string | undefined {
	if (hasNonFileScheme(rawPath)) return undefined;
	const normalized = normalizeWorkspaceFileReference(rawPath);
	if (!normalized) return undefined;

	const exact = paths.find((path) => normalizeWorkspacePath(path) === normalized);
	if (exact) return exact;

	const suffixes = paths.filter((path) => {
		const candidate = normalizeWorkspacePath(path);
		return normalized.endsWith(`/${candidate}`) || candidate.endsWith(`/${normalized}`);
	});
	if (suffixes.length === 1) return suffixes[0];

	const base = fileBasename(normalized);
	const byBase = paths.filter((path) => fileBasename(normalizeWorkspacePath(path)) === base);
	return byBase.length === 1 ? byBase[0] : undefined;
}

/** Reuse hits and misses for one immutable workspace catalog. */
export function createWorkspaceFileLookup(paths: readonly string[]): (reference: string) => string | undefined {
	const results = new Map<string, string | undefined>();
	let index: {
		exact: Map<string, string>;
		byBasename: Map<string, { path: string; normalized: string }[]>;
	} | undefined;
	const resolve = (reference: string): string | undefined => {
		if (hasNonFileScheme(reference)) return undefined;
		const normalized = normalizeWorkspaceFileReference(reference);
		if (!normalized) return undefined;
		// Build on the first file reference, not for chats without any. A fresh
		// session mount must not normalize the whole catalog for every snippet.
		if (!index) {
			index = { exact: new Map(), byBasename: new Map() };
			for (const path of paths) {
				const candidate = normalizeWorkspacePath(path);
				if (!index.exact.has(candidate)) index.exact.set(candidate, path);
				const base = fileBasename(candidate);
				const entries = index.byBasename.get(base) ?? [];
				entries.push({ path, normalized: candidate });
				index.byBasename.set(base, entries);
			}
		}
		const exact = index.exact.get(normalized);
		if (exact) return exact;
		// Every suffix match shares the final path segment. Keep duplicates in
		// the bucket: they must remain ambiguous, just as in the linear lookup.
		const candidates = index.byBasename.get(fileBasename(normalized)) ?? [];
		const suffixes = candidates.filter(({ normalized: candidate }) =>
			normalized.endsWith(`/${candidate}`) || candidate.endsWith(`/${normalized}`),
		);
		if (suffixes.length === 1) return suffixes[0]?.path;
		return candidates.length === 1 ? candidates[0]?.path : undefined;
	};
	return (reference) => {
		if (results.has(reference)) return results.get(reference);
		const result = resolve(reference);
		// Streaming can produce arbitrarily many incomplete references.
		if (results.size >= 1024) results.clear();
		results.set(reference, result);
		return result;
	};
}

/** A link-shaped local path can still target a new file absent from a stale catalog. */
export function explicitWorkspaceFilePath(reference: string): string | undefined {
	if (!reference.trim() || reference.trim().startsWith("#") || hasNonFileScheme(reference)) return undefined;
	const normalized = normalizeWorkspaceFileReference(reference);
	if (!normalized) return undefined;
	if (
		/^\//.test(normalized) ||
		/^\.\.\//.test(normalized) ||
		/^~\//.test(normalized) ||
		/^[A-Za-z]:\//.test(normalized) ||
		normalized.includes("/")
	) {
		return normalized;
	}
	return undefined;
}

/** True for a normalized reference that names a location by absolute path. */
export function isAbsoluteFileReference(path: string): boolean {
	return path.startsWith("/") || path === "~" || path.startsWith("~/") || /^[A-Za-z]:\//.test(path);
}

/**
 * The path to hand to the file opener for a reference the file list matched to
 * `matched`. An absolute reference is kept as written: its tail matching a
 * workspace file does not mean it is that file, so the daemon decides.
 */
export function workspaceFileOpenTarget(reference: string, matched: string): string {
	if (hasNonFileScheme(reference)) return matched;
	const normalized = normalizeWorkspaceFileReference(reference);
	return isAbsoluteFileReference(normalized) ? normalized : matched;
}

/**
 * Map a chat/turn path onto the workspace-relative path the Files API expects.
 * Turn diffs often carry basenames or absolute worktree paths; the workspace
 * file list carries repo-relative paths. For an absolute path this is a guess
 * from its tail, so callers resolve absolute references through the daemon
 * instead.
 */
export function matchWorkspaceFilePath(
	rawPath: string,
	files: readonly WorkspaceFileSummary[],
): string {
	const normalized = normalizeWorkspaceFileReference(rawPath);
	if (!normalized) return rawPath;

	const exact = files.find((file) => normalizeWorkspacePath(file.path) === normalized);
	if (exact) return exact.path;

	const suffix = files.find((file) => {
		const candidate = normalizeWorkspacePath(file.path);
		return normalized.endsWith(`/${candidate}`) || candidate.endsWith(`/${normalized}`);
	});
	if (suffix) return suffix.path;

	const base = fileBasename(normalized);
	const byBase = files.filter((file) => fileBasename(normalizeWorkspacePath(file.path)) === base);
	if (byBase.length === 1) return byBase[0]!.path;

	return normalized;
}
