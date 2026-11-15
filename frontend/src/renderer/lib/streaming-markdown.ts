import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import type { Nodes } from "mdast";
import { remarkMath } from "./remark-math";

const parser = unified().use(remarkParse).use(remarkGfm);
const mathParser = unified().use(remarkParse).use(remarkGfm).use(remarkMath);

type MarkdownBlock = { source: string; start: number; repairInline: boolean };
export type MarkdownBlocks = {
	source: string;
	math: boolean;
	blocks: MarkdownBlock[];
	stableCount: number;
};

function containsDefinition(node: Nodes): boolean {
	return node.type === "definition" || node.type === "footnoteDefinition"
		|| ("children" in node && node.children.some(containsDefinition));
}

function containsCode(node: Nodes): boolean {
	return node.type === "code" || node.type === "math" || node.type === "inlineMath"
		|| ("children" in node && node.children.some(containsCode));
}

export function splitStreamingMarkdown(source: string, previous?: MarkdownBlocks, math = false): MarkdownBlocks {
	if (previous?.source === source && previous.math === math) return previous;
	const keep = previous?.math === math && source.startsWith(previous.source) ? previous.stableCount : 0;
	const blocks = previous?.blocks.slice(0, keep) ?? [];
	const offset = previous?.blocks[keep]?.start ?? 0;
	const tail = source.slice(offset);
	const tree = (math ? mathParser : parser).parse(tail);
	if (tree.children.some(containsDefinition)) {
		return { source, math, blocks: [{ source, start: 0, repairInline: false }], stableCount: 0 };
	}
	const starts = tree.children.map((node) => {
		const position = node.position?.start;
		return position ? (position.offset ?? 0) - position.column + 1 : 0;
	});
	for (let i = 0; i < starts.length; i++) {
		const start = i === 0 ? 0 : starts[i];
		blocks.push({
			source: tail.slice(start, starts[i + 1]),
			start: offset + start,
			repairInline: !containsCode(tree.children[i]),
		});
	}
	if (blocks.length === 0) blocks.push({ source, start: 0, repairInline: false });
	let stableCount = keep;
	for (let i = blocks.length - 3; i >= keep; i--) {
		if (/(?:\r?\n)[\t ]*(?:\r?\n)[\s]*$/.test(blocks[i].source)) {
			stableCount = i + 1;
			break;
		}
	}
	return { source, math, blocks, stableCount };
}
