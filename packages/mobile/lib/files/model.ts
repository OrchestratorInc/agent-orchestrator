import type { DiffRow } from "../../../shared/files/diff-parser";
import type { FileAnnotationTarget } from "../../../shared/files/file-annotations";
import type { WorkspaceFileStatus, WorkspaceFileSummary, WorkspaceManifest, WorkspaceSection } from "./api";

// ---- Changed-file list -------------------------------------------------------

/** A changed file with every git section it shows up in (staged and unstaged at once, say). */
export type ChangeEntry = WorkspaceFileSummary & { sections: WorkspaceSection[] };
export type ChangeGroup = { key: "uncommitted" | "committed"; title: string; files: ChangeEntry[] };

const SECTION_ORDER: WorkspaceSection[] = ["staged", "unstaged", "untracked", "committed"];

/**
 * Two groups instead of desktop's four sections: on a phone the question is
 * "what has the agent not committed yet" versus "what is already on the branch".
 * Each path appears once — a committed file edited again belongs with the
 * uncommitted work — and keeps its git sections as badges. Counts come from the
 * combined (base → worktree) list when it has the path, matching the diff the
 * viewer opens.
 */
export function changeGroups(manifest: Pick<WorkspaceManifest, "files" | "sections">): ChangeGroup[] {
	const combined = new Map(manifest.files.map((file) => [file.path, file]));
	const found = new Map<string, { file: WorkspaceFileSummary; sections: WorkspaceSection[] }>();
	for (const section of SECTION_ORDER) {
		for (const file of manifest.sections[section] ?? []) {
			const seen = found.get(file.path);
			if (seen) {
				if (!seen.sections.includes(section)) seen.sections.push(section);
			} else {
				found.set(file.path, { file, sections: [section] });
			}
		}
	}
	// A degraded manifest can list files without sections; they are still changes.
	for (const file of manifest.files) if (!found.has(file.path)) found.set(file.path, { file, sections: [] });

	const uncommitted: ChangeEntry[] = [];
	const committed: ChangeEntry[] = [];
	for (const [path, { file, sections }] of found) {
		const entry: ChangeEntry = { ...file, ...combined.get(path), sections };
		if (sections.length === 1 && sections[0] === "committed") committed.push(entry);
		else uncommitted.push(entry);
	}
	const byPath = (a: ChangeEntry, b: ChangeEntry) => a.path.localeCompare(b.path);
	return [
		{ key: "uncommitted" as const, title: "Uncommitted", files: uncommitted.sort(byPath) },
		{ key: "committed" as const, title: "Committed on branch", files: committed.sort(byPath) },
	].filter((group) => group.files.length > 0);
}

/** Every changed file in list order — what the viewer's previous/next buttons walk. */
export function orderedChanges(groups: ChangeGroup[]): ChangeEntry[] {
	return groups.flatMap((group) => group.files);
}

export function splitPath(path: string): { name: string; dir: string } {
	const slash = path.lastIndexOf("/");
	return slash < 0 ? { name: path, dir: "" } : { name: path.slice(slash + 1), dir: path.slice(0, slash) };
}

export const statusLabel: Record<WorkspaceFileStatus, string> = { modified: "Modified", added: "Added", deleted: "Deleted", renamed: "Renamed", unmodified: "Unchanged" };
export const sectionLabel: Record<WorkspaceSection, string> = { staged: "Staged", unstaged: "Unstaged", untracked: "Untracked", committed: "Committed" };

/** What VoiceOver/TalkBack reads for a changed-file row. */
export function changeAccessibilityLabel(file: Pick<ChangeEntry, "path" | "previousPath" | "status" | "additions" | "deletions" | "binary">): string {
	const { name, dir } = splitPath(file.path);
	const where = dir ? ` in ${dir}` : "";
	const renamed = file.previousPath ? `, renamed from ${file.previousPath}` : "";
	const counts = file.binary ? ", binary file" : `, ${plural(file.additions, "line")} added, ${plural(file.deletions, "line")} removed`;
	return `${name}${where}, ${statusLabel[file.status].toLowerCase()}${renamed}${counts}`;
}

/** 2395 → "2.4k": for pills read at a glance; the Files screen shows exact numbers. */
export function compactCount(value: number): string {
	if (value < 1000) return String(value);
	if (value < 10_000) return `${Math.round(value / 100) / 10}k`;
	return `${Math.round(value / 1000)}k`;
}

export function plural(count: number, noun: string): string {
	return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

// ---- File kinds --------------------------------------------------------------

const NAMED_LANGUAGES: Record<string, string> = { dockerfile: "shell", makefile: "shell", ".bashrc": "shell", ".zshrc": "shell" };
const IMAGE_EXTENSIONS = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "ico", "heic"]);

function extension(path: string): string {
	const name = splitPath(path).name.toLowerCase();
	const dot = name.lastIndexOf(".");
	return dot > 0 ? name.slice(dot + 1) : "";
}

