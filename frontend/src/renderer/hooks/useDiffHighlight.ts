import { useMemo, useReducer } from "react";
import { highlight, highlightSync } from "../lib/code-highlight";
import { composeLineRuns, languageForPath, splitHastByLine, type DiffRun } from "../lib/diff-highlight";
import type { GrammarName } from "../lib/code-highlight";
import type { DiffRow } from "../lib/diff-parser";

type HunkLine = { text: string; rowIndex: number };
type Hunk = { old: HunkLine[]; new: HunkLine[] };

function groupHunks(rows: DiffRow[]): Hunk[] {
	const hunks: Hunk[] = [];
	let current: Hunk | null = null;
	rows.forEach((row, rowIndex) => {
		if (row.kind === "hunk") {
			current = { old: [], new: [] };
			hunks.push(current);
			return;
		}
		if (!current) return;
		if (row.kind === "context") {
			current.old.push({ text: row.text, rowIndex });
			current.new.push({ text: row.text, rowIndex });
		} else if (row.kind === "del") {
			current.old.push({ text: row.text, rowIndex });
		} else if (row.kind === "add") {
			current.new.push({ text: row.text, rowIndex });
		}
	});
	return hunks;
}

function highlightSide(
	hunks: Hunk[],
	rows: DiffRow[],
	pickSide: (hunk: Hunk) => HunkLine[],
	lang: GrammarName | undefined,
	reread: () => void,
): DiffRun[][] {
	const runs: DiffRun[][] = rows.map((row) => composeLineRuns(undefined, row.segments, row.text));
	if (!lang) return runs;

	for (const hunk of hunks) {
		const side = pickSide(hunk);
		if (side.length === 0) continue;
		const blob = side.map((line) => line.text).join("\n");
		const tree = highlightSync(blob, lang);
		if (!tree) {
			void highlight(blob, lang).then((resolved) => {
				if (resolved) reread();
			});
			continue;
		}
		const perLine = splitHastByLine(tree, side.length);
		side.forEach((line, i) => {
			const row = rows[line.rowIndex];
			runs[line.rowIndex] = composeLineRuns(perLine[i], row.segments, row.text);
		});
	}
	return runs;
}

export type DiffHighlight = {
	oldSide: DiffRun[][];
	newSide: DiffRun[][];
};

export function useDiffHighlight(rows: DiffRow[], path: string, previousPath: string | undefined): DiffHighlight {
	const oldLang = useMemo(() => languageForPath(previousPath ?? path), [previousPath, path]);
	const newLang = useMemo(() => languageForPath(path), [path]);
	const [rereadCount, reread] = useReducer((count: number) => count + 1, 0);

	return useMemo(() => {
		const hunks = groupHunks(rows);
		return {
			oldSide: highlightSide(hunks, rows, (hunk) => hunk.old, oldLang, reread),
			newSide: highlightSide(hunks, rows, (hunk) => hunk.new, newLang, reread),
		};
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [rows, oldLang, newLang, rereadCount]);
}
