/**
 * A session's artifacts: the files an agent leaves in the session's own
 * artifact directory, outside the git workspace. Mirrors the desktop's
 * Artifacts inspector (frontend SessionArtifactsView) for the phone.
 */

import { deliverableArtifacts, type ArtifactFacts } from "../../shared/artifacts/deliverables";
import type { FileAnnotationTarget } from "../../shared/files/file-annotations";
import { fileMediaKind, isDelimitedTextPath, isMermaidPath, isRasterImagePath, isSvgPath } from "../../shared/files/file-formats";

/** The daemon's classification of an artifact file. */
export type ArtifactKind = "html" | "markdown" | "file";

/** One file in a session's artifact directory, as the daemon lists it. */
export type SessionArtifact = {
	/** Artifact-directory-relative path; the id every artifact route takes. */
	path: string;
	name: string;
	kind: ArtifactKind;
	size: number;
	updatedAt: string;
	/** The file's public link, when someone published it from the desktop's Share. */
	shareUrl?: string;
	/** Set on a file the agent registered with `ao artifact add`. */
	registered?: boolean;
};

/**
 * How the viewer draws a file: the same formats the desktop previews (see
 * shared/files/file-formats), plus HEIC photos, which the phone decodes.
 * `binary` is a format known to have no preview, which the viewer says
 * instead of reading it.
 */
export type ArtifactViewKind = "html" | "markdown" | "image" | "svg" | "video" | "audio" | "pdf" | "table" | "diagram" | "binary" | "text";

/**
 * The daemon's reserved prefix that routes a preview-file request to the
 * session's artifact directory. The daemon keeps this form for compatibility,
 * so the app uses it only where it must: a URL a WebView loads by itself (an
 * SVG, a video, an unlisted file), whose auth is the preview-files cookie
 * scoped to that route, and a page's raw HTML. Reads the app makes with the
 * Bearer header use the artifact-files route (artifactFilesPath).
 */
const ARTIFACT_ENTRY_PREFIX = "__ao_artifacts__";

/** Text files larger than this are not loaded into the viewer. */
export const MAX_ARTIFACT_TEXT_BYTES = 1 << 20;

/**
 * The most text the viewer lays out at once. A text view is laid out whole, on
 * Android on the UI thread, so a long file shows its start.
 */
export const MAX_SHOWN_TEXT_CHARS = 200_000;

// iPhone photos: the desktop does not preview them, the phone's image decoder does.
const PHONE_IMAGE = /\.(heic|heif)$/i;
const MARKDOWN = /\.(md|markdown)$/i;
// Files known to be binary: archives, packages, office documents, databases,
// data and model formats, fonts. They have no text to copy and no versions.
// Any other extension counts as text; the viewer reads the file and shows it
// unless it reads as binary, as desktop does.
const BINARY_EXTENSION = /\.(zip|gz|tgz|tar|bz2|xz|zst|7z|rar|jar|war|apk|aab|ipa|dmg|iso|exe|dll|so|dylib|bin|o|a|class|wasm|pyc|doc|docx|xls|xlsx|ppt|pptx|odt|ods|odp|pages|numbers|key|epub|sqlite|sqlite3|db|parquet|avro|orc|feather|arrow|npy|npz|pkl|pickle|h5|hdf5|onnx|pt|pth|safetensors|ckpt|woff|woff2|ttf|otf|eot|psd|ai|sketch|fig|blend|glb|fbx)$/i;

/** Reads the wire `artifactFiles`, dropping anything malformed. */
export function parseArtifacts(raw: unknown): SessionArtifact[] {
	if (!Array.isArray(raw)) return [];
	const out: SessionArtifact[] = [];
	for (const item of raw) {
		if (!item || typeof item !== "object") continue;
		const { path, name, kind, size, updatedAt, shareUrl, registered } = item as Record<string, unknown>;
		if (typeof path !== "string" || !isSafeArtifactPath(path)) continue;
		const link = artifactShareUrl(shareUrl);
		out.push({
			path,
			name: typeof name === "string" && name ? name : path.split("/").pop() || path,
			kind: kind === "html" || kind === "markdown" ? kind : "file",
			size: typeof size === "number" && size >= 0 ? size : 0,
			updatedAt: typeof updatedAt === "string" ? updatedAt : "",
			...(link ? { shareUrl: link } : {}),
			...(registered === true ? { registered } : {}),
		});
	}
	return out;
}

