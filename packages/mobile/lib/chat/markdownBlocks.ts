/**
 * A list item. `depth` is its nesting (0 for a top-level item); `number` is the
 * number it shows when it sits in an ordered list, counted the way CommonMark
 * counts: from the list's first number, so "1. 1. 1." reads 1, 2, 3 and a list
 * that starts at "6." stays one item numbered 6.
 */
export type MarkdownListItem = { text: string; checked?: boolean; depth: number; number?: number };

export type MarkdownBlock =
	| { kind: "paragraph"; text: string }
	| { kind: "heading"; text: string; level: number }
	| { kind: "quote"; text: string }
	| { kind: "list"; ordered: boolean; items: MarkdownListItem[] }
	| { kind: "code"; language?: string; text: string }
	| { kind: "table"; headers: string[]; align: TableAlign[]; rows: string[][] }
	| { kind: "image"; alt: string; url: string }
	| { kind: "rule" };

export type TableAlign = "left" | "center" | "right" | undefined;

/**
 * Put before a line inside a code block that would otherwise read as a fence, by
 * text built for this parser (GitHub comments, githubHtml.ts): it keeps the block
 * open here and in safeMarkdown, and the parser drops it from the block's text, so
 * what is drawn, selected and copied is the line as written.
 */
export const FENCE_GUARD = "\u200B";
const GUARDED_LINE = /^\u200B(?= {0,3}(?:`{3}|~{3}))/;

const FENCE = /^(\s*)(`{3,}|~{3,})(.*)$/;
const LIST_ITEM = /^(\s*)(?:(\d{1,9})[.)]|([-*+]))\s+(.*)$/;
const HEADING = /^\s{0,3}(#{1,6})\s+(.+?)(?:\s+#+)?\s*$/;
const RULE = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/;
const SETEXT = /^\s{0,3}(=+|-+)\s*$/;
const QUOTE = /^\s{0,3}>\s?/;

/**
 * Pure GFM-shaped block parser; unknown syntax remains readable paragraph text.
 *
 * `relativeImages` also reads `![alt](path/to.png)`, a link to a file beside a
 * document (an artifact), as an image; a chat reply keeps to web images.
 */
export function parseBlocks(input: string, { relativeImages = false }: { relativeImages?: boolean } = {}): MarkdownBlock[] {
	// Structure reads indentation with tabs as four spaces; code keeps its own tabs.
	const raw = input.replace(/\r/g, "").split("\n");
	const lines = raw.map((line) => line.replace(/^[ \t]+/, (indent) => indent.replace(/\t/g, "    ")));
	const blocks: MarkdownBlock[] = [];
	let paragraph: string[] = [];
	// The open list: its block, the indent of each open nesting level, and each
	// level's running number. It survives a fence indented under an item, so the
	// steps after the fence keep counting.
	let list: { block: Extract<MarkdownBlock, { kind: "list" }>; indents: number[]; numbers: Array<number | undefined>; blank: boolean } | null = null;
	let listResumes = false;
	// Prose under an item after its code block (`npm i` … "Then run it."): its own
	// paragraph, with the list held open so the next step keeps its number.
	let listProse: Extract<MarkdownBlock, { kind: "paragraph" }> | null = null;
	// The source lines of each list item and of each paragraph under a step, joined
	// once the text is parsed: joining as each line arrives would copy the text so
	// far on every line, quadratic in a long item.
	const runs = new Map<{ text: string }, Run>();
	const flushParagraph = () => {
		if (paragraph.length) blocks.push({ kind: "paragraph", text: joinLines(paragraph) });
		paragraph = [];
	};
	const closeList = () => {
		list = null;
		listResumes = false;
		listProse = null;
	};
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
		const indent = leadingSpaces(line);

		const fence = FENCE.exec(line);
		if (fence && (fence[1].length <= 3 || list)) {
			flushParagraph();
			const marker = fence[2];
			const language = fence[3].trim().split(/\s+/)[0] || undefined;
			// A fence opened inside a list item keeps the list's numbering going.
			const inList = Boolean(list) && (fence[1].length > 0 || !list!.blank);
			if (!inList) closeList();
			// Under a list item the closer's indent is the item's; at top level, at most three spaces.
			const closes = (candidate: string) => closesFence(candidate, marker, inList ? Infinity : 3);
			const code: string[] = [];
			for (i += 1; i < lines.length && !closes(lines[i]); i++) code.push(dedent(raw[i].replace(GUARDED_LINE, ""), fence[1].length));
			blocks.push({ kind: "code", language, text: code.join("\n") });
			if (inList) listResumes = true;
			listProse = null;
			continue;
		}

		if (!line.trim()) {
			flushParagraph();
			if (list) list.blank = true;
			continue;
		}

		// An indented code block: lines indented four or more spaces that start a
		// block (no paragraph open for them to continue, no list to belong to). Its
		// blank lines are kept; trailing ones are not.
		if (indent >= 4 && !list && !paragraph.length) {
			const code: string[] = [];
			for (; i < lines.length && (leadingSpaces(lines[i]) >= 4 || !lines[i].trim()); i++) code.push(dedent(raw[i], 4));
			i--;
			while (!code[code.length - 1].trim()) code.pop();
			blocks.push({ kind: "code", text: code.join("\n") });
			continue;
		}

		const item = LIST_ITEM.exec(line);
		if (item && item[4].trim() && !RULE.test(line)) {
			const ordered = item[2] !== undefined;
			// A top-level item of the other kind starts a list of its own.
			if (list && indent <= list.indents[0] + 1 && list.block.ordered !== ordered) closeList();
			// CommonMark: an ordered item interrupts a paragraph only when it starts at 1.
			if (!list && paragraph.length && ordered && Number(item[2]) !== 1) { paragraph.push(line); continue; }
			flushParagraph();
			if (!list) {
				list = { block: { kind: "list", ordered, items: [] }, indents: [indent], numbers: [undefined], blank: false };
				blocks.push(list.block);
			} else if (listResumes) {
				// The list continues after a fence indented under one of its items.
				list.block = { kind: "list", ordered: list.block.ordered, items: [] };
				blocks.push(list.block);
			}
			listResumes = false;
			listProse = null;
			const depth = nestingDepth(list, indent);
			let number: number | undefined;
			if (ordered) {
				const previous = list.numbers[depth];
				number = previous === undefined ? Number(item[2]) : previous + 1;
				list.numbers[depth] = number;
			}
			const added: MarkdownListItem = { ...taskItem(item[4]), depth, ...(number !== undefined ? { number } : {}) };
			list.block.items.push(added);
			runs.set(added, { lines: [added.text], gaps: [false] });
			list.blank = false;
			continue;
		}

		if (list) {
			const last = list.block.items[list.block.items.length - 1];
			// A line indented under an item belongs to it; so does an unindented
			// line straight after it (a lazy continuation), unless it opens a block.
			const opensBlock = HEADING.test(line) || QUOTE.test(line) || RULE.test(line) || /^\s*!\[/.test(line)
				|| (line.includes("|") && i + 1 < lines.length && tableDivider(lines[i + 1], tableCells(line).length) !== null);
			const belongs = indent >= 2 || (!list.blank && !opensBlock);
			if (last && listResumes && belongs) {
				if (listProse && !list.blank) runs.get(listProse)!.lines.push(line);
				else {
					listProse = { kind: "paragraph", text: "" };
					blocks.push(listProse);
					runs.set(listProse, { lines: [line], gaps: [false] });
				}
				list.blank = false;
				continue;
			}
			if (last && !listResumes && belongs) {
				const run = runs.get(last)!;
				run.lines.push(line);
				run.gaps.push(list.blank);
				list.blank = false;
				continue;
			}
			closeList();
		}

		const heading = HEADING.exec(line);
		if (heading) {
			flushParagraph();
			blocks.push({ kind: "heading", level: heading[1].length, text: heading[2].trim() });
			continue;
		}
		const setext = paragraph.length ? SETEXT.exec(line) : null;
		if (setext) {
			blocks.push({ kind: "heading", level: setext[1][0] === "=" ? 1 : 2, text: joinLines(paragraph) });
			paragraph = [];
			continue;
		}
		const image = (relativeImages ? /^!\[([^\]]*)\]\(([^\s)]+)\)\s*$/ : /^!\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)\s*$/).exec(line.trim());
		if (image) {
			flushParagraph();
			blocks.push({ kind: "image", alt: image[1], url: image[2] });
			continue;
		}
		const align = line.includes("|") && i + 1 < lines.length ? tableDivider(lines[i + 1], tableCells(line).length) : null;
		if (align) {
			flushParagraph();
			const headers = tableCells(line);
			const rows: string[][] = [];
			i += 2;
			while (i < lines.length && lines[i].includes("|") && lines[i].trim()) { rows.push(tableCells(lines[i])); i++; }
			i--;
			blocks.push({ kind: "table", headers, align, rows });
			continue;
		}
		if (RULE.test(line)) {
			flushParagraph();
			blocks.push({ kind: "rule" });
			continue;
		}
		if (QUOTE.test(line)) {
			flushParagraph();
			const quote = [line.replace(QUOTE, "")];
			while (i + 1 < lines.length && QUOTE.test(lines[i + 1])) quote.push(lines[++i].replace(QUOTE, ""));
			blocks.push({ kind: "quote", text: quoteText(quote.join("\n"), relativeImages) });
			continue;
		}
		paragraph.push(line);
	}
	flushParagraph();
	for (const [owner, run] of runs) owner.text = joinRun(run.lines, run.gaps);
	return blocks;
}

