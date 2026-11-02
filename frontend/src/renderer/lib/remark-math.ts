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

import { mathFromMarkdown } from "mdast-util-math";
import { math } from "micromark-extension-llm-math";
import type { Processor } from "unified";

export function remarkMath(this: Processor) {
	const data = this.data();
	(data.micromarkExtensions ??= []).push(math());
	(data.fromMarkdownExtensions ??= []).push(mathFromMarkdown());
}

/** Whether a `code` element's class marks it as an inline formula from `remarkMath`. */
export function isInlineMath(className: string | undefined): boolean {
	return className?.includes("math-inline") ?? false;
}
