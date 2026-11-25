// Pure diff-parsing logic shared between the main thread (small diffs, where
// a worker round-trip would cost more than it saves) and the diff-parser Web
// Worker (large diffs, where this keeps the parse + intra-line LCS off the
// render thread entirely). No DOM, no React — safe to run in either context.

export type DiffRowKind = "context" | "add" | "del" | "hunk";

export type DiffSegment = { text: string; changed: boolean };

export type DiffRow = {
	kind: DiffRowKind;
	oldNo: number | null;
	newNo: number | null;
	text: string;
	// hunk rows only: the enclosing function/section context after the @@ range.
	section?: string;
	// add/del rows only: intra-line word-level highlight of what changed.
	segments?: DiffSegment[];
};

// Git file-header lines carry no reviewable content, so they are hidden — the
// panel reads like a diff instead of a raw `git diff` dump. Matched by prefix
// after line endings are normalized, so it behaves the same on every OS.
const gitHeaderPrefixes = [
	"diff --git ",
	"index ",
	"old mode ",
	"new mode ",
	"new file mode ",
	"deleted file mode ",
	"similarity index ",
	"dissimilarity index ",
	"rename from ",
	"rename to ",
	"copy from ",
	"copy to ",
	"--- ",
	"+++ ",
];

const hunkHeaderPattern = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

// parseUnifiedDiff turns a raw `git diff` string into typed rows with real
// per-side line numbers. Line endings are normalized first (Windows \r\n and
// classic-Mac \r as well as Unix \n) so numbering and marker detection are
// identical across operating systems.
export function parseUnifiedDiff(raw: string): DiffRow[] {
	if (!raw) return [];
	const lines = raw.replace(/\r\n?/g, "\n").split("\n");
	if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
	const rows: DiffRow[] = [];
	let oldNo = 0;
	let newNo = 0;
	let inHunk = false;
	// The lines a hunk's header says are still to come on each side, when it says.
	// While any remain, every line is the file's own, however it starts: a removed
	// `-- comment` reads `--- comment`, an added `++ x` reads `+++ x`.
	let oldLeft: number | undefined;
	let newLeft: number | undefined;
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index]!;
		const counting = oldLeft !== undefined && newLeft !== undefined && (oldLeft > 0 || newLeft > 0);
		if (!counting && gitHeaderPrefixes.some((prefix) => line.startsWith(prefix))) {
			// Outside a hunk it is a header. Inside one whose size is not stated, only a
			// new file's header ends it: `diff --git`, or a `---` line just before its
			// `+++` line. Any other header-like line is the file's own.
			if (!inHunk) continue;
			if (line.startsWith("diff --git ") || (line.startsWith("--- ") && lines[index + 1]?.startsWith("+++ "))) {
				inHunk = false;
				continue;
			}
		}
		// A hunk header is never a file's line (one starts with its marker), so it always
		// opens a hunk, even after one whose stated size was wrong.
		if (line.startsWith("@@")) {
			const hunk = hunkHeaderPattern.exec(line);
			oldNo = hunk ? Number(hunk[1]) : 1;
			newNo = hunk ? Number(hunk[3]) : 1;
			// A header that states both sizes is counted; one without them is not.
			oldLeft = hunk?.[2] !== undefined && hunk[4] !== undefined ? Number(hunk[2]) : undefined;
			newLeft = hunk?.[2] !== undefined && hunk[4] !== undefined ? Number(hunk[4]) : undefined;
			inHunk = true;
			const sectionStart = line.indexOf("@@", 2);
			const range = sectionStart >= 0 ? line.slice(0, sectionStart + 2) : line;
			const section = sectionStart >= 0 ? line.slice(sectionStart + 2).trim() : "";
			rows.push({ kind: "hunk", oldNo: null, newNo: null, text: range, section: section || undefined });
			continue;
		}
		if (!inHunk) continue;
		if (line.startsWith("\\")) continue; // "\ No newline at end of file"
		const marker = line[0];
		const text = line.slice(1);
		if (marker === "+") {
			rows.push({ kind: "add", oldNo: null, newNo, text });
			newNo += 1;
			if (newLeft !== undefined) newLeft -= 1;
		} else if (marker === "-") {
			rows.push({ kind: "del", oldNo, newNo: null, text });
			oldNo += 1;
			if (oldLeft !== undefined) oldLeft -= 1;
		} else {
			rows.push({ kind: "context", oldNo, newNo, text });
			oldNo += 1;
			newNo += 1;
			if (oldLeft !== undefined) oldLeft -= 1;
			if (newLeft !== undefined) newLeft -= 1;
		}
	}
	annotateIntraLine(rows);
	return rows;
}

