import { fileMediaKind } from "../../lib/file-media";

// These are AO-owned prompt suffixes, not general markdown. Chat and spawn used
// slightly different wording, and older conversations used "Attached images";
// accepting every shipped form lets the transcript improve without rewriting
// its durable history.
const ATTACHMENT_REFERENCE_BLOCK =
	/(?:^|\n\n)(?:Attached files \(read these files in the workspace(?: for context)?\)|Attached images \(read these files in the workspace for visual context\)):\n((?:- [^\n]+(?:\n|$))+)$/;
const STAGED_ATTACHMENT_PATH = /^\.ao\/attachments\/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*$/;
export const IMAGE_ATTACHMENT_PATH = /\.(?:png|jpe?g|gif|webp|bmp)$/i;

export function stagedAttachmentParts(text: string): { body: string; attachments: string[] } {
	const match = ATTACHMENT_REFERENCE_BLOCK.exec(text);
	if (!match?.[1]) return { body: text, attachments: [] };

	const attachments = match[1]
		.trimEnd()
		.split("\n")
		.map((line) => line.slice(2));
	// Only reinterpret paths AO itself stages. A user can write an identically
	// worded example about docs/screenshot.png; that prose must remain untouched.
	if (attachments.length === 0 || attachments.some((path) => !STAGED_ATTACHMENT_PATH.test(path))) {
		return { body: text, attachments: [] };
	}
	// The match begins at the generated separator, so slicing at its index
	// removes only AO-owned text and preserves the authored body byte-for-byte.
	return { body: text.slice(0, match.index), attachments };
}

/**
 * Tell the agent to open the attached files. Mirrors the wording spawn uses for a task
 * brief, so the same instruction reaches the agent whether a file was attached at
 * spawn or mid-conversation.
 */
export function withAttachmentReferences(text: string, paths: string[]): string {
	if (paths.length === 0) return text;
	const lead = text.trim() === "" ? "" : `${text}\n\n`;
	return `${lead}Attached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`;
}

export function attachmentName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
}

/** A file's type as its name gives it, for a chip whose name may truncate. */
export function attachmentTypeLabel(name: string): string | undefined {
	return /\.([A-Za-z0-9]{1,8})$/.exec(name)?.[1]?.toUpperCase();
}

export function formatAttachmentSize(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * One size for anything attached to a sent message, so a mixed row lines up: the
 * height of an image tile and the width of the widest one. Text on top, a pill below.
 */
export const ATTACHMENT_TILE_CLASS = "relative flex h-16 w-32 min-w-0 select-none flex-col justify-between overflow-hidden break-all rounded-md border border-border bg-interactive-hover p-2 text-left text-caption leading-tight text-muted-foreground";

/** A tile's type tag: a tiny control, so slightly rounded rather than a pill (DESIGN.md radius). */
export const ATTACHMENT_TILE_PILL_CLASS = "inline-flex h-4 shrink-0 items-center rounded-xs border border-border px-1.5 text-micro whitespace-nowrap";

/** The type pill laid over a preview: solid, so it reads on any frame. */
export const ATTACHMENT_TILE_OVERLAY_PILL_CLASS = "pointer-events-none absolute bottom-1.5 left-1.5 bg-background text-foreground";

export type AttachmentPreviewKind = "video" | "pdf" | "text";

const TEXT_PREVIEW_PATH = /\.(?:txt|md|mdx|json|jsonl|yaml|yml|toml|ini|conf|log|xml|css|js|jsx|ts|tsx|sql|sh|py|go|rs|diff|patch|csv|tsv)$/i;

/** How a file tile can preview this path, if it can. HTML keeps its name card: rendering it needs an isolated origin. */
export function attachmentPreviewKind(path: string): AttachmentPreviewKind | undefined {
	const media = fileMediaKind(path);
	if (media === "video" || media === "pdf") return media;
	return TEXT_PREVIEW_PATH.test(path) ? "text" : undefined;
}

export function attachmentURL(apiBaseUrl: string, sessionId: string, path: string): string {
	const route = `/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${path
		.split("/")
		.map(encodeURIComponent)
		.join("/")}`;
	return apiBaseUrl ? `${apiBaseUrl.replace(/\/+$/, "")}${route}` : route;
}

// A staged image path written into the prose itself: the composer's inline image
// chip serializes to exactly this, so the agent can tell which image a sentence
// is about.
// The lookbehind keeps a longer path such as `/wt/.ao/attachments/...` whole.
const INLINE_IMAGE_PATH = /(?<!\/)\.ao\/attachments\/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*\.(?:png|jpe?g|gif|webp|bmp)(?![A-Za-z0-9_-])/gi;

/** Whether a staged path is one the inline-image renderers can read back out of prose. */
export function isInlineImagePath(path: string): boolean {
	const segments = splitInlineImagePaths(path);
	return segments.length === 1 && segments[0]?.path === path;
}

export type InlineImageSegment = { text: string; path?: undefined } | { path: string; text?: undefined };

/** Split prose around inline staged image paths, optionally only those `keep` accepts. */
export function splitInlineImagePaths(text: string, keep: (path: string) => boolean = () => true): InlineImageSegment[] {
	const segments: InlineImageSegment[] = [];
	let last = 0;
	for (const match of text.matchAll(INLINE_IMAGE_PATH)) {
		if (!keep(match[0])) continue;
		if (match.index > last) segments.push({ text: text.slice(last, match.index) });
		segments.push({ path: match[0] });
		last = match.index + match[0].length;
	}
	if (last < text.length) segments.push({ text: text.slice(last) });
	return segments;
}

/**
 * Text with one inline image taken out, as removing its chip would: the path goes
 * with one following space, so the words around it don't double-space.
 */
export function removeInlineImagePath(text: string, path: string): string {
	return text
		.split("\n")
		.map((line) => {
			let kept = "";
			let afterImage = false;
			for (const segment of splitInlineImagePaths(line, (candidate) => candidate === path)) {
				if (segment.path !== undefined) afterImage = true;
				else {
					kept += afterImage && segment.text.startsWith(" ") ? segment.text.slice(1) : segment.text;
					afterImage = false;
				}
			}
			return kept;
		})
		.join("\n");
}

/**
 * A message that is nothing but its attached images keeps no prose, since the
 * reference block already names them. Anything else is sent exactly as written:
 * a path the user typed is their text, and only the composer can tell a stale
 * chip from it (see the editor's pruneImages).
 */
export function proseBesideImages(text: string, attached: string[]): string {
	const onlyImages = splitInlineImagePaths(text).every((segment) =>
		segment.path === undefined ? segment.text.trim() === "" : attached.includes(segment.path));
	return onlyImages ? "" : text;
}

/** Plain-text form for one-line surfaces: attached inline images read as `[Image N]`. */
export function labelInlineImages(text: string, label: (index: number) => string = (index) => `Image ${index}`): string {
	const { body, attachments } = stagedAttachmentParts(text);
	const images = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	if (images.length === 0) return text;
	const labelled = splitInlineImagePaths(body, (path) => images.includes(path))
		.map((segment) => segment.path === undefined ? segment.text : `[${label(images.indexOf(segment.path) + 1)}]`)
		.join("");
	return labelled + text.slice(body.length);
}