/**
 * Whether `line` closes a fence opened with `opener`: a bare run of the same
 * character, at least as long, indented no more than `maxIndent` spaces.
 */
function closesFence(line: string, opener: string, maxIndent: number): boolean {
	const fence = FENCE.exec(line);
	return Boolean(fence) && fence![1].length <= maxIndent && fence![2][0] === opener[0] && fence![2].length >= opener.length && !fence![3].trim();
}

const INLINE_MARKERS = ["`", "**", "~~"] as const;

/**
 * Ends an unfinished inline span in a reply that is still streaming, so the
 * reader sees **bold** arriving rather than the asterisks that open it. Display
 * only: copying keeps the stored text. Text inside an open fence is code, and is
 * left as it is.
 *
 * Markers are counted only in the inline text the closer lands in: the reply's
 * last list item or heading, or else its last paragraph. An opener with nothing
 * after it yet (`Here is **`) is held back rather than closed on itself.
 */
export function repairStreamingInline(text: string): string {
	let open: string | null = null;
	// Where the prose after the last closed fence starts: its markers are not the reply's.
	let afterFence = 0;
	// Where the last list item or heading starts: its text is one inline run of its own.
	// A heading is one line, so text after it starts a run of its own.
	let lastItem = 0;
	let afterHeading: number | null = null;
	let at = 0;
	for (const line of text.split("\n")) {
		const lineStart = at;
		at += line.length + 1;
		if (open !== null) {
			if (closesFence(line, open, Infinity)) { open = null; afterFence = at; }
			continue;
		}
		const fence = FENCE.exec(line);
		if (fence) open = fence[2];
		else if (HEADING.test(line)) { lastItem = lineStart; afterHeading = at; }
		else if (LIST_ITEM.test(line) && !RULE.test(line)) { lastItem = lineStart; afterHeading = null; }
		else if (afterHeading !== null && line.trim()) { lastItem = afterHeading; afterHeading = null; }
	}
	if (open !== null) return text;
	const start = Math.max(afterFence, text.lastIndexOf("\n\n") + 1, lastItem);
	const scope = text.slice(start);
	let end = scope.length;
	while (end > 0 && /\s/.test(scope[end - 1])) end--;
	const trailing = scope.slice(end);
	let body = scope.slice(0, end);
	// Drop an opener that has just arrived, with no text after it to format.
	for (let dropped = true; dropped;) {
		dropped = false;
		for (const marker of INLINE_MARKERS) {
			if (body.endsWith(marker) && body[body.length - marker.length - 1] !== "\\" && unmatched(body, marker)) {
				body = body.slice(0, -marker.length);
				dropped = true;
			}
		}
	}
	// Close the code span first, then the spans around it, the latest opened first.
	const closeCode = unmatched(body, "`") ? "`" : "";
	const prose = outsideCode(body + closeCode);
	const closers = (["**", "~~"] as const)
		.filter((marker) => countUnescaped(prose, marker) % 2 === 1)
		.sort((a, b) => prose.lastIndexOf(b) - prose.lastIndexOf(a));
	return text.slice(0, start) + body + closeCode + closers.join("") + trailing;
}

