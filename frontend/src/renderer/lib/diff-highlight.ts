import type { Root, RootContent } from "hast";
import { canonicalLanguage, type GrammarName } from "./code-highlight";
import type { DiffSegment } from "./diff-parser";

export type LineToken = { text: string; className?: string };

export type DiffRun = { text: string; className?: string; changed: boolean };

export function languageForPath(path: string): GrammarName | undefined {
	const base = path.slice(path.lastIndexOf("/") + 1);
	const dot = base.lastIndexOf(".");
	if (dot === -1) return undefined;
	return canonicalLanguage(base.slice(dot + 1));
}

export function splitHastByLine(root: Root, lineCount: number): LineToken[][] {
	const lines: LineToken[][] = Array.from({ length: lineCount }, () => []);
	let lineIndex = 0;

	function pushRun(text: string, className: string | undefined): void {
		if (text === "") return;
		const line = lines[lineIndex];
		if (!line) return;
		const last = line[line.length - 1];
		if (last && last.className === className) last.text += text;
		else line.push({ text, className });
	}

	function walk(nodes: readonly RootContent[], classNames: readonly string[]): void {
		for (const node of nodes) {
			if (node.type === "text") {
				const parts = node.value.split("\n");
				const className = classNames.length ? classNames.join(" ") : undefined;
				parts.forEach((part, i) => {
					if (i > 0) lineIndex += 1;
					pushRun(part, className);
				});
			} else if (node.type === "element") {
				const raw = node.properties?.className;
				const names = Array.isArray(raw) ? raw.map(String) : [];
				walk(node.children, names.length ? [...classNames, ...names] : classNames);
			}
		}
	}

	walk(root.children, []);
	return lines;
}

type TextRun = { text: string; className?: string };

function mergeRuns(tokenRuns: TextRun[], segRuns: DiffSegment[]): DiffRun[] {
	const result: DiffRun[] = [];
	let ti = 0;
	let si = 0;
	let tOffset = 0;
	let sOffset = 0;
	while (ti < tokenRuns.length && si < segRuns.length) {
		const t = tokenRuns[ti];
		const s = segRuns[si];
		const take = Math.min(t.text.length - tOffset, s.text.length - sOffset);
		const text = t.text.slice(tOffset, tOffset + take);
		pushMerged(result, text, t.className, s.changed);
		tOffset += take;
		sOffset += take;
		if (tOffset >= t.text.length) {
			ti += 1;
			tOffset = 0;
		}
		if (sOffset >= s.text.length) {
			si += 1;
			sOffset = 0;
		}
	}
	return result;
}

function pushMerged(result: DiffRun[], text: string, className: string | undefined, changed: boolean): void {
	if (text === "") return;
	const last = result[result.length - 1];
	if (last && last.className === className && last.changed === changed) last.text += text;
	else result.push({ text, className, changed });
}

export function composeLineRuns(
	tokens: LineToken[] | undefined,
	segments: DiffSegment[] | undefined,
	text: string,
): DiffRun[] {
	if (text === "") return [{ text: " ", changed: false }];
	const tokenRuns: TextRun[] = tokens && tokens.length > 0 ? tokens : [{ text, className: undefined }];
	const segRuns: DiffSegment[] = segments && segments.length > 0 ? segments : [{ text, changed: false }];
	return mergeRuns(tokenRuns, segRuns);
}
