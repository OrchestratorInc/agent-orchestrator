/**
 * The HTML GitHub allows in a comment, read the way GitHub draws it, for the
 * mobile Markdown renderer, which draws no HTML. Bots such as CodeRabbit write
 * most of a review comment in it: hidden `<!-- … -->` state, `<picture>` icons,
 * and `<details>` sections holding their tool output and agent prompts. Left
 * as text, every comment reads as tags.
 *
 * This is the desktop's GITHUB_HTML_REHYPE (rehype-sanitize with GitHub's own
 * allowlist) in the shape mobile can draw: comments are dropped, an icon-sized
 * image without alt text is dropped and any other image becomes its alt text,
 * inline and block tags become their Markdown, and each `<details>` becomes a
 * section folded under its summary. Only tags on GitHub's allowlist are read as
 * tags, so prose such as `Array<string>` is left as written, and code (fences,
 * spans) is never touched. The Markdown this returns is not yet safe to draw:
 * callers still pass it through `safeMarkdown`.
 */

export type GitHubSegment =
	| { kind: "markdown"; text: string }
	| { kind: "details"; summary: string; body: GitHubSegment[] };

// A fence runs to a closing fence of the same character at least as long as its
// opening (CommonMark), or to the end of the text when it has none: a ``` line
// inside a ```` example is code, so a `<details>` after it stays code too. The
// body is optional, tried empty first, so an empty block closes at the next line.
// An opening line ends in a newline and, for backticks, holds no other backtick:
// ```code``` is a code span.
const FENCE = /^ {0,3}(?:(`{3,})[^`\n]*(?=\n)(?:(?:\n[\s\S]*?)??\n {0,3}\1`*[ \t]*(?=\n|(?![\s\S]))|[\s\S]*(?![\s\S]))|(~{3,})[^\n]*(?=\n)(?:(?:\n[\s\S]*?)??\n {0,3}\2~*[ \t]*(?=\n|(?![\s\S]))|[\s\S]*(?![\s\S])))/gm;
// A whole backtick run, then the first run of exactly that length (CommonMark): `` `<details>` `` is one span.
// A span may wrap a line but not past a blank line or a line that opens a fence (the
// same opener FENCE takes), which end the paragraph; ```example` opens none.
const CODE_SPAN = /(?<!`)(`+)(?!`)((?:(?!\n[ \t]*\n|\n {0,3}(?:`{3,}[^`\n]*|~{3,}[^\n]*)\n)[\s\S])*?[^`])\1(?!`)/g;
// An unterminated comment hides the rest of the text, as it does on GitHub.
const COMMENT = /<!--[\s\S]*?(?:-->|(?![\s\S]))/g;
const DETAILS = /<details\b[^>]*>|<\/details\s*>/gi;
const SUMMARY = /^\s*<summary\b[^>]*>([\s\S]*?)<\/summary\s*>/i;

// GitHub's comment allowlist, plus the `<picture>`/`<source>` wrappers it unwraps.
const TAGS = "a|abbr|b|bdo|blockquote|br|caption|center|cite|code|dd|del|details|dfn|div|dl|dt|em|figcaption|figure|font|g-emoji|h[1-6]|hr|i|img|input|ins|kbd|li|mark|ol|p|picture|pre|q|rp|rt|ruby|s|samp|section|small|source|span|strike|strong|sub|summary|sup|table|tbody|td|tfoot|th|thead|time|tr|tt|u|ul|var|wbr";
const ANY_TAG = new RegExp(String.raw`<\/?(?:${TAGS})\b[^<>]*\/?>`, "gi");
const BLOCK_TAG = /<\/?(?:p|div|dl|dd|dt|ul|ol|blockquote|table|thead|tbody|tfoot|tr|section|figure|figcaption|center|caption)\b[^<>]*>/gi;

const ENTITIES: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: "\"", apos: "'", nbsp: " " };

function decodeEntities(text: string): string {
	return text.replace(/&(?:#(\d{1,7})|#x([0-9a-f]{1,6})|([a-z]+));/gi, (entity, decimal: string | undefined, hex: string | undefined, name: string | undefined) => {
		if (name) return ENTITIES[name.toLowerCase()] ?? entity;
		const code = decimal ? Number(decimal) : Number.parseInt(hex!, 16);
		return code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : entity;
	});
}

