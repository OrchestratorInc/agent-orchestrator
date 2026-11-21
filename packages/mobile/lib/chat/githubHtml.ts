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
 * tags, so prose such as `Array<string>` is left as written, a backslash-escaped
 * tag (`\<details\>`) stays text, and code (fences, spans, `<pre>`, `<code>`) is
 * never read as markup. The Markdown this returns is not yet safe to draw:
 * callers still pass it through `safeMarkdown`.
 *
 * Every pass reads the body once: a body of unclosed tags, fences or comments
 * costs about what a short one does per character, so a hostile comment on a
 * watched PR cannot stall the chat (Hermes has no regex JIT).
 */

import { FENCE_GUARD } from "./markdownBlocks";

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
// A backslash before ASCII punctuation (CommonMark): `\<details\>` names the tag and opens nothing.
const ESCAPE = /\\[!-/:-@[-`{-~]/g;
// HTML code: an opener, then the first closing tag of its name, found by the reader below.
const HTML_CODE_OPEN = /<(pre|code)(?![\w-])(?:"[^"<]*"|'[^'<]*'|[^'"<>])*>/gi;
const HTML_CODE_CLOSE = { pre: /<\/pre\s*>/gi, code: /<\/code\s*>/gi };
// No part of a tag holds `<`, so a tag that never closes is given up at the next `<`, not the end of the body.
const DETAILS = /<details(?![\w-])[^<>]*>|<\/details\s*>/gi;
const SUMMARY = /^\s*<summary(?![\w-])[^<>]*>([\s\S]*?)<\/summary\s*>/i;

// GitHub's comment allowlist, plus the `<picture>`/`<source>` wrappers it unwraps.
const TAGS = "a|abbr|b|bdo|blockquote|br|caption|center|cite|code|dd|del|details|dfn|div|dl|dt|em|figcaption|figure|font|g-emoji|h[1-6]|hr|i|img|input|ins|kbd|li|mark|ol|p|picture|pre|q|rp|rt|ruby|s|samp|section|small|source|span|strike|strong|sub|summary|sup|table|tbody|td|tfoot|th|thead|time|tr|tt|u|ul|var|wbr";
// One allowlisted tag: closing slash, name, attributes. A quoted value may hold `>`.
const TAG = new RegExp(String.raw`<(\/?)(${TAGS})(?![\w-])((?:"[^"<]*"|'[^'<]*'|[^'"<>])*)>`, "gi");
const BLOCK_TAGS = new Set(["p", "div", "dl", "dd", "dt", "blockquote", "table", "thead", "tbody", "tfoot", "tr", "section", "figure", "figcaption", "center", "caption"]);
const PAIRED_TAGS = /^(?:a|b|strong|i|em|h[1-6])$/;
// Deeper nesting than this reads as text: it keeps each closing tag's work bounded.
const MAX_DEPTH = 64;

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
 * A code block the mobile parsers cannot leave early. ChatMarkdown opens a block
 * at a ``` line and closes it at the next line that starts with ```, whatever its
 * length, and knows no ~~~ fences; safeMarkdown closes at any line of three
 * backticks or tildes. So every block is drawn with ```, and a line inside that
 * would read as a fence starts with FENCE_GUARD, which neither reads as one; the
 * parser drops it again, so the block draws and copies as written.
 */