// Longest line length still worth an intra-line (word-level) diff. The LCS is
// O(tokens^2), so very long lines are left as whole-line highlights.
const maxIntraLineChars = 400;

// annotateIntraLine pairs each run of deleted lines with the equal-length run of
// added lines that immediately follows it (a line-for-line replacement) and
// marks the exact tokens that changed within each pair, so the UI can highlight
// what actually changed instead of tinting the whole line.
function annotateIntraLine(rows: DiffRow[]): void {
	let i = 0;
	while (i < rows.length) {
		if (rows[i].kind !== "del") {
			i += 1;
			continue;
		}
		let delEnd = i;
		while (delEnd < rows.length && rows[delEnd].kind === "del") delEnd += 1;
		let addEnd = delEnd;
		while (addEnd < rows.length && rows[addEnd].kind === "add") addEnd += 1;
		const dels = delEnd - i;
		const adds = addEnd - delEnd;
		if (dels > 0 && dels === adds) {
			for (let k = 0; k < dels; k += 1) {
				const del = rows[i + k];
				const add = rows[delEnd + k];
				if (del.text.length > maxIntraLineChars || add.text.length > maxIntraLineChars) continue;
				const { oldSegments, newSegments } = intraLineSegments(del.text, add.text);
				del.segments = oldSegments;
				add.segments = newSegments;
			}
		}
		i = addEnd > i ? addEnd : i + 1;
	}
}

function tokenizeLine(value: string): string[] {
	return value.match(/\s+|[A-Za-z0-9_]+|[^\sA-Za-z0-9_]/g) ?? [];
}

function pushSegment(segments: DiffSegment[], text: string, changed: boolean): void {
	const last = segments[segments.length - 1];
	if (last && last.changed === changed) last.text += text;
	else segments.push({ text, changed });
}

// intraLineSegments token-diffs two lines via LCS and returns the tokens that
// only exist on one side marked as changed.
function intraLineSegments(oldText: string, newText: string): { oldSegments: DiffSegment[]; newSegments: DiffSegment[] } {
	const a = tokenizeLine(oldText);
	const b = tokenizeLine(newText);
	const lcs: number[][] = Array.from({ length: a.length + 1 }, () => new Array(b.length + 1).fill(0));
	for (let x = a.length - 1; x >= 0; x -= 1) {
		for (let y = b.length - 1; y >= 0; y -= 1) {
			lcs[x][y] = a[x] === b[y] ? lcs[x + 1][y + 1] + 1 : Math.max(lcs[x + 1][y], lcs[x][y + 1]);
		}
	}
	const oldSegments: DiffSegment[] = [];
	const newSegments: DiffSegment[] = [];
	let x = 0;
	let y = 0;
	while (x < a.length && y < b.length) {
		if (a[x] === b[y]) {
			pushSegment(oldSegments, a[x], false);
			pushSegment(newSegments, b[y], false);
			x += 1;
			y += 1;
		} else if (lcs[x + 1][y] >= lcs[x][y + 1]) {
			pushSegment(oldSegments, a[x], true);
			x += 1;
		} else {
			pushSegment(newSegments, b[y], true);
			y += 1;
		}
	}
	while (x < a.length) pushSegment(oldSegments, a[x++], true);
	while (y < b.length) pushSegment(newSegments, b[y++], true);
	return { oldSegments, newSegments };
}