function attribute(attributes: string, name: string): string | undefined {
	const match = new RegExp(String.raw`(?:^|\s)${name}\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`, "i").exec(attributes);
	return match ? decodeEntities(match[1] ?? match[2] ?? match[3] ?? "") : undefined;
}

/**
 * Whether an image is an inline icon (CodeRabbit's 12–16px glyphs beside a
 * heading) rather than content: no alt text, and every size it gives is
 * icon-sized. The desktop's `isDecorativeIcon`.
 */
export function isDecorativeIcon(alt: string | undefined, width: string | undefined, height: string | undefined): boolean {
	if (alt?.trim()) return false;
	const sizes = [width, height].map((value) => Number.parseInt(value ?? "", 10)).filter(Number.isFinite);
	return sizes.length > 0 && sizes.every((value) => value <= 32);
}

/** Code set aside behind a placeholder, so nothing below reads into it. */
type Shelf = string[];
const shelve = (shelf: Shelf, code: string) => `\u0000${shelf.push(code) - 1}\u0000`;
const unshelve = (shelf: Shelf, text: string) => text.replace(/\u0000(\d+)\u0000/g, (_token, index: string) => shelf[Number(index)] ?? "");

/**
 * Drops comments and sets code aside in one pass, left to right, with one rule:
 * whichever starts first wins. A backtick or a fence line inside a closed comment
 * is part of it and cannot reach past its `-->`; a fence or span that opens before
 * `<!--` keeps it as code. A fence wins a tie with a span at the same spot.
 */
function setAside(text: string, shelf: Shelf): string {
	const readers = [FENCE, COMMENT, CODE_SPAN];
	// Each reader's next match from where the scan stands; a reader is searched again only once the scan passes it.
	const next: Array<RegExpExecArray | null | undefined> = readers.map(() => undefined);
	let out = "";
	let at = 0;
	for (;;) {
		let first: RegExpExecArray | null = null;
		readers.forEach((reader, index) => {
			if (next[index] === undefined || (next[index] && next[index]!.index < at)) {
				reader.lastIndex = at;
				next[index] = reader.exec(text);
			}
			const match = next[index];
			if (match && (!first || match.index < first.index)) first = match;
		});
		if (!first) return out + text.slice(at);
		const match: RegExpExecArray = first;
		out += text.slice(at, match.index) + (match[0].startsWith("<!--") ? "" : shelve(shelf, match[0]));
		at = match.index + match[0].length;
	}
}