const MAX_SHARE_URL_LENGTH = 512;

/** An http(s) URL of sensible length, as the desktop keeps share links; anything else is not one. */
export function artifactShareUrl(value: unknown): string | undefined {
	if (typeof value !== "string" || value.length > MAX_SHARE_URL_LENGTH) return undefined;
	try {
		const { protocol } = new URL(value);
		return protocol === "https:" || protocol === "http:" ? value : undefined;
	} catch {
		return undefined;
	}
}

/** Newest first; equal times by path, so the order holds between polls. */
export function sortArtifacts(artifacts: readonly SessionArtifact[]): SessionArtifact[] {
	return [...artifacts].sort((a, b) => timeOf(b.updatedAt) - timeOf(a.updatedAt) || a.path.localeCompare(b.path));
}

/** Non-empty, relative, and without a `.`/`..` segment or backslash. */
export function isSafeArtifactPath(path: string): boolean {
	return path !== "" && !path.startsWith("/") && !path.includes("\\") && !path.split("/").some((part) => part === "" || part === "." || part === "..");
}

export function artifactViewKind(artifact: Pick<SessionArtifact, "path" | "kind">): ArtifactViewKind {
	const { path } = artifact;
	if (artifact.kind === "html") return "html";
	if (artifact.kind === "markdown" || MARKDOWN.test(path)) return "markdown";
	if (isRasterImagePath(path) || PHONE_IMAGE.test(path)) return "image";
	if (isSvgPath(path)) return "svg";
	const media = fileMediaKind(path);
	if (media === "video" || media === "audio" || media === "pdf") return media;
	if (isDelimitedTextPath(path)) return "table";
	if (isMermaidPath(path)) return "diagram";
	if (BINARY_EXTENSION.test(path)) return "binary";
	return "text";
}

/** Kinds whose file is text: an SVG, a table and a diagram are drawn from their source. */
const TEXT_KINDS: ReadonlySet<ArtifactViewKind> = new Set(["html", "markdown", "svg", "table", "diagram", "text"]);

/**
 * Whether the file is taken as text, for Copy, Version history and a card's
 * preview: a page, a document, an SVG, a table, a diagram, or any file without
 * a known media or binary extension (an archive, an office document, a
 * database). The viewer still reads an unknown file and decides by its contents.
 */
export function isTextArtifact(artifact: Pick<SessionArtifact, "path" | "kind">): boolean {
	return TEXT_KINDS.has(artifactViewKind(artifact));
}

/** Whether the file's contents can be copied as text. */
export function canCopyArtifact(artifact: Pick<SessionArtifact, "path" | "kind">): boolean {
	return isTextArtifact(artifact);
}

/** The start of a long text, cut at a line break, for the viewer to lay out. */
export function shownText(text: string, max = MAX_SHOWN_TEXT_CHARS): { text: string; truncated: boolean } {
	if (text.length <= max) return { text, truncated: false };
	const lineEnd = text.lastIndexOf("\n", max);
	return { text: text.slice(0, lineEnd > max / 2 ? lineEnd : max), truncated: true };
}

/**
 * Whether a file keeps a version history the phone can show. The daemon reads
 * versions back as text, so images, media and binary files have none (as on
 * desktop, which hides History when the file reads as binary).
 */
export function canShowArtifactHistory(artifact: Pick<SessionArtifact, "path" | "kind">): boolean {
	return isTextArtifact(artifact);
}

/** One entry in an artifact's long-press or header menu. */
export type ArtifactMenuAction = "open" | "share-link" | "copy-link" | "ask" | "copy" | "history" | "delete";

/**
 * What an artifact's menu offers, in order. The viewer already shows the file,
 * so it leaves out Open. A published file offers its public link.
 */
