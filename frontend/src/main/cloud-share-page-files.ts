/**
 * The files a shared HTML page uses: the images and other files it refers to
 * by relative path, which its share link serves beside it, each at the path
 * the browser asks for once it resolves the page's relative URL. A page is
 * served at its link's root, so "images/a.png" in the page is served at
 * /images/a.png and read from images/a.png next to the page in the session's
 * artifacts folder.
 *
 * Only files inside the page's own folder can be included: one a page
 * reaches with "..", or by an absolute path, would be asked for at another
 * path than the one it has here. Video is not included yet: the share domain
 * goes through a CDN whose terms do not allow serving video. Anything not
 * included is reported, so the person knows the shared page will miss it.
 * External URLs (http:, data: and the like) are left alone: the shared page
 * never loads them.
 */

import path from "node:path";
import { isUnplayableShareVideo } from "../shared/cloud-share";
import { CloudShareError, resolveShareFile, type ResolvedShareFile } from "./cloud-share";

/** The most files a link serves beside its own (the control plane's limit). */
export const MAX_PAGE_FILES = 200;

/** Why a file the page uses is not included. */
export type PageFileSkipReason =
	/** A video the control plane refused: it does not serve videos yet. */
	| "video"
	/** A video in a format browsers do not play (isUnplayableShareVideo). */
	| "video_format"
	/** Outside the page's folder, by "..", or an absolute path. */
	| "outside"
	/** Not a file in the session's artifacts. */
	| "missing"
	/** A path the share host keeps for itself, or that it cannot serve. */
	| "unservable"
	/** Past MAX_PAGE_FILES. */
	| "limit"
	/**
	 * An SVG <use> of another file's symbol: a shared page is sandboxed, so
	 * its origin is opaque and the browser never loads another file's symbol
	 * into it.
	 */
	| "svg_use";

export interface PageFile {
	/** Where the link serves it, relative to its root. */
	path: string;
	/**
	 * The path the page names it by in the session's artifacts, before any
	 * symlink is followed: file is what it resolves to now.
	 */
	referencedPath: string;
	file: ResolvedShareFile;
}

export interface PageFileSkip {
	/** The reference as the page writes it. */
	reference: string;
	reason: PageFileSkipReason;
	/** For a missing file, where it would be in the session's artifacts. */
	relativePath?: string;
}

/** A URL the page loads a file from, and whether an SVG <use> names it. */
export interface PageReference {
	url: string;
	svgUse?: true;
}

/** Whether a file is shared as a page whose files go with it. */
export function isSharePage(name: string): boolean {
	return /\.html?$/i.test(name);
}

const ENTITIES: Record<string, string> = { amp: "&", quot: '"', apos: "'", lt: "<", gt: ">" };

function decodeEntities(value: string): string {
	return value.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (whole, name: string) => {
		if (name[0] === "#") {
			const code = name[1] === "x" || name[1] === "X" ? Number.parseInt(name.slice(2), 16) : Number.parseInt(name.slice(1), 10);
			return Number.isFinite(code) && code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : whole;
		}
		return ENTITIES[name.toLowerCase()] ?? whole;
	});
}

