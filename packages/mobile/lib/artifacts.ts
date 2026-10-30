/**
 * A session's artifacts: the files an agent leaves in the session's own
 * artifact directory, outside the git workspace. Mirrors the desktop's
 * Artifacts inspector (frontend SessionArtifactsView) for the phone.
 */

import type { FileAnnotationTarget } from "../../shared/files/file-annotations";

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
};

/** How the viewer draws a file. */
export type ArtifactViewKind = "html" | "markdown" | "image" | "web-media" | "text";

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

const RASTER_IMAGE = /\.(apng|avif|bmp|gif|heic|heif|jpe?g|png|webp)$/i;
// Formats React Native's Image cannot draw, or players it does not have.
const WEB_MEDIA = /\.(svg|m4v|mov|mp4|ogv|webm|aac|flac|m4a|mp3|oga|ogg|opus|wav|pdf)$/i;
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
		const { path, name, kind, size, updatedAt } = item as Record<string, unknown>;
		if (typeof path !== "string" || !isSafeArtifactPath(path)) continue;
		out.push({
			path,
			name: typeof name === "string" && name ? name : path.split("/").pop() || path,
			kind: kind === "html" || kind === "markdown" ? kind : "file",
			size: typeof size === "number" && size >= 0 ? size : 0,
			updatedAt: typeof updatedAt === "string" ? updatedAt : "",
		});
	}
	return out;
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
	if (artifact.kind === "html") return "html";
	if (artifact.kind === "markdown" || MARKDOWN.test(artifact.path)) return "markdown";
	if (RASTER_IMAGE.test(artifact.path)) return "image";
	if (WEB_MEDIA.test(artifact.path)) return "web-media";
	return "text";
}

/**
 * Whether the file is taken as text, for Copy, Version history and a card's
 * preview: a page, a document, or any file without a known binary extension
 * (an archive, an office document, a database). The viewer still reads an
 * unknown file and decides by its contents.
 */
export function isTextArtifact(artifact: Pick<SessionArtifact, "path" | "kind">): boolean {
	const kind = artifactViewKind(artifact);
	if (kind === "html" || kind === "markdown") return true;
	return kind === "text" && !BINARY_EXTENSION.test(artifact.path);
}

/** Whether the file's contents can be copied as text. */
export function canCopyArtifact(artifact: Pick<SessionArtifact, "path" | "kind">): boolean {
	return isTextArtifact(artifact);
}

/**
 * Whether the viewer can draw a web-media file in a WebView on `os`. Android's
 * WebView has no PDF renderer: it shows nothing, or starts a download.
 */
export function showsInWebView(path: string, os: string): boolean {
	return !(os === "android" && /\.pdf$/i.test(path));
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
export type ArtifactMenuAction = "open" | "ask" | "copy" | "history" | "delete";

/**
 * What an artifact's menu offers, in order. The viewer already shows the file,
 * so it leaves out Open.
 */
export function artifactMenuActions(artifact: Pick<SessionArtifact, "path" | "kind" | "size">, { inViewer = false }: { inViewer?: boolean } = {}): ArtifactMenuAction[] {
	return [
		...(inViewer ? [] : ["open" as const]),
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

function timeOf(value: string): number {
	const time = Date.parse(value);
	return Number.isNaN(time) ? 0 : time;
}
