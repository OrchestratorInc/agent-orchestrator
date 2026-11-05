/** The delimiter for a CSV or TSV path, or undefined for any other file. */
export function tabularDelimiter(path: string): "," | "\t" | undefined {
	if (/\.csv$/i.test(path)) return ",";
	if (/\.tsv$/i.test(path)) return "\t";
	return undefined;
}

/**
 * The leading rows of delimited text, each cut to maxColumns cells. Quoted
 * fields may contain the delimiter, doubled quotes, and line breaks; blank
 * lines are skipped.
 */
export function csvPreviewRows(content: string, delimiter: string, maxRows: number, maxColumns: number): string[][] {
	const rows: string[][] = [];
	let row: string[] = [];
	let field = "";
	let quoted = false;
	const endRow = () => {
		row.push(field);
		if (row.length > 1 || row[0] !== "") rows.push(row.slice(0, maxColumns));
		row = [];
		field = "";
	};
	const text = content.startsWith("﻿") ? content.slice(1) : content;
	for (let index = 0; index < text.length && rows.length < maxRows; index += 1) {
		const char = text[index];
		if (quoted) {
			if (char !== "\"") field += char;
			else if (text[index + 1] === "\"") {
				field += "\"";
				index += 1;
			} else quoted = false;
		} else if (char === "\"" && field === "") {
			quoted = true;
		} else if (char === delimiter) {
			row.push(field);
			field = "";
		} else if (char === "\n" || char === "\r") {
			if (char === "\r" && text[index + 1] === "\n") index += 1;
			endRow();
		} else {
			field += char;
		}
	}
	if (rows.length < maxRows && (field !== "" || row.length > 0)) endRow();
	return rows;
}