export function artifactMenuActions(artifact: Pick<SessionArtifact, "path" | "kind" | "size" | "shareUrl">, { inViewer = false }: { inViewer?: boolean } = {}): ArtifactMenuAction[] {
	return [
		...(inViewer ? [] : ["open" as const]),
		...(artifact.shareUrl ? ["share-link" as const, "copy-link" as const] : []),
		// Any file can be asked about as a whole; text can also be quoted from the viewer.
		"ask",
		...(canCopyArtifact(artifact) && artifact.size <= MAX_ARTIFACT_TEXT_BYTES ? ["copy" as const] : []),
		...(canShowArtifactHistory(artifact) ? ["history" as const] : []),
		"delete",
	];
}

/**
 * The preview-files path for one artifact, each segment escaped, for a WebView
 * to load by URL (see ARTIFACT_ENTRY_PREFIX). Undefined for a path that could
 * leave the artifact directory. `raw` asks for a file's source; without it the
 * daemon serves markdown rendered as a page.
 */
export function artifactFilePath(sessionId: string, path: string, { raw = false }: { raw?: boolean } = {}): string | undefined {
	if (!isSafeArtifactPath(path)) return undefined;
	const escaped = [ARTIFACT_ENTRY_PREFIX, ...path.split("/")].map(encodeURIComponent).join("/");
	return `/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${escaped}${raw ? "?raw=true" : ""}`;
}

/**
 * What a comment on an artifact is about, as the desktop's artifact viewer
 * sends it: the file by name and path, and the selected text when there is
 * one. Without a quote the comment is about the whole file.
 */
export function artifactCommentTarget(artifact: Pick<SessionArtifact, "path" | "name">, quote?: string): FileAnnotationTarget {
	const selected = quote?.trim();
	return { source: artifact.name, path: artifact.path, side: "file", ...(selected ? { quote: selected } : {}) };
}

/**
 * A path a document links to (an image, a stylesheet), resolved against the
 * document's own folder in the artifact directory. Undefined for a URL, a
 * rooted path, or one that climbs out of the artifact directory.
 */