const ATTRIBUTE = /([^\s"'<>/=]+)\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/g;

function attributes(tag: string): Map<string, string> {
	const found = new Map<string, string>();
	for (const match of tag.matchAll(ATTRIBUTE)) {
		const name = match[1].toLowerCase();
		if (!found.has(name)) found.set(name, decodeEntities(match[3] ?? match[4] ?? match[5] ?? ""));
	}
	return found;
}

/** The URLs a srcset lists, without their width or density descriptors. */
function srcsetURLs(value: string): string[] {
	return value.split(",").map((candidate) => candidate.trim().split(/\s+/)[0]).filter(Boolean);
}

const CSS_URL = /url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)/gi;

function cssURLs(css: string): string[] {
	return [...css.matchAll(CSS_URL)].map((match) => match[1] ?? match[2] ?? match[3] ?? "").filter(Boolean);
}

/**
 * Every URL the page loads a file from by markup: images (src, srcset, an
 * <svg> <image> or <use>), a <video>'s poster, <audio>, <track>, icons, a
 * legacy background attribute, and url() in its style blocks and style
 * attributes. Scripts are not read, nor are URLs a script builds, nor what an
 * HTML comment holds.
 */
export function pageFileReferences(html: string): PageReference[] {
	const markup = html.replace(/<!--[\s\S]*?-->/g, "").replace(/<script\b[\s\S]*?<\/script\s*>/gi, "");
	const found: PageReference[] = [];
	const push = (...urls: string[]) => found.push(...urls.map((url) => ({ url })));
	for (const style of markup.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style\s*>/gi)) push(...cssURLs(style[1]));
	for (const match of markup.matchAll(/<([a-z][a-z0-9-]*)\b((?:[^>"']|"[^"]*"|'[^']*')*)>/gi)) {
		const tag = match[1].toLowerCase();
		const attrs = attributes(match[2]);
		const add = (name: string) => {
			const value = attrs.get(name);
			if (value) push(value);
		};
		switch (tag) {
			case "img":
			case "source":
				add("src");
				push(...srcsetURLs(attrs.get("srcset") ?? ""));
				break;
			case "video":
				add("src");
				add("poster");
				break;
			case "audio":
			case "track":
			case "input":
				add("src");
				break;
			case "image":
				add("href");
				add("xlink:href");
				break;
			case "use":
				for (const name of ["href", "xlink:href"]) {
					const value = attrs.get(name);
					if (value) found.push({ url: value, svgUse: true });
				}
				break;
			case "link":
				if (/\bicon\b/i.test(attrs.get("rel") ?? "")) add("href");
				break;
		}
		add("background");
		const style = attrs.get("style");
		if (style) push(...cssURLs(style));
	}
	return found;
}

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;
/** A base whose first segment marks the page's folder, to see what leaves it. */
const PAGE_BASE = "https://page.invalid/p/";

/**
 * Where the link serves a reference, relative to its root, as the browser asks
 * for it; why it cannot; or null for a reference that loads no file of the
 * page's (a fragment, an external or data URL).
 */
export function servedPath(reference: string): { path: string } | { skip: PageFileSkipReason } | null {
	const trimmed = reference.trim();
	if (trimmed === "" || trimmed.startsWith("#") || SCHEME.test(trimmed) || trimmed.startsWith("//")) return null;
	if (trimmed.startsWith("/") || trimmed.startsWith("\\")) return { skip: "outside" };
	let resolved: URL;
	try {
		resolved = new URL(trimmed, PAGE_BASE);
	} catch {
		return { skip: "unservable" };
	}
	if (resolved.origin !== new URL(PAGE_BASE).origin || !resolved.pathname.startsWith("/p/")) return { skip: "outside" };
	let segments: string[];
	try {
		segments = resolved.pathname.slice("/p/".length).split("/").map((segment) => decodeURIComponent(segment));
	} catch {
		return { skip: "unservable" };
	}
	const served = segments.join("/");
	return validServedPath(served) ? { path: served } : { skip: "unservable" };
}

/** The control plane's rule for an included file's path (ValidArtifactShareFilePath). */
function validServedPath(served: string): boolean {
	if (served === "" || served === "raw" || Buffer.byteLength(served) > 1024) return false;
	if ([...served].some((char) => char < " " || char === "\x7f" || char === "\\")) return false;
	const segments = served.split("/");
	if (segments[0] === "_ao" || segments[0].startsWith("~")) return false;
	return segments.every((segment) => segment !== "" && segment !== "." && segment !== ".." && Buffer.byteLength(segment) <= 255);
}

/**
 * The files the page at pageRelativePath (slash-separated, in the session's
 * artifacts, as resolveShareFile names it once symlinks are followed) uses,
 * each resolved strictly inside the page's folder, symlinks followed too, and
 * what it uses that cannot be included.
 * Each served path is listed once, in the order the page first uses it.
 */
export async function collectPageFiles(
	dataDir: string,
	sessionId: string,
	pageRelativePath: string,
	html: string,
): Promise<{ files: PageFile[]; skipped: PageFileSkip[] }> {
	const folder = path.posix.dirname(pageRelativePath);
	const files: PageFile[] = [];
	const skipped: PageFileSkip[] = [];
	const seen = new Set<string>();
	for (const { url: reference, svgUse } of pageFileReferences(html)) {
		const result = servedPath(reference);
		// A fragment alone names a symbol in the page itself, which works.
		if (result === null) continue;
		const key = "path" in result ? `${svgUse ? "use" : "path"}:${result.path}` : `ref:${reference}`;
		if (seen.has(key)) continue;
		seen.add(key);
		if ("skip" in result) {
			skipped.push({ reference, reason: result.skip });
			continue;
		}
		if (svgUse) {
			skipped.push({ reference, reason: "svg_use" });
			continue;
		}
		const served = result.path;
		// A playable video goes to the control plane, which may still refuse it.
		if (isUnplayableShareVideo(served)) {
			skipped.push({ reference, reason: "video_format" });
			continue;
		}
		const relative = folder === "." ? served : `${folder}/${served}`;
		if (relative === pageRelativePath) continue;
		let file: ResolvedShareFile;
		try {
			file = await resolveShareFile(dataDir, sessionId, relative);
		} catch (error) {
			if (!(error instanceof CloudShareError)) throw error;
			skipped.push(error.code === "file_missing" ? { reference, reason: "missing", relativePath: relative } : { reference, reason: "outside" });
			continue;
		}
		// A symlink in the page's folder may lead out of it: only what is in it goes.
		if (folder !== "." && !file.relativePath.startsWith(`${folder}/`)) {
			skipped.push({ reference, reason: "outside" });
			continue;
		}
		if (files.length >= MAX_PAGE_FILES) {
			skipped.push({ reference, reason: "limit" });
			continue;
		}
		files.push({ path: served, referencedPath: relative, file });
	}
	return { files, skipped };
}