/** Whether `marker` is left open in `text`; markers inside code, closed or still open, do not count. */
function unmatched(text: string, marker: string): boolean {
	return countUnescaped(marker === "`" ? text : outsideCode(text), marker) % 2 === 1;
}

/** `text` without its code spans, including one still open at the end; `\`` is a literal backtick, not a span. */
function outsideCode(text: string): string {
	// A placeholder, not nothing: deleting `\`` would join `*\`*` into a `**` that is not there.
	const closed = text.replace(/\\`/g, "\u0000").replace(/`[^`]*`/g, "");
	const open = closed.indexOf("`");
	return open === -1 ? closed : closed.slice(0, open);
}

function countUnescaped(text: string, marker: string): number {
	let count = 0;
	for (let at = text.indexOf(marker); at !== -1; at = text.indexOf(marker, at + marker.length)) {
		if (text[at - 1] !== "\\") count++;
	}
	return count;
}

/**
 * The nesting level an item at `indent` sits at: deeper than the item above it
 * when indented past that item, otherwise the level whose indent it reaches.
 */
function nestingDepth(list: { indents: number[]; numbers: Array<number | undefined> }, indent: number): number {
	const { indents, numbers } = list;
	if (indent > indents[indents.length - 1] + 1) {
		indents.push(indent);
		numbers.push(undefined);
	} else {
		while (indents.length > 1 && indent < indents[indents.length - 1] - 1) {
			indents.pop();
			numbers.pop();
		}
	}
	return indents.length - 1;
}

/**
 * A blockquote's own Markdown as quoted prose: its blocks sit a blank line apart,
 * as paragraphs do, and its lists keep one line per item.
 */
function quoteText(value: string, relativeImages: boolean): string {
	return parseBlocks(value, { relativeImages }).map((block) => {
		switch (block.kind) {
			case "list": return block.items.map((item) => `${"  ".repeat(item.depth)}${item.checked !== undefined ? (item.checked ? "☑ " : "☐ ") : item.number !== undefined ? `${item.number}. ` : "• "}${item.text}`).join("\n");
			case "table": return [block.headers, ...block.rows].map((row) => row.join(" · ")).join("\n");
			case "image": return block.alt || block.url;
			case "rule": return "";
			default: return block.text;
		}
	}).filter(Boolean).join("\n\n");
}

/** A block's source lines, and whether a blank line came before each. */
type Run = { lines: string[]; gaps: boolean[] };

/**
 * A paragraph's source lines as the text it reads as. A plain line break is a
 * soft break, a space, as on the desktop and GitHub; a line ending in two spaces
 * or a backslash is a hard break.
 */
function joinLines(lines: string[]): string {
	return joinRun(lines, []);
}

/**
 * Source lines joined in one pass: after a blank line (`gaps`) a line starts
 * on a line of its own; otherwise each break is decided by the line before it,
 * hard or soft. A backslash with no line after it is not a break: `C:\` keeps it.
 */
function joinRun(lines: string[], gaps: boolean[]): string {
	const parts: string[] = [];
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i].trimStart();
		if (i === lines.length - 1) parts.push(line.trimEnd());
		else if (gaps[i + 1]) parts.push(line.trimEnd(), "\n");
		else if (line.endsWith("\\")) parts.push(line.slice(0, -1).trimEnd(), "\n");
		else if (line.endsWith("  ")) parts.push(line.trimEnd(), "\n");
		else parts.push(line.trimEnd(), " ");
	}
	return parts.join("");
}

