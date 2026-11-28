/**
 * A Markdown text's code, as [start, end) offsets: fenced blocks and inline
 * code spans. Text inside code is shown as written: an attachment link there is
 * an example, not a file, and HTML there is not cleaned up.
 *
 * A fence is a line that starts, after any indent, with three or more
 * backticks or tildes (a backtick fence's line has no other backtick); it runs
 * to the next line of at least as many of the same character, or to the end.
 * A code span opens with a run of backticks and closes at the next run of
 * exactly as many, within its paragraph; a run with no such partner is plain
 * text. A backslash before a backtick makes that backtick plain text.
 *
 * The daemon's markdownCodeRanges (backend/internal/service/pr/markdown_code.go)
 * mirrors this, so the app and the daemon agree on which files a comment links.
 */
export function markdownCodeRanges(text: string): [number, number][] {
	const ranges: [number, number][] = [];
	let lineStart = true;
	for (let i = 0; i < text.length;) {
		if (lineStart) {
			const fenceEnd = fencedBlockEnd(text, i);
			if (fenceEnd !== undefined) {
				ranges.push([i, fenceEnd]);
				i = fenceEnd;
				continue;
			}
		}
		const c = text[i];
		if (c === "\n") {
			lineStart = true;
			i++;
		} else if (c === "\\" && text[i + 1] === "`") {
			lineStart = false;
			i += 2;
		} else if (c === "`") {
			lineStart = false;
			const run = runLength(text, i, "`");
			const spanEnd = codeSpanEnd(text, i + run, run);
			if (spanEnd !== undefined) {
				ranges.push([i, spanEnd]);
				i = spanEnd;
			} else {
				i += run;
			}
		} else {
			lineStart = false;
			i++;
		}
	}
	return ranges;
}

/** Where a fenced block opened by the line at start ends, if that line opens one. */
function fencedBlockEnd(text: string, start: number): number | undefined {
	const [line, next] = lineAt(text, start);
	const indent = line.length - line.replace(/^[ \t]+/, "").length;
	const marker = line[indent];
	if (marker !== "`" && marker !== "~") return undefined;
	const size = runLength(line, indent, marker);
	if (size < 3 || (marker === "`" && line.slice(indent + size).includes("`"))) return undefined;
	for (let at = next; at < text.length;) {
		const [closing, after] = lineAt(text, at);
		const body = closing.replace(/^[ \t]+/, "");
		const length = runLength(body, 0, marker);
		if (length >= size && body.slice(length).trim() === "") return after;
		at = after;
	}
	return text.length;
}

/** Where a code span opened by a run of size backticks ends: after the next run of exactly size, before a blank line. */
function codeSpanEnd(text: string, from: number, size: number): number | undefined {
	for (let i = from; i < text.length;) {
		if (text[i] === "`") {
			const run = runLength(text, i, "`");
			if (run === size) return i + run;
			i += run;
		} else if (text[i] === "\n") {
			if (lineAt(text, i + 1)[0].trim() === "") return undefined;
			i++;
		} else {
			i++;
		}
	}
	return undefined;
}

/** The line starting at start, without its newline, and where the next line starts. */
function lineAt(text: string, start: number): [string, number] {
	const end = text.indexOf("\n", start);
	return end < 0 ? [text.slice(start), text.length] : [text.slice(start, end), end + 1];
}

function runLength(text: string, start: number, c: string): number {
	let n = 0;
	while (text[start + n] === c) n++;
	return n;
}

/** The text with fn applied to each stretch outside code; code is left exactly as written. */
export function mapOutsideMarkdownCode(text: string, fn: (prose: string) => string): string {
	let out = "";
	let last = 0;
	for (const [start, end] of markdownCodeRanges(text)) {
		out += fn(text.slice(last, start)) + text.slice(start, end);
		last = end;
	}
	return out + fn(text.slice(last));
}
