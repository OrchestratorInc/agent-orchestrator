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
		} else {
			cell += char;
		}
	}
	if (cell !== "" || row.length > 0) {
		row.push(cell);
		rows.push(row);
	}
	return rows;
}
