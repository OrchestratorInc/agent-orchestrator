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
	return decodePath(path).replace(/\\/g, "/");
}

function hasParentTraversal(path: string): boolean {
	return path.split("/").includes("..");
}

function comparableWorkspacePath(path: string): string {
	return path.replace(/^(?:(?:\.\/)+|\/+)/, "");
}

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
