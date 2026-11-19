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
	const lines = input.replace(/\r/g, "").replace(/\t/g, "    ").split("\n");
	const blocks: MarkdownBlock[] = [];
	let paragraph: string[] = [];
	// The open list: its block, the indent of each open nesting level, and each
	// level's running number. It survives a fence indented under an item, so the
	// steps after the fence keep counting.
	let list: { block: Extract<MarkdownBlock, { kind: "list" }>; indents: number[]; numbers: Array<number | undefined>; blank: boolean } | null = null;
	let listResumes = false;
	const flushParagraph = () => {
		if (paragraph.length) blocks.push({ kind: "paragraph", text: joinLines(paragraph) });
		paragraph = [];
	};
	const closeList = () => {
		list = null;
		listResumes = false;
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
			const close = new RegExp(`^\\s*${marker[0] === "`" ? "`" : "~"}{${marker.length},}\\s*$`);
			const code: string[] = [];
			for (i += 1; i < lines.length && !close.test(lines[i]); i++) code.push(dedent(lines[i], fence[1].length));
			blocks.push({ kind: "code", language, text: code.join("\n") });
			if (inList) listResumes = true;
			continue;
		}

		if (!line.trim()) {
			flushParagraph();
			if (list) list.blank = true;
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
			const depth = nestingDepth(list, indent);
			let number: number | undefined;
			if (ordered) {
				const previous = list.numbers[depth];
				number = previous === undefined ? Number(item[2]) : previous + 1;
				list.numbers[depth] = number;
			}
			list.block.items.push({ ...taskItem(item[4]), depth, ...(number !== undefined ? { number } : {}) });
			list.blank = false;
			continue;
		}

		if (list) {
			const last = list.block.items[list.block.items.length - 1];
			// A line indented under an item belongs to it; so does an unindented
			// line straight after it (a lazy continuation), unless it opens a block.
			const opensBlock = HEADING.test(line) || QUOTE.test(line) || RULE.test(line) || /^\s*!\[/.test(line)
				|| (line.includes("|") && i + 1 < lines.length && tableDivider(lines[i + 1], tableCells(line).length) !== null);
			if (last && !listResumes && (indent >= 2 || (!list.blank && !opensBlock))) {
				last.text = list.blank ? `${endLine(last.text)}\n${line.trimStart()}` : appendLine(last.text, line);
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
	for (const block of blocks) if (block.kind === "list") for (const item of block.items) item.text = endLine(item.text);
	return blocks;
}

/**
 * Ends an unfinished inline span in a reply that is still streaming, so the
 * reader sees **bold** arriving rather than the asterisks that open it. Display
 * only: copying keeps the stored text. Text inside an open fence is code, and is
 * left as it is.
 */
export function repairStreamingInline(text: string): string {
	let open: string | null = null;
	for (const line of text.split("\n")) {
		const fence = FENCE.exec(line);
		if (!fence) continue;
		if (open === null) open = fence[2][0];
		else if (fence[2][0] === open && !fence[3].trim()) open = null;
	}
	if (open !== null) return text;
	const tail = text.slice(text.lastIndexOf("\n\n") + 1);
	// Code spans first: emphasis markers inside them are not emphasis.
	let repaired = text;
	if (countUnescaped(tail, "`") % 2 === 1) return `${repaired}\``;
	const prose = tail.replace(/`[^`]*`/g, "");
	if (countUnescaped(prose, "**") % 2 === 1) repaired += "**";
	if (countUnescaped(prose, "~~") % 2 === 1) repaired += "~~";
	return repaired;
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

/** A blockquote's own Markdown as quoted prose: its lists keep one line per item. */
function quoteText(value: string, relativeImages: boolean): string {
	return parseBlocks(value, { relativeImages }).map((block) => {
		switch (block.kind) {
			case "list": return block.items.map((item) => `${"  ".repeat(item.depth)}${item.checked !== undefined ? (item.checked ? "☑ " : "☐ ") : item.number !== undefined ? `${item.number}. ` : "• "}${item.text}`).join("\n");
			case "table": return [block.headers, ...block.rows].map((row) => row.join(" · ")).join("\n");
			case "image": return block.alt || block.url;
			case "rule": return "";
			default: return block.text;
		}
	}).join("\n");
}

/**
 * A paragraph's source lines as the text it reads as. A plain line break is a
 * soft break, a space, as on the desktop and GitHub; a line ending in two spaces
 * or a backslash is a hard break.
 */
function joinLines(lines: string[]): string {
	let text = "";
	for (const line of lines) text = text ? appendLine(text, line) : line.trimStart();
	return endLine(text);
}

/** `before` followed by the next source line, across a hard or a soft break. */
function appendLine(before: string, line: string): string {
	const next = line.trimStart();
	return HARD_BREAK.test(before) ? `${endLine(before)}\n${next}` : `${before.trimEnd()} ${next}`;
}

const HARD_BREAK = /(?: {2,}|\\)$/;

/** A line without the trailing spaces or backslash that would have broken it. */
function endLine(text: string): string {
	return text.replace(HARD_BREAK, "").trimEnd();
}

function leadingSpaces(line: string): number {
	return /^ */.exec(line)![0].length;
}

/** Removes up to `count` leading spaces: a fence's own indent from its code. */
function dedent(line: string, count: number): string {
	return line.slice(Math.min(count, leadingSpaces(line)));
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
