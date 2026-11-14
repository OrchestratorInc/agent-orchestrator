/// <reference types="remark-parse" />

import { mathFromMarkdown } from "mdast-util-math";
import { math } from "micromark-extension-llm-math";
import type { Processor } from "unified";

export function remarkMath(this: Processor) {
	const data = this.data();
	(data.micromarkExtensions ??= []).push(math());
	(data.fromMarkdownExtensions ??= []).push(mathFromMarkdown());
}

export function isInlineMath(className: string | undefined): boolean {
	return className?.includes("math-inline") ?? false;
}