function inlineCode(text: string): string {
	const code = decodeEntities(text.replace(ANY_TAG, "")).replace(/\s*\n\s*/g, " ");
	if (!code.trim()) return "";
	const ticks = "`".repeat(Math.max(0, ...[...code.matchAll(/`+/g)].map((run) => run[0].length)) + 1);
	return ticks.length > 1 || code.startsWith(" ") || code.endsWith(" ") ? `${ticks} ${code} ${ticks}` : `${ticks}${code}${ticks}`;
}

/** One stretch of comment HTML, without `<details>`, as the Markdown it reads as. */
function toMarkdown(html: string, shelf: Shelf): string {
	let out = html
		// HTML code holds Markdown code spans already set aside; restore them first, so
		// they stay as written and the delimiter chosen below sees their backticks.
		.replace(/<pre\b[^>]*>([\s\S]*?)<\/pre\s*>/gi, (_pre, inner: string) => {
			const code = decodeEntities(unshelve(shelf, inner).replace(ANY_TAG, "")).replace(/^\n|\n$/g, "");
			const fence = "`".repeat(Math.max(2, ...[...code.matchAll(/`+/g)].map((run) => run[0].length)) + 1);
			return `\n\n${shelve(shelf, `${fence}\n${code}\n${fence}`)}\n\n`;
		})
		.replace(/<code\b[^>]*>([\s\S]*?)<\/code\s*>/gi, (_code, inner: string) => shelve(shelf, inlineCode(unshelve(shelf, inner))))
		// `<picture>` unwraps to the `<img>` inside it, the light-mode image.
		.replace(/<\/?(?:picture|source)\b[^<>]*>/gi, "")
		// A dropped image takes the space after it, so `**<img> Minor**` still reads as bold.
		.replace(/<img\b([^<>]*)>([ \t]*)/gi, (_img, attributes: string, space: string) => {
			const alt = attribute(attributes, "alt")?.trim();
			if (!alt || isDecorativeIcon(alt, attribute(attributes, "width"), attribute(attributes, "height"))) return "";
			return alt + space;
		})
		.replace(/<br\b[^<>]*>/gi, "\n")
		.replace(/<hr\b[^<>]*>/gi, "\n\n---\n\n")
		.replace(/<h([1-6])\b[^>]*>([\s\S]*?)<\/h\1\s*>/gi, (_heading, level: string, inner: string) => {
			const text = inner.replace(/\s+/g, " ").trim();
			return text ? `\n\n${"#".repeat(Number(level))} ${text}\n\n` : "";
		})
		.replace(/<a\b([^<>]*)>([\s\S]*?)<\/a\s*>/gi, (_link, attributes: string, inner: string) => {
			const label = inner.replace(/\s+/g, " ").trim();
			const href = attribute(attributes, "href");
			return !label ? "" : href ? `[${label}](${href.replace(/[\s()]/g, encodeURIComponent)})` : label;
		})
		.replace(/<(b|strong)\b[^>]*>([\s\S]*?)<\/\1\s*>/gi, (_bold, _tag, inner: string) => inner.trim() ? `**${inner.trim()}**` : "")
		.replace(/<(i|em)\b[^>]*>([\s\S]*?)<\/\1\s*>/gi, (_italic, _tag, inner: string) => inner.trim() ? `*${inner.trim()}*` : "")
		.replace(/<li\b[^>]*>/gi, "\n- ")
		.replace(/<\/?(?:td|th)\b[^<>]*>/gi, " ")
		.replace(BLOCK_TAG, "\n")
		.replace(ANY_TAG, "");
	out = decodeEntities(out);
	return out.split("\n").map((line) => line.replace(/[ \t]+$/, "")).join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

type Frame = { parts: Array<string | GitHubSegment> };

function closeFrame(frame: Frame, shelf: Shelf): GitHubSegment {
	let summary = "Details";
	const first = frame.parts[0];
	if (typeof first === "string") {
		const match = SUMMARY.exec(first);
		if (match) {
			// A summary is one line; whatever it held becomes inline Markdown.
			summary = unshelve(shelf, toMarkdown(match[1]!, shelf)).replace(/\s+/g, " ").trim() || summary;
			frame.parts[0] = first.slice(match[0].length);
		}
	}
	return { kind: "details", summary, body: segmentsOf(frame.parts, shelf) };
}

function segmentsOf(parts: ReadonlyArray<string | GitHubSegment>, shelf: Shelf): GitHubSegment[] {
	const segments: GitHubSegment[] = [];
	for (const part of parts) {
		if (typeof part !== "string") {
			segments.push(part);
			continue;
		}
		const text = unshelve(shelf, toMarkdown(part, shelf));
		if (!text) continue;
		const last = segments.at(-1);
		if (last?.kind === "markdown") last.text = `${last.text}\n\n${text}`;
		else segments.push({ kind: "markdown", text });
	}
	return segments;
}

/** A GitHub comment's body as Markdown runs and the `<details>` sections between them. */
export function githubSegments(body: string): GitHubSegment[] {
	const shelf: Shelf = [];
	const text = setAside(body.replace(/\r\n?/g, "\n").replace(/\u0000/g, ""), shelf);
	const stack: Frame[] = [{ parts: [] }];
	let at = 0;
	for (const match of text.matchAll(DETAILS)) {
		stack.at(-1)!.parts.push(text.slice(at, match.index));
		at = match.index! + match[0].length;
		if (match[0][1] !== "/") stack.push({ parts: [] });
		// A stray closing tag outside any section is dropped, as a browser drops it.
		else if (stack.length > 1) {
			const frame = stack.pop()!;
			stack.at(-1)!.parts.push(closeFrame(frame, shelf));
		}
	}
	stack.at(-1)!.parts.push(text.slice(at));
	// Sections left open close at the end of the body.
	while (stack.length > 1) {
		const frame = stack.pop()!;
		stack.at(-1)!.parts.push(closeFrame(frame, shelf));
	}
	return segmentsOf(stack[0]!.parts, shelf);
}

