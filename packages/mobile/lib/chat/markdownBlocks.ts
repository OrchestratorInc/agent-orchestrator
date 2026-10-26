export type MarkdownBlock =
	| { kind: "paragraph"; text: string }
	| { kind: "heading"; text: string; level: number }
	| { kind: "quote"; text: string }
	| { kind: "list"; ordered: boolean; items: Array<{ text: string; checked?: boolean }> }
	| { kind: "code"; language?: string; text: string }
	| { kind: "table"; headers: string[]; align: TableAlign[]; rows: string[][] }
	| { kind: "image"; alt: string; url: string }
	| { kind: "rule" };

export type TableAlign = "left" | "center" | "right" | undefined;

/** Pure GFM-shaped block parser; unknown syntax remains readable paragraph text. */
export function parseBlocks(input: string): MarkdownBlock[] {
	const lines = input.replace(/\r/g, "").split("\n");
	const blocks: MarkdownBlock[] = [];
	let paragraph: string[] = [];
	const flushParagraph = () => {
		if (paragraph.length) blocks.push({ kind: "paragraph", text: paragraph.join("\n").trim() });
		paragraph = [];
	};
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
		if (line.startsWith("```")) {
			flushParagraph();
			const language = line.slice(3).trim() || undefined;
			const code: string[] = [];
			for (i += 1; i < lines.length && !lines[i].startsWith("```"); i++) code.push(lines[i]);
			blocks.push({ kind: "code", language, text: code.join("\n") });
			continue;
		}
		const heading = /^(#{1,6})\s+(.+)$/.exec(line);
		if (heading) {
			flushParagraph();
			blocks.push({ kind: "heading", level: heading[1].length, text: heading[2] });
			continue;
		}
		const image = /^!\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)\s*$/.exec(line.trim());
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
		if (/^\s*(---+|\*\*\*+)\s*$/.test(line)) {
			flushParagraph();
			blocks.push({ kind: "rule" });
			continue;
		}
		const list = /^\s*(?:(\d+)\.|[-*+])\s+(.+)$/.exec(line);
		if (list) {
			flushParagraph();
			const ordered = Boolean(list[1]);
			const items = [taskItem(list[2])];
			while (i + 1 < lines.length) {
				const next = /^\s*(?:(\d+)\.|[-*+])\s+(.+)$/.exec(lines[i + 1]);
				if (!next || Boolean(next[1]) !== ordered) break;
				items.push(taskItem(next[2]));
				i += 1;
			}
			blocks.push({ kind: "list", ordered, items });
			continue;
		}
		if (/^>\s?/.test(line)) {
			flushParagraph();
			const quote = [line.replace(/^>\s?/, "")];
			while (i + 1 < lines.length && /^>\s?/.test(lines[i + 1])) quote.push(lines[++i].replace(/^>\s?/, ""));
			blocks.push({ kind: "quote", text: quote.join("\n") });
			continue;
		}
		if (!line.trim()) flushParagraph();
		else paragraph.push(line);
	}
	flushParagraph();
	return blocks;
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
