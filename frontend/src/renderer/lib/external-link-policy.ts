import { aoBridge } from "./bridge";
import { decodePath } from "./workspace-file-path";

export function isWebLink(url: string): boolean {
	try {
		const { protocol } = new URL(url);
		return protocol === "http:" || protocol === "https:";
	} catch {
		return false;
	}
}

/** Schemes the renderer hands to the OS: web pages and mail, never file:, javascript: or app routes. */
export function isSystemBrowserLink(url: string): boolean {
	try {
		const { protocol } = new URL(url);
		return protocol === "http:" || protocol === "https:" || protocol === "mailto:";
	} catch {
		return false;
	}
}

function decodedPath(url: string): string {
	const trimmed = url.trim();
	let path = trimmed.split(/[?#]/, 1)[0];
	if (/^file:/i.test(trimmed)) {
		try {
			path = new URL(trimmed).pathname;
		} catch {
			return "";
		}
	}
	// A malformed percent escape stays as written; the preview endpoint will
	// reject anything it cannot resolve inside the session workspace.
	return decodePath(path).replace(/\\/g, "/");
}

function hasParentTraversal(path: string): boolean {
	return path.split("/").includes("..");
}

function comparableWorkspacePath(path: string): string {
	return path.replace(/^(?:(?:\.\/)+|\/+)/, "");
}

/** Resolve a displayed path to the workspace-relative path returned by Files. */
export function workspaceFilePath(url: string, workspacePaths: string[]): string | undefined {
	const path = decodedPath(url);
	if (!path || hasParentTraversal(path)) return undefined;
	const comparable = comparableWorkspacePath(path);
	const exact = workspacePaths.find((candidate) => comparable === candidate);
	if (exact) return exact;

	const absolute = /^file:/i.test(url.trim()) || path.startsWith("/") || /^[a-z]:\//i.test(path);
	if (!absolute) return undefined;
	return workspacePaths
		.filter((candidate) => comparable.endsWith(`/${candidate}`))
		.sort((left, right) => right.length - left.length)[0];
}

export function isWorkspaceFileLink(url: string, workspacePaths: string[]): boolean {
	return workspaceFilePath(url, workspacePaths) !== undefined;
}

/**
 * Whether a chat href looks like a local file path worth asking the daemon to
 * resolve. This does not grant filesystem access: the session preview endpoint
 * still confines the result to the workspace before serving it.
 */
export function isPotentialWorkspaceFileLink(url: string): boolean {
	const trimmed = url.trim();
	if (!trimmed || trimmed.startsWith("#") || isWebLink(trimmed)) return false;

	const path = decodedPath(trimmed);
	if (!path || hasParentTraversal(path) || path.startsWith("//")) return false;
	if (/^[a-z][a-z\d+.-]*:/i.test(path) && !/^[a-z]:\//i.test(path)) return false;

	return path.includes("/") || /^\.[^/]+$/.test(path) || /\.[a-z\d][a-z\d._-]*$/i.test(path);
}

export async function openLinkInSystemBrowser(url: string): Promise<void> {
	try {
		await aoBridge.app.openExternal(url);
	} catch (error) {
		console.warn("Unable to open link in system browser", error);
	}
}

export function handleModifierLinkClick(event: MouseEvent): void {
	if (event.button !== 0 || !event.altKey || event.defaultPrevented) return;
	const anchor = event.target instanceof Element ? event.target.closest("a[href]") : null;
	const href = anchor?.getAttribute("href");
	if (!href) return;

	let url: URL;
	try {
		url = new URL(href, window.location.href);
	} catch {
		return;
	}
	if (!["http:", "https:"].includes(url.protocol) || url.origin === window.location.origin) return;

	event.preventDefault();
	void openLinkInSystemBrowser(url.href);
}