/** The highlighter's language for a path; it resolves its own aliases (ts, py, yml…). */
export function languageForPath(path: string): string | undefined {
	const named = NAMED_LANGUAGES[splitPath(path).name.toLowerCase()];
	return named ?? (extension(path) || undefined);
}

export function isImagePath(path: string): boolean {
	return IMAGE_EXTENSIONS.has(extension(path));
}

export function isSvgPath(path: string): boolean {
	return extension(path) === "svg";
}

export function isMarkdownPath(path: string): boolean {
	return ["md", "markdown", "mdx"].includes(extension(path));
}

// ---- Diff display rows -----------------------------------------------------------

export type LineRow = DiffRow & { kind: "context" | "add" | "del" };

export type DisplayRow =
	| { type: "line"; key: string; row: LineRow }
	| {
		type: "fold";
		key: string;
		/** Reveal state key: `before:<hunk>` grows upward from a hunk, `after` grows down from the last one. */
		id: string;
		hidden: number;
		/** The function or section git names in the hunk header, when it names one. */
		section?: string;
		expandable: boolean;
	}
	/** A comment drawn under the line it is about. */
	| { type: "comment"; key: string; commentId: string };

/** Lines one tap on a fold reveals. */
export const CONTEXT_STEP = 20;

const HUNK_HEADER = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

type Hunk = { firstNew: number; nextNew: number; oldOffset: number; section?: string; rows: LineRow[] };

function hunksOf(rows: DiffRow[]): Hunk[] {
	const hunks: Hunk[] = [];
	for (const row of rows) {
		if (row.kind === "hunk") {
			const match = HUNK_HEADER.exec(row.text);
			const oldStart = match ? Number(match[1]) : 1;
			const oldCount = match?.[2] === undefined ? 1 : Number(match[2]);
			const newStart = match ? Number(match[3]) : 1;
			const newCount = match?.[4] === undefined ? 1 : Number(match[4]);
			// A zero count names the line *before* the change, so the hunk's own
			// first line is one further on.
			const firstNew = newCount === 0 ? newStart + 1 : newStart;
			const firstOld = oldCount === 0 ? oldStart + 1 : oldStart;
			hunks.push({ firstNew, nextNew: firstNew + newCount, oldOffset: firstOld - firstNew, section: row.section, rows: [] });
		} else if (hunks.length) {
			hunks[hunks.length - 1].rows.push(row as LineRow);
		}
	}
	return hunks;
}

/**
 * Splits `content` the way the parser splits a diff, so line N here is new-side line N.
 * Returns null when it cannot stand in for the file (truncated, or there is none).
 */
export function contentLines(content: string | undefined, truncated: boolean): string[] | null {
	if (truncated || content == null) return null;
	const lines = content.replace(/\r\n?/g, "\n").split("\n");
	if (lines.length && lines[lines.length - 1] === "") lines.pop();
	return lines;
}

/**
 * The rows the diff viewer draws: the hunks' own lines, with the unchanged code
 * between them folded into one tappable row each. `revealed` says how many
 * lines each fold has given back, so expanding is incremental like GitHub's
 * "Show more lines" rather than all-or-nothing. Without the full file (binary,
 * truncated, deleted) folds still report their size but cannot open.
 */
export function diffDisplayRows(rows: DiffRow[], lines: string[] | null, revealed: Readonly<Record<string, number>> = {}): DisplayRow[] {
	const out: DisplayRow[] = [];
	const hunks = hunksOf(rows);
	let nextNew = 1;
	const context = (from: number, to: number, offset: number) => {
		for (let n = from; n <= to; n += 1) {
			out.push({ type: "line", key: `x:${n}`, row: { kind: "context", oldNo: n + offset, newNo: n, text: lines?.[n - 1] ?? "" } });
		}
	};
	hunks.forEach((hunk, index) => {
		const hidden = Math.max(0, hunk.firstNew - nextNew);
		const expandable = lines !== null && hunk.firstNew - 1 <= lines.length;
		const shown = expandable ? Math.min(hidden, revealed[`before:${index}`] ?? 0) : 0;
		if (hidden - shown > 0) {
			out.push({ type: "fold", key: `fold:${index}`, id: `before:${index}`, hidden: hidden - shown, section: hunk.section, expandable });
		}
		context(hunk.firstNew - shown, hunk.firstNew - 1, hunk.oldOffset);
		hunk.rows.forEach((row, rowIndex) => out.push({ type: "line", key: `${index}:${rowIndex}`, row }));
		nextNew = Math.max(nextNew, hunk.nextNew);
	});
	if (lines && hunks.length) {
		const last = hunks[hunks.length - 1];
		const lastOffset = last.oldOffset + last.rows.reduce((sum, row) => sum + (row.kind === "del" ? 1 : row.kind === "add" ? -1 : 0), 0);
		const hidden = Math.max(0, lines.length - (nextNew - 1));
		const shown = Math.min(hidden, revealed.after ?? 0);
		context(nextNew, nextNew + shown - 1, lastOffset);
		if (hidden - shown > 0) out.push({ type: "fold", key: "fold:after", id: "after", hidden: hidden - shown, expandable: true });
	}
	return out;
}

