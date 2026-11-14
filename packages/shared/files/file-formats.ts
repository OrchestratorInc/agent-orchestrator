// Which files the artifact and file viewers draw, and how. Shared so the desktop
// and the mobile app preview the same formats: a file one previews and the other
// shows as text or as "not available" reads as a bug.

export type FileMediaKind = "image" | "video" | "audio" | "pdf";

/** Raster formats the app previews as images, matching the daemon's workspace blob route. */
export function isRasterImagePath(path: string): boolean {
	return /\.(apng|avif|bmp|gif|ico|jpe?g|png|webp)$/i.test(path);
}

/** A Mermaid diagram source file, which renders as its diagram. */
export function isMermaidPath(path: string): boolean {
	return /\.(mmd|mermaid)$/i.test(path);
}

/** SVG is text, so it keeps its code view and gains a rendered one. */
export function isSvgPath(path: string): boolean {
	return /\.svg$/i.test(path);
}

/** Common media containers; decoding failures stay in the viewer. */
export function fileMediaKind(path: string): FileMediaKind | undefined {
	if (isRasterImagePath(path)) return "image";
	if (/\.(m4v|mov|mp4|ogv|webm)$/i.test(path)) return "video";
	if (/\.(aac|flac|m4a|mp3|oga|ogg|opus|wav)$/i.test(path)) return "audio";
	if (/\.pdf$/i.test(path)) return "pdf";
	return undefined;
}

/** CSV and TSV files, which preview as a table. */
export function isDelimitedTextPath(path: string): boolean {
	return /\.(csv|tsv)$/i.test(path);
}

/**
 * Parses CSV (or TSV for a `.tsv` path) into rows of cells. Quoted fields may
 * hold the delimiter, newlines, and `""` for a literal quote, and may follow
 * leading whitespace. A trailing newline does not add an empty row.
 */
export function parseDelimitedText(content: string, path: string): string[][] {
	const delimiter = /\.tsv$/i.test(path) ? "\t" : ",";
	const text = content.startsWith("\uFEFF") ? content.slice(1) : content;
	const rows: string[][] = [];
	let row: string[] = [];
	let cell = "";
	let quoted = false;
	// A quoted field begun on this line: `""` is a field even though it is empty.
	let fieldStarted = false;
	for (let index = 0; index < text.length; index += 1) {
		const char = text[index];
		if (quoted) {
			if (char !== "\"") cell += char;
			else if (text[index + 1] === "\"") {
				cell += "\"";
				index += 1;
			} else quoted = false;
		} else if (char === "\"" && cell.trim() === "") {
			// A quote after only whitespace (`a, "b,c"`) still opens a quoted field.
			quoted = true;
			fieldStarted = true;
			cell = "";
		} else if (char === delimiter) {
			row.push(cell);
			cell = "";
		} else if (char === "\n" || char === "\r") {
			if (char === "\r" && text[index + 1] === "\n") index += 1;
			row.push(cell);
			rows.push(row);
			row = [];
			cell = "";
			fieldStarted = false;
		} else {
			cell += char;
		}
	}
	if (cell !== "" || row.length > 0 || fieldStarted) {
		row.push(cell);
		rows.push(row);
	}
	return rows;
}

// A comma is accepted only as a thousands separator (`1,000`), so a locale
// decimal like `1,5` is not read as a number.
const NUMBER = /^-?(?=\.?\d)(?:\d{1,3}(?:,\d{3})+|\d*)(?:\.\d+)?(?:e[+-]?\d+)?%?$/i;

/** Whether a table cell reads as a number, so its column right-aligns and sorts by value. */
export function isNumericCell(cell: string): boolean {
	return NUMBER.test(cell);
}

/** A numeric cell's value without thousands separators or a percent sign. */
export function cellNumber(cell: string): number {
	return Number(cell.trim().replace(/[,%]/g, ""));
}

/**
 * A delimited file as a table: the first row is the header, every row is
 * padded to the widest, and a column whose non-empty cells are all numbers is
 * marked numeric.
 */
export function delimitedTable(content: string, path: string): { header: string[]; rows: string[][]; numeric: boolean[] } {
	const [first = [], ...rest] = parseDelimitedText(content, path);
	const width = rest.reduce((max, row) => Math.max(max, row.length), first.length);
	const pad = (row: string[]) => Array.from({ length: width }, (_, index) => row[index] ?? "");
	const rows = rest.map(pad);
	const numeric = Array.from({ length: width }, (_, column) => {
		const values = rows.map((row) => row[column].trim()).filter(Boolean);
		return values.length > 0 && values.every(isNumericCell);
	});
	return { header: pad(first), rows, numeric };
}

export type TableSort = { column: number; direction: "ascending" | "descending" };

/**
 * Rows ordered by one column: numeric columns by value, text columns by
 * `compareText`; empty cells stay last either way. Without a sort, file order.
 */
export function sortTableRows(rows: string[][], numeric: readonly boolean[], sort: TableSort | null, compareText: (a: string, b: string) => number): string[][] {
	if (!sort) return rows;
	const order = sort.direction === "ascending" ? 1 : -1;
	const compare = numeric[sort.column] ? (a: string, b: string) => cellNumber(a) - cellNumber(b) : compareText;
	const keyed = rows.map((row) => ({ row, cell: row[sort.column] ?? "", empty: !(row[sort.column] ?? "").trim() }));
	keyed.sort((a, b) => a.empty ? (b.empty ? 0 : 1) : b.empty ? -1 : order * compare(a.cell, b.cell));
	return keyed.map(({ row }) => row);
}

/** A header tap's next sort: ascending, then descending, then file order. */
export function nextTableSort(current: TableSort | null, column: number): TableSort | null {
	if (current?.column !== column) return { column, direction: "ascending" };
	return current.direction === "ascending" ? { column, direction: "descending" } : null;
}

/**
 * Above this, a diagram file stays source text. Mermaid lays out synchronously,
 * and a 100KB "diagram" is really a log file with the wrong label.
 */
export const MAX_DIAGRAM_CHARS = 20_000;

/** Empty or oversized sources have nothing worth laying out; callers show the source. */
export function isRenderableDiagram(code: string): boolean {
	return code.trim().length > 0 && code.length <= MAX_DIAGRAM_CHARS;
}
