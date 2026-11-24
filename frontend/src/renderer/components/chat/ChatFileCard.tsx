/**
 * A file the agent read or edited, drawn as the file itself.
 *
 * One card for both: the file's icon and workspace path on top, the code under it
 * with one column of line numbers. A read shows the lines it returned; an edit shows
 * its patch in a single column, a removed line under its old number and an added
 * line under its new one, marked by a bar on the card's edge as well as by tint.
 *
 * Numbers are shown only where the source gives them. A Claude Code edit carries the
 * old and new text with no hunk range, so it has no gutter rather than a guessed one.
 */

import { useMemo, type ReactNode } from "react";
import { cn } from "../../lib/utils";
import { highlightSync } from "../../lib/code-highlight";
import { languageForPath, splitHastByLine, type DiffRun, type LineToken } from "../../lib/diff-highlight";
import { parseUnifiedDiff, type DiffRow } from "../../lib/diff-parser";
import { useDiffHighlight } from "../../hooks/useDiffHighlight";
import { ChangeCounts, WorkspacePathSegments } from "../ChangedFilesList";
import { ChatFileIcon } from "./ChatFileIcon";
import { CopyButton } from "./CopyButton";
import "./code-theme.css";

/** Where a file sits in the workspace, for the card header. */
export function fileCardPath(path: string, hints: { cwd?: string; summary?: string } = {}): string {
	const normalized = path.replace(/\\/g, "/");
	if (!normalized.startsWith("/") && !/^[A-Za-z]:\//.test(normalized)) return normalized.replace(/^\.\//, "");
	const root = hints.cwd?.replace(/\\/g, "/").replace(/\/$/, "");
	if (root && normalized.startsWith(`${root}/`)) return normalized.slice(root.length + 1);
	// The provider's own title already names the file from the workspace root
	// ("Edit src/lib/money.ts"): the longest tail of the path it ends with.
	// A ranged read adds its lines after the name: "Read README.md (1 - 40)".
	const summary = hints.summary?.trim().replace(/\s+\([^()]*\)$/, "");
	if (summary) {
		const segments = normalized.split("/").filter(Boolean);
		for (let start = 0; start < segments.length; start += 1) {
			const tail = segments.slice(start).join("/");
			if (summary === tail || summary.endsWith(` ${tail}`)) return tail;
		}
	}
	// An Orchestrator worktree: ~/.ao/…/worktrees/<project>/<session>/<path>.
	const worktree = /\/\.ao\/(?:[^/]+\/)*worktrees\/[^/]+\/[^/]+\/(.+)$/.exec(normalized);
	if (worktree) return worktree[1]!;
	return normalized.replace(/^\/(?:Users|home)\/[^/]+/, "~");
}

function FileCard({
	kind,
	path,
	label,
	additions,
	deletions,
	copyText,
	copyLabel,
	children,
	footer,
}: {
	kind: "read" | "edit";
	/** The path the icon is resolved from. */
	path: string;
	/** The path as shown. */
	label: string;
	additions?: number;
	deletions?: number;
	copyText: string | (() => string);
	copyLabel: string;
	children: ReactNode;
	footer?: ReactNode;
}) {
	return (
		<div
			data-chat-file-card={kind}
			className="chat-code mt-1 mb-1 flex min-w-0 flex-col overflow-hidden rounded-lg border border-border bg-surface"
		>
			<div className="flex min-h-9 min-w-0 items-center gap-2 pr-1.5 pl-3 pt-1">
				<ChatFileIcon path={path} />
				<span className="flex min-w-0 flex-1 text-xs" title={label}>
					<WorkspacePathSegments path={label} nameClassName="text-foreground/85" />
				</span>
				{additions !== undefined && deletions !== undefined ? (
					<ChangeCounts additions={additions} deletions={deletions} className="text-caption" />
				) : null}
				<CopyButton compact text={copyText} label={copyLabel} />
			</div>
			<div data-no-swipe-nav="" className="scrollbar-none max-h-80 overflow-auto pt-0.5 pb-2 font-mono text-xs leading-[1.6]">
				<div className="w-max min-w-full">{children}</div>
			</div>
			{footer}
		</div>
	);
}

function gutterWidth(largest: number): string {
	return `${Math.max(2, String(largest).length)}ch`;
}

/* -------------------------------------------------------------------------- */
/* read                                                                       */
/* -------------------------------------------------------------------------- */

export type FileLines = { lines: { no?: number; text: string }[] };

const NUMBERED_LINE = /^\s*(\d+)(?:\t|→)(.*)$/;
const SYSTEM_REMINDER = /<system-reminder>[\s\S]*?<\/system-reminder>/g;

/**
 * The lines of a read, numbered as the tool numbered them (`cat -n`, Claude Code's
 * Read) or counted from `start` when it gives a plain copy. Undefined when the
 * output is not plainly one file's text.
 */
export function readFileLines(output: string, start?: number): FileLines | undefined {
	const text = output.replace(/\r\n?/g, "\n").replace(SYSTEM_REMINDER, "").replace(/\n+$/, "");
	if (!text.trim()) return undefined;
	const raw = text.split("\n");
	const numbered = raw.every((line) => NUMBERED_LINE.test(line) || line.trim() === "")
		&& raw.some((line) => NUMBERED_LINE.test(line));
	if (numbered) {
		return {
			lines: raw.map((line) => {
				const match = NUMBERED_LINE.exec(line);
				return match ? { no: Number(match[1]), text: match[2]! } : { text: "" };
			}),
		};
	}
	return { lines: raw.map((line, index) => ({ no: start === undefined ? undefined : start + index, text: line })) };
}

export function FileReadCard({ path, label, file }: { path: string; label: string; file: FileLines }) {
	const language = useMemo(() => languageForPath(path), [path]);
	const code = useMemo(() => file.lines.map((line) => line.text).join("\n"), [file]);
	const tokens = useMemo<LineToken[][] | undefined>(() => {
		if (!language) return undefined;
		const tree = highlightSync(code, language);
		return tree ? splitHastByLine(tree, file.lines.length) : undefined;
	}, [code, language, file]);
	const largest = file.lines.reduce((max, line) => Math.max(max, line.no ?? 0), 0);
	const numbered = largest > 0;

	return (
		<FileCard kind="read" path={path} label={label} copyText={code} copyLabel="Copy file">
			{file.lines.map((line, index) => (
				<div key={index} className="flex">
					{numbered ? (
						<span
							aria-hidden="true"
							className="shrink-0 select-none pr-4 pl-3 text-right tabular-nums text-passive"
							style={{ minWidth: `calc(${gutterWidth(largest)} + 1.75rem)` }}
						>
							{line.no ?? ""}
						</span>
					) : (
						<span aria-hidden="true" className="w-3 shrink-0" />
					)}
					<span className="whitespace-pre pr-4 text-foreground/90">
						{tokens?.[index] ? renderTokens(tokens[index]!) : line.text || " "}
					</span>
				</div>
			))}
		</FileCard>
	);
}

function renderTokens(tokens: LineToken[]): ReactNode {
	if (tokens.length === 0) return " ";
	return tokens.map((token, index) =>
		token.className ? <span key={index} className={token.className}>{token.text}</span> : token.text,
	);
}

/* -------------------------------------------------------------------------- */
/* edit                                                                       */
/* -------------------------------------------------------------------------- */

const HUNK_RANGE = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

export type ChatDiff = { rows: DiffRow[]; numbered: boolean };

/**
 * A patch as rows for the card. A patch with real hunk ranges keeps its numbers.
 * One without (an editor's old/new text, an `apply_patch` hunk that names a function
 * instead of a range) is parsed as one unnumbered hunk per `@@`, except a new file,
 * whose lines are numbered from 1 because there is nothing else they could be.
 */
export function chatDiff(patch: string, status?: string): ChatDiff {
	const lines = patch.replace(/\r\n?/g, "\n").split("\n");
	let header = 0;
	while (header < lines.length && /^(?:--- |\+\+\+ |diff --git |index )/.test(lines[header]!)) header += 1;
	const body = lines.slice(header);
	const hunks = body.filter((line) => line.startsWith("@@"));
	if (hunks.length > 0 && hunks.every((line) => HUNK_RANGE.test(line))) {
		return { rows: parseUnifiedDiff(body.join("\n")), numbered: true };
	}
	// The new text's own trailing newline is not a line the reader added.
	if (body.length > 1 && body.at(-1) === "+") body.pop();
	const ranged = body.map((line) => (line.startsWith("@@") ? "@@ -1 +1 @@" : line));
	if (!ranged[0]?.startsWith("@@")) ranged.unshift("@@ -1 +1 @@");
	const rows = parseUnifiedDiff(ranged.join("\n"));
	const added = status === "added" && rows.every((row) => row.kind === "add" || row.kind === "hunk");
	if (added && hunks.length === 0) return { rows, numbered: true };
	return { rows: rows.map((row) => ({ ...row, oldNo: null, newNo: null })), numbered: false };
}

export function FileDiffCard({
	path,
	oldPath,
	label,
	patch,
	status,
	additions,
	deletions,
	truncated,
}: {
	path: string;
	oldPath?: string;
	label: string;
	patch: string;
	status?: string;
	additions: number;
	deletions: number;
	truncated?: boolean;
}) {
	const diff = useMemo(() => chatDiff(patch, status), [patch, status]);
	const highlight = useDiffHighlight(diff.rows, path, oldPath);
	const largest = diff.rows.reduce((max, row) => Math.max(max, row.oldNo ?? 0, row.newNo ?? 0), 0);
	const gutter = diff.numbered ? `calc(${gutterWidth(largest)} + 1.75rem)` : undefined;
	const rows: ReactNode[] = [];
	let lastOld: number | undefined;
	let seenHunk = false;
	diff.rows.forEach((row, index) => {
		if (row.kind === "hunk") {
			// The first hunk opens the card; each later one stands for the lines between.
			if (seenHunk) {
				const start = HUNK_RANGE.exec(row.text);
				const skipped = start && lastOld !== undefined ? Number(start[1]) - lastOld - 1 : undefined;
				rows.push(
					<div key={index} className="flex text-passive" data-chat-diff-gap="">
						<span aria-hidden="true" className="shrink-0 select-none pr-4 pl-3 text-right" style={{ minWidth: gutter }}>
							⋯
						</span>
						<span className="whitespace-pre pr-4 font-sans text-caption leading-[1.9]">
							{skipped && skipped > 0 ? `${skipped} unchanged ${skipped === 1 ? "line" : "lines"}` : ""}
						</span>
					</div>,
				);
			}
			seenHunk = true;
			return;
		}
		if (row.oldNo !== null) lastOld = row.oldNo;
		const runs = row.kind === "del" ? highlight.oldSide[index] : highlight.newSide[index];
		const number = row.kind === "del" ? row.oldNo : row.newNo;
		rows.push(
			<div
				key={index}
				data-tool-diff-row=""
				data-kind={row.kind}
				className={cn(
					"relative flex",
					row.kind === "add" && "bg-success/10",
					row.kind === "del" && "bg-error/10",
				)}
			>
				{row.kind !== "context" ? (
					<span aria-hidden="true" className={cn("absolute inset-y-0 left-0 w-0.5", row.kind === "add" ? "bg-success" : "chat-diff-removed-bar")} />
				) : null}
				{diff.numbered ? (
					<span
						aria-hidden="true"
						className={cn(
							"shrink-0 select-none pr-4 pl-3 text-right tabular-nums",
							row.kind === "add" ? "text-success" : row.kind === "del" ? "text-error" : "text-passive",
						)}
						style={{ minWidth: gutter }}
					>
						{number ?? ""}
					</span>
				) : (
					<span aria-hidden="true" className="w-3 shrink-0" />
				)}
				<span className="whitespace-pre pr-4 text-foreground/90">{renderRuns(runs, row.kind === "add")}</span>
			</div>,
		);
	});

	return (
		<FileCard
			kind="edit"
			path={path}
			label={label}
			additions={additions}
			deletions={deletions}
			copyText={patch}
			copyLabel="Copy diff"
			footer={truncated ? (
				<p className="px-3 pb-2 text-xs leading-relaxed text-warning">
					This patch is longer than Orchestrator stores, so it stops early. The whole change is in the
					worktree and in the turn&rsquo;s diff.
				</p>
			) : null}
		>
			{rows}
		</FileCard>
	);
}

function renderRuns(runs: DiffRun[] | undefined, add: boolean): ReactNode {
	if (!runs || runs.length === 0) return " ";
	return runs.map((run, index) =>
		run.changed ? (
			<span key={index} className={cn(run.className, "rounded-sm", add ? "bg-success/25" : "bg-error/25")}>{run.text}</span>
		) : run.className ? (
			<span key={index} className={run.className}>{run.text}</span>
		) : (
			run.text
		),
	);
}
