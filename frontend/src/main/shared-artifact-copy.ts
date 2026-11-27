/**
 * Copies a link someone shared with the person, so a session can work from it.
 *
 * The renderer gets the link's contentUrl from viewArtifactShare with the
 * person's sign-in; a private link's carries a short-lived view token. The file
 * host is cross-origin to the renderer, so the bytes are read here. A shared
 * HTML page takes the files publishing included with it (the images and other
 * files its markup names by relative path, cloud-share-page-files.ts), each
 * read from where the link serves it, beside the page, so the copy holds what
 * the link shows. A file the link does not serve is reported, not silently
 * left out.
 */

import { createHash } from "node:crypto";
import { isSharePage, MAX_PAGE_FILES, pageFileReferences, servedPath } from "./cloud-share-page-files";

/**
 * A copy's total bytes: the daemon's limit for one import
 * (maxArtifactImportBytes). The spawn request carries it as base64 beside the
 * message's own attachments under one body cap (maxSpawnBodyBytes, about
 * 135 MiB), so a copy leaves room for them.
 */
export const MAX_SHARED_COPY_BYTES = 48 * 1024 * 1024;

/** One file's bytes: the daemon's limit for each imported file (attachmentstore.MaxFileBytes). */
export const MAX_SHARED_COPY_FILE_BYTES = 50 * 1024 * 1024;

/**
 * Longest file or folder name a copy writes, in UTF-8 bytes: file systems
 * allow 255, and the daemon writes each file first under a temporary name
 * about 30 bytes longer.
 */
const MAX_NAME_BYTES = 200;

/** Names the daemon's artifact list leaves out (sessionartifacts.ignoredNames), so it refuses to import them. */
const IGNORED_NAMES = new Set(["node_modules", "__pycache__", "thumbs.db", "desktop.ini"]);

/** Names Windows reserves for devices, with or without an extension. */
const WINDOWS_RESERVED = /^(con|prn|aux|nul|conin\$|conout\$|com[0-9¹²³]|lpt[0-9¹²³])\s*(\.|:|$)/i;

/** Where a copy goes inside a session's artifact folder. */
export const SHARED_COPY_ROOT = "shared";

export interface SharedArtifactCopyFile {
	/** Slash-separated, relative to the session's artifact folder. */
	path: string;
	/** Standard base64. */
	data: string;
}

export interface SharedArtifactCopy {
	/** The page or file itself, relative to the artifact folder. */
	entry: string;
	/** The folder the copy is in, relative to the artifact folder. */
	dir: string;
	files: SharedArtifactCopyFile[];
	/** Page files the link did not serve, as the page names them. */
	missing: string[];
	/** A hash of every copied path and its bytes, to tell a real change from a new date. */
	fingerprint: string;
}

export class SharedArtifactCopyError extends Error {
	constructor(readonly code: "invalid" | "unavailable" | "too_large", message: string) {
		super(message);
		this.name = "SharedArtifactCopyError";
	}
}

type Fetch = (url: string, init?: { redirect?: "follow" | "manual" }) => Promise<Response>;

/** Longest name of a copy's own folder or file, in UTF-8 bytes. */
const MAX_COPY_NAME_BYTES = 120;

/** text cut to at most max UTF-8 bytes, never inside a character. */
function cutToBytes(text: string, max: number): string {
	let out = "";
	let bytes = 0;
	for (const char of text) {
		bytes += Buffer.byteLength(char);
		if (bytes > max) break;
		out += char;
	}
	return out;
}

/**
 * A file or folder name that is safe in every file system and never hidden:
 * at most maxBytes UTF-8 bytes, since APFS and ext4 count a name's bytes, and
 * 80 Chinese characters or emoji are already 240 to 320 of them.
 */