export function resolveArtifactAssetPath(fromPath: string, ref: string): string | undefined {
	const trimmed = ref.trim();
	if (!trimmed || /^[a-z][a-z0-9+.-]*:/i.test(trimmed) || trimmed.startsWith("/") || trimmed.startsWith("#")) return undefined;
	const bare = trimmed.split(/[?#]/)[0] ?? "";
	let decoded = bare;
	try {
		decoded = decodeURIComponent(bare);
	} catch {
		// A stray % keeps the reference as written.
	}
	const parts = fromPath.split("/").slice(0, -1);
	for (const segment of decoded.split("/")) {
		if (segment === "" || segment === ".") continue;
		if (segment === "..") {
			if (!parts.length) return undefined;
			parts.pop();
		} else parts.push(segment);
	}
	const resolved = parts.join("/");
	return isSafeArtifactPath(resolved) ? resolved : undefined;
}

/**
 * The artifact-files path for one artifact, served from the artifact folder
 * only. An HTML file comes back as a page, with AO's theme variables, font and
 * viewport written in (the render bootstrap, which pages written for AO style
 * against; the desktop frames artifacts from this route). Any other file comes
 * back as its bytes, markdown as its source.
 */
export function artifactFilesPath(sessionId: string, path: string): string | undefined {
	if (!isSafeArtifactPath(path)) return undefined;
	return `/api/v1/sessions/${encodeURIComponent(sessionId)}/artifact-files/${path.split("/").map(encodeURIComponent).join("/")}`;
}

/** Decoded text that is really a binary file: a NUL, or mostly undecodable bytes. */
export function looksBinary(text: string): boolean {
	if (text.includes("\u0000")) return true;
	const sample = text.slice(0, 4096);
	if (!sample) return false;
	let replaced = 0;
	for (const char of sample) if (char === "�") replaced += 1;
	return replaced / sample.length > 0.1;
}

/** The code language a text artifact highlights as, from its extension. */
export function artifactLanguage(path: string): string | undefined {
	const ext = /\.([a-z0-9]+)$/i.exec(path)?.[1]?.toLowerCase();
	return ext || undefined;
}

/** "1.2 MB", "340 KB", "12 B". */
export function formatArtifactSize(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** The file's extension in capitals, for a card that has no picture, e.g. "CSV". */
export function artifactExtensionLabel(path: string): string {
	const ext = /\.([a-z0-9]{1,6})$/i.exec(path)?.[1];
	return ext ? ext.toUpperCase() : "FILE";
}

/** What a file is, for a viewer that cannot draw it: "ZIP file · 2.4 MB" (without a size when it is not known). */
export function artifactFormatLabel(artifact: Pick<SessionArtifact, "path" | "size">): string {
	const ext = artifactExtensionLabel(artifact.path);
	const format = ext === "FILE" ? "File" : `${ext} file`;
	return artifact.size > 0 ? `${format} · ${formatArtifactSize(artifact.size)}` : format;
}

/**
 * The desktop's name for the system file manager on the machine's platform,
 * as its artifact menu says it ("Show in Finder"). Undefined when the
 * platform is not known (a machine connected by address only).
 */
export function machineFileManagerName(platform: string | undefined): string | undefined {
	const normalized = platform?.trim().toLowerCase() ?? "";
	if (normalized === "darwin" || normalized === "macos") return "Finder";
	if (normalized === "win32" || normalized === "windows") return "File Explorer";
	if (normalized === "linux") return "File Manager";
	return undefined;
}

/** Where a Cloud session's file the phone cannot show can be opened: there is no machine to name. */
export const OPEN_CLOUD_FILE_HINT = "Open it from the session's artifacts in the desktop app.";

/**
 * Where a file the phone cannot show can be opened: on the machine, from the
 * desktop app's artifact menu, named as that menu names it.
 */
export function openOnMachineHint(machineName: string | undefined, platform: string | undefined): string {
	const machine = machineName?.trim() || "the machine";
	const fileManager = machineFileManagerName(platform);
	// The menu item's name stays on one line: non-breaking spaces.
	const label = `Show in ${fileManager ?? ""}`.replace(/ /g, "\u00a0");
	return fileManager
		? `Open it on ${machine} with “${label}” in the desktop app.`
		: `Open it on ${machine} from the artifact's ⋯ menu in the desktop app.`;
}

/**
 * A file's full size from a range response's `Content-Range` ("bytes 0-0/4182"),
 * or undefined when the header does not give it ("bytes 0-0/*", missing).
 */
export function contentRangeTotal(header: string | null | undefined): number | undefined {
	const total = /\/(\d+)\s*$/.exec(header ?? "")?.[1];
	return total === undefined ? undefined : Number(total);
}

/**
 * How much of a file too large to load as text the viewer reads to tell text
 * from binary, as the desktop decides from the start of the file.
 */
export const ARTIFACT_PROBE_BYTES = 64 * 1024;

/**
 * The largest file the phone downloads to preview (a PDF on Android): past it,
 * the viewer says it is too large rather than fill the phone's storage.
 */
export const MAX_ARTIFACT_DOWNLOAD_BYTES = 100 * 1024 * 1024;

/** Downloaded artifacts kept in the cache at most, all files together. */
export const MAX_ARTIFACT_CACHE_BYTES = 200 * 1024 * 1024;

/**
 * Where a downloaded artifact is kept in the cache: `prefix` names the file
 * (paired machine, session and path; session ids are only unique on one
 * machine), `name` that version of it. An unchanged file opens from
 * the cache; a rewritten one downloads under a new name, and the old copy goes
 * (artifactCacheEvictions).
 */
export type ArtifactCacheEntry = { prefix: string; name: string };

/**
 * A 128-bit hash of `text` as four 32-bit lanes (cyrb128): wide enough that two
 * different files never share a cache name, where a 32-bit hash could collide
 * and open one file's cached copy for another.
 */
function hash128(text: string): number[] {
	let h1 = 1779033703, h2 = 3144134277, h3 = 1013904242, h4 = 2773480762;
	for (let index = 0; index < text.length; index += 1) {
		const k = text.charCodeAt(index);
		h1 = h2 ^ Math.imul(h1 ^ k, 597399067);
		h2 = h3 ^ Math.imul(h2 ^ k, 2869860233);
		h3 = h4 ^ Math.imul(h3 ^ k, 951274213);
		h4 = h1 ^ Math.imul(h4 ^ k, 2716044179);
	}
	h1 = Math.imul(h3 ^ (h1 >>> 18), 597399067);
	h2 = Math.imul(h4 ^ (h2 >>> 22), 2869860233);
	h3 = Math.imul(h1 ^ (h3 >>> 17), 951274213);
	h4 = Math.imul(h2 ^ (h4 >>> 19), 2716044179);
	h1 ^= h2 ^ h3 ^ h4;
	h2 ^= h1;
	h3 ^= h1;
	h4 ^= h1;
	return [h1, h2, h3, h4];
}

function hex(lanes: number[]): string {
	return lanes.map((lane) => (lane >>> 0).toString(16).padStart(8, "0")).join("");
}

/** `machine` is the paired machine's stable identity (machineIdentity). */
export function artifactCacheEntry(machine: string, sessionId: string, path: string, version: string): ArtifactCacheEntry {
	// 128 bits name the file; 64 tell its versions apart (only ever compared within one file).
	const prefix = hex(hash128(`${machine}\n${sessionId}\n${path}`));
	const ext = /\.([a-z0-9]{1,6})$/i.exec(path)?.[1]?.toLowerCase() ?? "bin";
	return { prefix, name: `${prefix}-${hex(hash128(version).slice(0, 2))}.${ext}` };
}

/** A file in the download cache, as the viewer finds it there. */
export type CachedArtifactFile = { name: string; size: number; modified: number };

/**
 * The cache files to delete once `keep` has downloaded: every other version
 * of the same file (and an interrupted download of one), then, while the cache
 * is over `maxBytes`, the least recently written other files. A download still
 * in progress is never deleted.
 */
export function artifactCacheEvictions(files: readonly CachedArtifactFile[], keep: ArtifactCacheEntry, inProgress: (name: string) => boolean, maxBytes = MAX_ARTIFACT_CACHE_BYTES): string[] {
	const busy = (file: CachedArtifactFile) => file.name === keep.name || inProgress(file.name.replace(/\.part$/, ""));
	const evicted = new Set(files.filter((file) => !busy(file) && file.name.startsWith(`${keep.prefix}-`)).map((file) => file.name));
	let total = files.reduce((sum, file) => evicted.has(file.name) ? sum : sum + file.size, 0);
	const others = files.filter((file) => !busy(file) && !evicted.has(file.name)).sort((a, b) => a.modified - b.modified);
	for (const file of others) {
		if (total <= maxBytes) break;
		evicted.add(file.name);
		total -= file.size;
	}
	return [...evicted];
}

function timeOf(value: string): number {
	const time = Date.parse(value);
	return Number.isNaN(time) ? 0 : time;
}

/** The page a horizontal pager of artifacts rests on at a scroll offset. */
export function artifactPageAt<T>(pages: readonly T[], offsetX: number, pageWidth: number): T | undefined {
	if (pageWidth <= 0 || pages.length === 0) return undefined;
	const index = Math.min(pages.length - 1, Math.max(0, Math.round(offsetX / pageWidth)));
	return pages[index];
}

/**
 * The viewer's page order. It is the list's order when the viewer opened, so a
 * file the agent rewrites (which moves to the front of the newest-first list)
 * keeps its page. Files that appear later go at the end, in list order, and
 * removed ones drop out.
 */
export function stableArtifactOrder<T extends { path: string }>(previous: readonly string[] | undefined, artifacts: readonly T[]): T[] {
	if (!previous) return [...artifacts];
	const byPath = new Map(artifacts.map((artifact) => [artifact.path, artifact]));
	const kept = previous.flatMap((path) => {
		const artifact = byPath.get(path);
		return artifact ? [artifact] : [];
	});
	const known = new Set(previous);
	return [...kept, ...artifacts.filter((artifact) => !known.has(artifact.path))];
}

/**
 * The viewer's pages: the artifacts the list shows, so swiping cannot reach a
 * file the list hides. A hidden file opened by a link is a page of its own.
 */
export function artifactViewerPages<T extends ArtifactFacts>(previous: readonly string[] | undefined, artifacts: readonly T[], openPath: string): T[] {
	const listed: readonly T[] = deliverableArtifacts(artifacts);
	if (!listed.some((artifact) => artifact.path === openPath)) {
		const opened = artifacts.find((artifact) => artifact.path === openPath);
		if (opened) return [opened];
	}
	return stableArtifactOrder(previous, listed);
}