function codeBlock(code: string, language = ""): string {
	const lines = code === "" ? [] : code.split("\n").map((line) => /^ {0,3}(?:`{3}|~{3})/.test(line) ? FENCE_GUARD + line : line);
	return ["```" + language, ...lines, "```"].join("\n");
}

/** A Markdown fence as a `codeBlock`: its language kept, its closing line (when it has one) dropped. */
function fenceBlock(fence: string): string {
	const lines = fence.split("\n");
	const open = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(lines[0]!)!;
	const close = lines.length > 1 ? /^ {0,3}(`{3,}|~{3,})[ \t]*$/.exec(lines.at(-1)!) : null;
	const closed = !!close && close[1]![0] === open[1]![0] && close[1]!.length >= open[1]!.length;
	const language = open[2]!.trim().split(/\s/)[0]!.replace(/`/g, "");
	return codeBlock(lines.slice(1, closed ? -1 : undefined).join("\n"), language);
}

/** HTML code's text: its tags gone, its entities read. */
const htmlCodeText = (inner: string) => decodeEntities(inner.replace(TAG, ""));

function inlineCode(inner: string): string {
	const lines = htmlCodeText(inner).split("\n");
	// Line breaks in inline code read as spaces; the code's own outer spaces are kept.
	const code = lines.map((line, index) => index === 0 ? (lines.length > 1 ? line.trimEnd() : line) : index === lines.length - 1 ? line.trimStart() : line.trim()).join(" ");
	if (!code.trim()) return "";
	const ticks = "`".repeat(Math.max(0, ...[...code.matchAll(/`+/g)].map((run) => run[0].length)) + 1);
	return ticks.length > 1 || code.startsWith(" ") || code.endsWith(" ") ? `${ticks} ${code} ${ticks}` : `${ticks}${code}${ticks}`;
}

/** Something the scan below can read: its next match at or after `from`, and what it leaves in the text. */
type Found = { index: number; end: number; emit: () => string };
type Reader = (text: string, from: number) => Found | null;

function matching(pattern: RegExp, emit: (match: string) => string): Reader {
	return (text, from) => {
		pattern.lastIndex = from;
		const match = pattern.exec(text);
		return match ? { index: match.index, end: match.index + match[0].length, emit: () => emit(match[0]) } : null;
	};
}

/**
 * `<pre>` and `<code>`, each from its opener to the first closing tag of its name.
 * The next closing tag of each name is remembered, so an opener that has none
 * after it is passed over without scanning to the end of the body again.
 */
function htmlCode(emit: (name: "pre" | "code", inner: string) => string): Reader {
	const closes: { pre?: RegExpExecArray | null; code?: RegExpExecArray | null } = {};
	return (text, from) => {
		HTML_CODE_OPEN.lastIndex = from;
		for (let open = HTML_CODE_OPEN.exec(text); open; open = HTML_CODE_OPEN.exec(text)) {
			const name = open[1]!.toLowerCase() as "pre" | "code";
			const start = open.index + open[0].length;
			let close = closes[name];
			if (close === undefined || (close && close.index < start)) {
				HTML_CODE_CLOSE[name].lastIndex = start;
				close = closes[name] = HTML_CODE_CLOSE[name].exec(text);
			}
			if (close) {
				const inner = text.slice(start, close.index);
				return { index: open.index, end: close.index + close[0].length, emit: () => emit(name, inner) };
			}
		}
		return null;
	};
}

/**
 * Drops comments and sets code and escapes aside in one pass, left to right, with
 * one rule: whichever starts first wins. A backtick or a fence line inside a closed
 * comment is part of it and cannot reach past its `-->`; a fence, span or `<pre>`
 * that opens before `<!--` keeps it as code; an escaped `<` or backtick opens
 * nothing, and a backslash inside code is code. A fence wins a tie with a span.
 * `\<`, `\>` and `\&` draw as themselves, since nothing after this reads HTML and
 * an `&` set aside cannot start an entity; other escapes stay as written.
 */
function setAside(text: string, shelf: Shelf): string {
	const aside = (code: string) => shelve(shelf, code);
	const readers: Reader[] = [
		matching(FENCE, (fence) => aside(fenceBlock(fence))),
		htmlCode((name, inner) => name === "pre" ? `\n\n${aside(codeBlock(htmlCodeText(inner).replace(/^\n|\n$/g, "")))}\n\n` : aside(inlineCode(inner))),
		matching(COMMENT, () => ""),
		matching(CODE_SPAN, aside),
		matching(ESCAPE, (escape) => aside(/^\\[<>&]$/.test(escape) ? escape[1]! : escape)),
	];
	// Each reader's next match from where the scan stands; a reader is asked again only once the scan passes it.
	const next: Array<Found | null | undefined> = readers.map(() => undefined);
	let out = "";
	let at = 0;
	for (;;) {
		let first: Found | null = null;
		for (let index = 0; index < readers.length; index++) {
			const cached = next[index];
			const found = cached === undefined || (cached && cached.index < at) ? (next[index] = readers[index]!(text, at)) : cached;
			if (found && (!first || found.index < first.index)) first = found;
		}
		if (!first) return out + text.slice(at);
		out += text.slice(at, first.index) + first.emit();
		at = first.end;
	}
}

/** `mark` around each paragraph of `text`, so `<b>a\n\nb</b>` is bold in both and leaves no stray `**`. */
function eachParagraph(text: string, mark: string): string {
	return text.split(/(\n[ \t]*\n)/).map((part, index) => {
		const core = part.trim();
		if (index % 2 === 1 || !core) return part;
		const lead = part.length - part.trimStart().length;
		const trail = part.length - part.trimEnd().length;
		return part.slice(0, lead) + mark + core + mark + part.slice(part.length - trail);
	}).join("");
}

// Characters that would end a Markdown link target early, written as escapes the URL means the same with.
const encodeHref = (href: string) => href.replace(/[ \t\n\r\f\v()<>[\]]/g, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`);

/** A closed `<a>`, `<b>`, `<i>`, `<h1>`… pair around content already converted. */
function pair(name: string, attributes: string, content: string, summary: boolean): string {
	if (name === "a") {
		const label = content.replace(/\s+/g, " ").trim();
		const href = attribute(attributes, "href");
		// Neither renderer reads a `]` in a label, escaped or not, so such a link keeps its words.
		return !label ? "" : href && !/[[\]]/.test(label) ? `[${label}](${encodeHref(href)})` : label;
	}
	if (name[0] === "h") {
		const text = content.replace(/\s+/g, " ").trim();
		return !text || summary ? text : `\n\n${"#".repeat(Number(name[1]))} ${text}\n\n`;
	}
	if (name === "b" || name === "strong") return eachParagraph(content, "**");
	// A summary draws bold, code and links only; emphasis there is its plain words.
	return summary ? content : eachParagraph(content, "*");
}

/**
 * One stretch of comment HTML, without `<details>` or HTML code, as the Markdown
 * it reads as, in one pass over its tags. A pair is converted when its closing tag
 * arrives; an opener never closed drops its tag and keeps its words, and a stray
 * closing tag is dropped. `summary` reads it as one line, for a section's summary.
 */
function toMarkdown(html: string, summary = false): string {
	const out: string[] = [];
	const open: Array<{ name: string; attributes: string; start: number }> = [];
	const lists: Array<{ ordered: boolean; next: number }> = [];
	// A dropped image takes the space after it, so `**<img> Minor**` still reads as bold.
	let dropSpace = false;
	const text = (raw: string) => {
		if (!raw) return;
		const value = decodeEntities(raw);
		out.push(dropSpace ? value.replace(/^[ \t]+/, "") : value);
		dropSpace = false;
	};
	let at = 0;
	for (const tag of html.matchAll(TAG)) {
		text(html.slice(at, tag.index));
		at = tag.index! + tag[0].length;
		const closing = tag[1] === "/";
		const name = tag[2]!.toLowerCase();
		const attributes = tag[3]!;
		if (PAIRED_TAGS.test(name)) {
			if (!closing) {
				if (open.length < MAX_DEPTH) open.push({ name, attributes, start: out.length });
				continue;
			}
			let index = open.length - 1;
			while (index >= 0 && open[index]!.name !== name) index--;
			if (index < 0) continue;
			// Openers inside it that never closed keep their words.
			const frame = open[index]!;
			open.length = index;
			out.push(pair(frame.name, frame.attributes, out.splice(frame.start).join(""), summary));
			continue;
		}
		switch (name) {
			case "img": {
				const alt = closing ? undefined : attribute(attributes, "alt")?.trim();
				if (alt && !isDecorativeIcon(alt, attribute(attributes, "width"), attribute(attributes, "height"))) out.push(alt);
				else if (!closing) dropSpace = true;
				break;
			}
			case "br":
				out.push(summary ? " " : "\n");
				break;
			case "hr":
				out.push("\n\n---\n\n");
				break;
			case "ol":
			case "ul":
				if (closing) lists.pop();
				else lists.push({ ordered: name === "ol", next: Number.parseInt(attribute(attributes, "start") ?? "", 10) || 1 });
				out.push("\n");
				break;
			case "li": {
				const list = lists.at(-1);
				if (!closing) out.push(list?.ordered ? `\n${list.next++}. ` : "\n- ");
				break;
			}
			case "td":
			case "th":
				out.push(" ");
				break;
			default:
				if (BLOCK_TAGS.has(name)) out.push("\n");
		}
	}
	text(html.slice(at));
	return out.join("").split("\n").map((line) => line.trimEnd()).join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

type Frame = { parts: Array<string | GitHubSegment> };

function closeFrame(frame: Frame, shelf: Shelf): GitHubSegment {
	let summary = "Details";
	const first = frame.parts[0];
	if (typeof first === "string") {
		const match = SUMMARY.exec(first);
		if (match) {
			summary = unshelve(shelf, toMarkdown(match[1]!, true)).replace(/\s+/g, " ").trim() || summary;
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
		const text = unshelve(shelf, toMarkdown(part));
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
