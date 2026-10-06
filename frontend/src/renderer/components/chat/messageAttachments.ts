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

export function attachmentName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
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
 * The prose a composer sends: inline image paths survive only for images this
 * message attaches (an undo can restore a chip whose image was removed), and a
 * message that is nothing but its images keeps no prose at all, since the
 * reference block already names them.
 */
export function attachedInlineImages(text: string, attached: string[]): string {
	let result = "";
	let words = false;
	for (const segment of splitInlineImagePaths(text)) {
		if (segment.path === undefined) {
			// A dropped chip takes its trailing space with it.
			result += result.endsWith(" ") && segment.text.startsWith(" ") ? segment.text.slice(1) : segment.text;
			words ||= segment.text.trim() !== "";
		} else if (attached.includes(segment.path)) {
			result += segment.path;
		}
	}
	return words ? result.trim() : "";
}

/** Plain-text form for one-line surfaces: attached inline images read as `[Image N]`. */
export function labelInlineImages(text: string): string {
	const { body, attachments } = stagedAttachmentParts(text);
	const images = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	if (images.length === 0) return text;
	const labelled = splitInlineImagePaths(body, (path) => images.includes(path))
		.map((segment) => segment.path === undefined ? segment.text : `[Image ${images.indexOf(segment.path) + 1}]`)
		.join("");
	return labelled + text.slice(body.length);
}
