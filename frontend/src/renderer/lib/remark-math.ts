/**
 * Math delimiters in agent markdown: `$…$`, `$$…$$`, and the `\(…\)` / `\[…\]`
 * pair that models trained on LaTeX write instead.
 *
 * The tokenizer is a fork of the one `remark-math` uses, adding the backslash
 * delimiters; CommonMark would otherwise read `\(` as an escaped paren and show
 * `(x^2)`. Inline formulas become `code.language-math.math-inline` and display
 * formulas `pre > code.language-math.math-display`, so the renderers pick them
 * up in the same overrides that already claim code.
 *
 * Opt-in, because a single `$` is also currency and shell syntax: "$5 and $10"
 * parses as the formula "5 and ". ```math fences need no plugin and no opt-in;
 * they are ordinary fences with an unambiguous language.
 */

// Registers the `micromarkExtensions` / `fromMarkdownExtensions` fields on
// unified's `Data`, as remark-math's own types do.
/// <reference types="remark-parse" />

import type { Nodes } from "mdast";
import { mathFromMarkdown } from "mdast-util-math";
import { math } from "micromark-extension-llm-math";
import remarkParse from "remark-parse";
import { unified, type Processor } from "unified";

export function remarkMath(this: Processor) {
	const data = this.data();
	(data.micromarkExtensions ??= []).push(math());
	(data.fromMarkdownExtensions ??= []).push(mathFromMarkdown());
}

/** Whether a `code` element's class marks it as an inline formula from `remarkMath`. */
export function isInlineMath(className: string | undefined): boolean {
	return className?.includes("math-inline") ?? false;
}

const mathParser = unified().use(remarkParse).use(remarkMath);

type Edit = { start: number; end: number; text: string };

function backslashFormulas(node: Nodes, source: string, edits: Edit[]): void {
	if (node.type === "math" || node.type === "inlineMath") {
		const start = node.position!.start.offset!;
		const end = node.position!.end.offset!;
		const raw = source.slice(start, end);
		if (raw[0] !== "\\") return;
		const closed = raw.endsWith(raw[1] === "(" ? "\\)" : "\\]");
		const indent = source.slice(source.lastIndexOf("\n", start - 1) + 1, start).replace(/[^\s>]/g, " ");
		const text = node.type !== "math"
			? `${raw[1] === "[" ? "$$" : "$"}${raw.slice(2, -2).trim()}${raw[1] === "[" ? "$$" : "$"}`
			: closed && !raw.includes("\n")
				? `$$\n${indent}${raw.slice(2, -2).trim()}\n${indent}$$`
				: `$$${raw.slice(2, closed ? -2 : undefined)}${closed ? "$$" : ""}`;
		edits.push({ start, end, text });
	} else if ("children" in node) for (const child of node.children) backslashFormulas(child, source, edits);
}

export function createDollarDelimiters(): (text: string) => string {
	let source = "";
	let stable = 0;
	let done = "";
	return (text) => {
		if (!text.includes("\\(") && !text.includes("\\[")) return text;
		const from = source && text.startsWith(source.slice(0, stable)) ? stable : 0;
		const tail = text.slice(from);
		const tree = mathParser.parse(tail);
		const edits: Edit[] = [];
		backslashFormulas(tree, tail, edits);
		const apply = (start: number, end: number) => {
			let out = "";
			let at = start;
			for (const edit of edits) if (edit.start >= start && edit.end <= end) {
				out += tail.slice(at, edit.start) + edit.text;
				at = edit.end;
			}
			return out + tail.slice(at, end);
		};
		const last = tree.children.at(-1)?.position?.start.offset ?? 0;
		done = (from ? done : "") + apply(0, last);
		stable = from + last;
		source = text;
		return done + apply(last, tail.length);
	};
}