export function safeName(raw: string, fallback: string, maxBytes = MAX_COPY_NAME_BYTES): string {
	const cleaned = cutToBytes(raw.normalize("NFC").replace(/[\u0000-\u001f\u007f<>:"/\\|?*]+/g, "-").replace(/^[.\s-]+/, ""), maxBytes)
		// After the cut, so a name never ends in a dot or space, which Windows drops.
		.replace(/[.\s]+$/, "");
	return cleaned && !WINDOWS_RESERVED.test(cleaned) && !IGNORED_NAMES.has(cleaned.toLowerCase()) ? cleaned : fallback;
}

/** A file name cleaned as safeName does, keeping its extension when the name is long or reserved. */
function safeFileName(raw: string, fallback: string): string {
	const match = /^(.*?)(\.[A-Za-z0-9]{1,10})?$/.exec(raw.normalize("NFC"));
	const extension = match?.[2] ?? "";
	return `${safeName(match?.[1] ?? raw, fallback, MAX_COPY_NAME_BYTES - extension.length)}${extension}`;
}

/**
 * Whether path, slash-separated inside the artifact folder, is one the daemon
 * imports (sessionartifacts.CleanImportPath) and this computer's file system
 * can hold. A page file that is not lands in missing instead of failing the
 * whole copy: a page made on a Mac may name "chart-10:00.png", which Windows
 * cannot.
 */
export function validCopyPath(path: string, platform: NodeJS.Platform = process.platform): boolean {
	return path.split("/").every((part) => {
		if (part === "" || part === ".." || part.startsWith(".") || /[\\\u0000]/.test(part)) return false;
		if (IGNORED_NAMES.has(part.toLowerCase()) || Buffer.byteLength(part) > MAX_NAME_BYTES) return false;
		// The daemon refuses a trailing dot or space everywhere: Windows drops it, so two names could become one.
		if (/[.\s]$/.test(part)) return false;
		if (platform !== "win32") return true;
		return !/[\u0001-\u001f<>:"|?*]/.test(part) && !WINDOWS_RESERVED.test(part);
	});
}

/** The folder name for a shared file: its name without the extension. */
export function copyFolderName(name: string): string {
	const base = name.replace(/\.[^.]+$/, "");
	return safeName(base, "artifact");
}

/**
 * The link's file address: https, or a local http address only for a dev
 * build's own control plane.
 */
function sharedURL(contentUrl: string, allowLocal: boolean): URL {
	let url: URL;
	try {
		url = new URL(contentUrl);
	} catch {
		throw new SharedArtifactCopyError("invalid", "Invalid shared artifact URL");
	}
	const local = url.hostname === "localhost" || url.hostname === "127.0.0.1";
	if (url.username || url.password || (url.protocol !== "https:" && !(allowLocal && url.protocol === "http:" && local))) {
		throw new SharedArtifactCopyError("invalid", "Invalid shared artifact URL");
	}
	return url;
}

/** Whether a response came from the page's own site, not one a redirect led to. */
function stayedOn(response: Response, page: URL): boolean {
	if (!response.url) return true;
	try {
		return new URL(response.url).origin === page.origin;
	} catch {
		return false;
	}
}

const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

/**
 * Fetches url, following a redirect only while it stays on the page's own
 * site: one leading anywhere else is never requested, so neither the view
 * token nor the app's cookies go there. "moved" when a redirect leaves.
 */
async function fetchSameSite(fetcher: Fetch, url: string, page: URL): Promise<{ response: Response; url: string } | "moved"> {
	let next = url;
	for (let hops = 0; hops <= 5; hops++) {
		const response = await fetcher(next, { redirect: "manual" });
		if (response.type === "opaqueredirect") return "moved";
		if (!REDIRECT_STATUSES.has(response.status)) return stayedOn(response, page) ? { response, url: next } : "moved";
		const location = response.headers.get("location");
		if (!location) return { response, url: next };
		const target = new URL(location, next);
		if (target.origin !== page.origin) return "moved";
		next = target.toString();
	}
	return "moved";
}

/** The body, read only up to budget bytes: a reply with no length is never held whole first. */
async function readBounded(response: Response, budget: number): Promise<Uint8Array> {
	const tooLarge = () => new SharedArtifactCopyError("too_large", "The shared artifact is too large to copy");
	const declared = Number(response.headers.get("content-length"));
	if (Number.isFinite(declared) && declared > budget) throw tooLarge();
	if (!response.body) return new Uint8Array(0);
	const reader = response.body.getReader();
	const chunks: Uint8Array[] = [];
	let size = 0;
	for (;;) {
		const { done, value } = await reader.read();
		if (done) break;
		size += value.byteLength;
		if (size > budget) {
			await reader.cancel().catch(() => undefined);
			throw tooLarge();
		}
		chunks.push(value);
	}
	const bytes = new Uint8Array(size);
	let offset = 0;
	for (const chunk of chunks) {
		bytes.set(chunk, offset);
		offset += chunk.byteLength;
	}
	return bytes;
}

/**
 * A page file's URL: resolved against the page, as the browser resolves it,
 * carrying the page's view token, which the file host may ask of every file.
 */
function pageFileURL(page: URL, served: string, withToken: boolean): string {
	const url = new URL(served.split("/").map(encodeURIComponent).join("/"), page);
	url.search = withToken ? page.search : "";
	url.hash = "";
	return url.toString();
}

/** A page file's bytes, or null when the link does not serve it or it is over budget. */
async function fetchPageFile(fetcher: Fetch, page: URL, served: string, budget: number): Promise<Uint8Array | null> {
	for (const withToken of page.search ? [true, false] : [false]) {
		const url = pageFileURL(page, served, withToken);
		if (new URL(url).origin !== page.origin) return null;
		let fetched: Awaited<ReturnType<typeof fetchSameSite>>;
		try {
			fetched = await fetchSameSite(fetcher, url, page);
		} catch {
			continue;
		}
		if (fetched === "moved" || !fetched.response.ok) continue;
		try {
			return await readBounded(fetched.response, budget);
		} catch {
			return null;
		}
	}
	return null;
}

/** Where a copy is written: a new one's own folder, or, for an update, the folder and entry the first copy used. */
function copyTarget(name: string, into?: { dir: string; entry: string }): { dir: string; entry: string } {
	if (!into) {
		const dir = `${SHARED_COPY_ROOT}/${copyFolderName(name)}`;
		const entry = `${dir}/${safeFileName(name, "artifact")}`;
		// safeName keeps to the daemon's rules already; this is the last word.
		if (validCopyPath(entry)) return { dir, entry };
		return { dir: `${SHARED_COPY_ROOT}/artifact`, entry: `${SHARED_COPY_ROOT}/artifact/artifact` };
	}
	if (!into.dir.startsWith(`${SHARED_COPY_ROOT}/`) || !validCopyPath(into.dir) || !validCopyPath(into.entry) || !into.entry.startsWith(`${into.dir}/`) || into.entry.slice(into.dir.length + 1).includes("/")) {
		throw new SharedArtifactCopyError("invalid", "Invalid copy folder");
	}
	return into;
}

/**
 * Copies the link. An update passes into, the first copy's folder and entry,
 * so the owner renaming the artifact replaces that copy rather than adding a
 * second one beside it.
 */
export async function copySharedArtifact(
	fetcher: Fetch,
	input: { contentUrl: string; name: string; into?: { dir: string; entry: string } },
	options: { allowLocal?: boolean } = {},
): Promise<SharedArtifactCopy> {
	const page = sharedURL(input.contentUrl, options.allowLocal ?? false);
	const { dir, entry } = copyTarget(input.name, input.into);
	let fetched: Awaited<ReturnType<typeof fetchSameSite>>;
	try {
		fetched = await fetchSameSite(fetcher, page.toString(), page);
	} catch {
		throw new SharedArtifactCopyError("unavailable", "Could not download the shared artifact");
	}
	if (fetched === "moved") throw new SharedArtifactCopyError("unavailable", "The shared artifact moved to another site");
	const { response } = fetched;
	if (!response.ok) throw new SharedArtifactCopyError("unavailable", `Could not download the shared artifact (${response.status})`);
	// Page files resolve against where the page was served from, after any
	// redirect on its own site, as a browser resolves them; the view token goes along.
	const pageBase = new URL(fetched.url);
	pageBase.search = page.search;
	const main = await readBounded(response, Math.min(MAX_SHARED_COPY_FILE_BYTES, MAX_SHARED_COPY_BYTES));
	let total = main.byteLength;
	const copied = new Map<string, Uint8Array>([[entry, main]]);
	const missing: string[] = [];
	if (isSharePage(input.name)) {
		const html = new TextDecoder().decode(main);
		// Case-insensitive file systems (the Windows and macOS defaults) hold
		// one of two names that differ only in case: the second is reported.
		// Names are compared in one Unicode form, as macOS compares them, but
		// kept as the page spells them, which Windows and Linux match exactly.
		// Lower-cased one character at a time, as the daemon's strings.ToLower
		// does: whole-string toLowerCase applies the Greek final-sigma rule, and
		// turns "İ" into "i" plus a combining dot.
		const sameName = (path: string) => [...path.normalize("NFC")].map((char) => char.toLowerCase()).join("").replace(/i̇/g, "i");
		const seen = new Map<string, string>([[sameName(entry), entry]]);
		const folders = new Set<string>();
		// Downloads tried, found or not: past MAX_PAGE_FILES, the rest are reported, never fetched.
		let attempted = 0;
		for (const reference of pageFileReferences(html)) {
			const served = servedPath(reference.url);
			if (!served || "skip" in served) continue;
			const local = `${dir}/${served.path}`;
			const earlier = seen.get(sameName(local));
			if (earlier === local) continue;
			// The cheap checks first, so a reference that is refused costs little.
			if (earlier !== undefined || !validCopyPath(local) || attempted >= MAX_PAGE_FILES) {
				missing.push(reference.url);
				continue;
			}
			// A name that is already a file can't also be a folder, nor the other way round.
			const parts = local.split("/");
			const parents = parts.slice(0, -1).map((_, index) => sameName(parts.slice(0, index + 1).join("/")));
			if (folders.has(sameName(local)) || parents.some((parent) => seen.has(parent))) {
				missing.push(reference.url);
				continue;
			}
			seen.set(sameName(local), local);
			for (const parent of parents) folders.add(parent);
			attempted++;
			const bytes = await fetchPageFile(fetcher, pageBase, served.path, Math.min(MAX_SHARED_COPY_FILE_BYTES, MAX_SHARED_COPY_BYTES - total));
			if (!bytes) {
				missing.push(reference.url);
				continue;
			}
			total += bytes.byteLength;
			copied.set(local, bytes);
		}
	}
	// Over paths inside the copy's folder, in code-unit order, so neither the
	// folder's name nor the system's language changes it.
	const hash = createHash("sha256");
	const files: SharedArtifactCopyFile[] = [];
	for (const [path, bytes] of [...copied].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) {
		const inside = path === entry ? "" : path.slice(dir.length + 1);
		hash.update(`${inside}\0${bytes.byteLength}\0`);
		hash.update(bytes);
		files.push({ path, data: Buffer.from(bytes).toString("base64") });
	}
	// What the link did not serve counts too, so a page file gone for good is
	// the same version every time; a passing failure is checked again by the caller.
	for (const reference of [...missing].sort()) hash.update(`missing\0${reference}\0`);
	return { entry, dir, files, missing, fingerprint: hash.digest("hex") };
}