/** The whole file as rows, for the File tab: one context row per line. */
export function fileDisplayRows(lines: string[]): DisplayRow[] {
	return lines.map((text, index) => ({ type: "line", key: `f:${index + 1}`, row: { kind: "context", oldNo: index + 1, newNo: index + 1, text } }));
}

// ---- Selection → feedback -----------------------------------------------------

/** Where a comment on these lines is drawn: under the last of them, on the side it shows. */
export function commentAnchor(lines: LineRow[]): { side: "old" | "new"; line: number } | undefined {
	const last = lines[lines.length - 1];
	if (!last) return undefined;
	if (last.kind === "del") return last.oldNo == null ? undefined : { side: "old", line: last.oldNo };
	return last.newNo == null ? undefined : { side: "new", line: last.newNo };
}

/**
 * Inserts a comment row after the line each comment is anchored to, oldest
 * first. A comment whose line is folded away or gone from the diff is not
 * drawn; the review sheet still lists it.
 */
export function withInlineComments(rows: DisplayRow[], notes: ReadonlyArray<{ id: string; anchor?: { side: "old" | "new"; line: number } }>): DisplayRow[] {
	if (!notes.length) return rows;
	const byLine = new Map<string, string[]>();
	for (const note of notes) {
		if (!note.anchor) continue;
		const key = `${note.anchor.side}:${note.anchor.line}`;
		byLine.set(key, [...(byLine.get(key) ?? []), note.id]);
	}
	if (!byLine.size) return rows;
	const out: DisplayRow[] = [];
	for (const row of rows) {
		out.push(row);
		if (row.type !== "line") continue;
		const key = row.row.kind === "del" ? `old:${row.row.oldNo}` : `new:${row.row.newNo}`;
		const ids = byLine.get(key);
		if (!ids) continue;
		// A context line has both numbers; draw its comments once, under the new side.
		byLine.delete(key);
		for (const commentId of ids) out.push({ type: "comment", key: `comment:${commentId}`, commentId });
	}
	return out;
}

/** The line rows between two display indexes, inclusive, in reading order. */
export function selectedLines(rows: DisplayRow[], anchor: number, focus: number): LineRow[] {
	const [from, to] = anchor <= focus ? [anchor, focus] : [focus, anchor];
	const out: LineRow[] = [];
	for (let index = from; index <= to; index += 1) {
		const item = rows[index];
		if (item?.type === "line") out.push(item.row);
	}
	return out;
}

/**
 * The comment's heading: "Line 42", "Lines 42–48", "Removed lines 12–14", or
 * "Line 7 (before and after)" for a replaced line. Numbers come from the side
 * the reader is looking at — new lines when there are any, old ones otherwise —
 * and span the whole selection, not just its first and last rows.
 */
export function selectionLabel(lines: LineRow[]): string {
	if (!lines.length) return "No lines";
	const onlyRemoved = lines.every((row) => row.kind === "del");
	const numbers = lines
		.map((row) => onlyRemoved ? row.oldNo : row.newNo)
		.filter((value): value is number => value != null);
	const prefix = onlyRemoved ? "Removed line" : "Line";
	if (!numbers.length) return prefix;
	const start = Math.min(...numbers);
	const end = Math.max(...numbers);
	if (start !== end) return `${prefix}s ${start}–${end}`;
	return lines.length > 1 && !onlyRemoved ? `${prefix} ${start} (before and after)` : `${prefix} ${start}`;
}

/**
 * The shared annotation shape desktop sends. One line maps exactly onto its
 * fields; a range keeps the first line's numbers and quotes the rest, since the
 * annotation format has no end line of its own.
 */
export function annotationTarget(
	file: { path: string; previousPath?: string; workspaceVersion?: string; fileFingerprint?: string },
	lines: LineRow[],
): FileAnnotationTarget {
	const first = lines[0];
	const side = lines.every((row) => row.kind === "del") ? "old" : "new";
	const base: FileAnnotationTarget = {
		path: file.path,
		previousPath: file.previousPath,
		side,
		workspaceVersion: file.workspaceVersion || undefined,
		fileFingerprint: file.fileFingerprint || undefined,
	};
	if (!first) return { ...base, side: "file" };
	const startLine = side === "old" ? first.oldNo ?? undefined : first.newNo ?? first.oldNo ?? undefined;
	if (lines.length === 1) {
		return { ...base, line: startLine, oldLine: first.oldNo ?? undefined, newLine: first.newNo ?? undefined, lineKind: first.kind, lineText: first.text };
	}
	const quote = `${selectionLabel(lines)}:\n${lines.map((row) => `${row.kind === "add" ? "+" : row.kind === "del" ? "-" : " "} ${row.text}`).join("\n")}`;
	return { ...base, line: startLine, oldLine: first.oldNo ?? undefined, newLine: first.newNo ?? undefined, quote };
}