function leadingSpaces(line: string): number {
	return /^ */.exec(line)![0].length;
}

/**
 * Removes a fence's own indent (`count` columns) from a line of its code, a tab
 * counting four; a tab wider than what is left to remove stays, as code.
 */
function dedent(line: string, count: number): string {
	let at = 0;
	for (let left = count; at < line.length && left > 0; at++) {
		const width = line[at] === " " ? 1 : line[at] === "\t" ? 4 : 0;
		if (!width || width > left) break;
		left -= width;
	}
	return line.slice(at);
}

function taskItem(value: string): { text: string; checked?: boolean } {
	const task = /^\[([ xX])\]\s+(.+)$/.exec(value);
	return task ? { text: task[2], checked: task[1].toLowerCase() === "x" } : { text: value };
}

/** GFM delimiter row: one `-+` cell per header column, colons set alignment. */
function tableDivider(value: string, columns: number): TableAlign[] | null {
	const cells = tableCells(value);
	if (cells.length !== columns || !cells.every((cell) => /^:?-+:?$/.test(cell))) return null;
	return cells.map((cell) => cell.endsWith(":") ? (cell.startsWith(":") ? "center" : "right") : cell.startsWith(":") ? "left" : undefined);
}

function tableCells(value: string): string[] {
	// `\|` is a literal pipe inside a cell, including inside inline code.
	return value.trim().replace(/\\\|/g, "\u0000").replace(/^\|/, "").replace(/\|$/, "").split("|").map((cell) => cell.trim().replace(/\u0000/g, "|"));
}
