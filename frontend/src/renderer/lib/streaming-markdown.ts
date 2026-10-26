import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import type { Nodes } from "mdast";

// Use the renderer's parser: a second markdown dialect can disagree about
// where lists, HTML, tables and indented code end.
const parser = unified().use(remarkParse).use(remarkGfm);

type MarkdownBlock = { source: string; start: number; repairInline: boolean };
export type MarkdownBlocks = {
	source: string;
	blocks: MarkdownBlock[];
	stableCount: number;
};

function containsCode(node: Nodes): boolean {
	return node.type === "code" || ("children" in node && node.children.some(containsCode));
}

/** Per-message, immutable state; concurrent replies never evict each other. */
export function splitStreamingMarkdown(source: string, previous?: MarkdownBlocks): MarkdownBlocks {
	if (previous?.source === source) return previous;
	// Definitions and footnotes have document-wide scope. Keep one document so
	// later definitions resolve earlier links and footnote IDs stay unique.
	if (/\]\s*:|\[\^/.test(source)) {
		return { source, blocks: [{ source, start: 0, repairInline: false }], stableCount: 0 };
	}
	const keep = previous && source.startsWith(previous.source) ? previous.stableCount : 0;
	const blocks = previous?.blocks.slice(0, keep) ?? [];
	const offset = previous?.blocks[keep]?.start ?? 0;
	const tail = source.slice(offset);
	const tree = parser.parse(tail);
	const starts = tree.children.map((node) => {
		const position = node.position?.start;
		// Include indentation in the next block, rather than leaving it on the
		// previous one. Positions and fade offsets remain source-exact (also CRLF).
		return position ? (position.offset ?? 0) - position.column + 1 : 0;
	});
	for (let i = 0; i < starts.length; i++) {
		const start = i === 0 ? 0 : starts[i];
		blocks.push({
			source: tail.slice(start, starts[i + 1]),
			start: offset + start,
			// Repair prose only. Code inside lists/quotes is just as literal as a
			// top-level fence; remend's own guards do not cover indented code.
			repairInline: !containsCode(tree.children[i]),
		});
	}
	if (blocks.length === 0) blocks.push({ source, start: 0, repairInline: false });
	// A partial '#' can become '#text' and rejoin the paragraph above; '2'
	// can become '2.' and extend a list. Retain two blocks, and only commit at
	// a blank line. A parsed boundary alone is not proof of stability.
	let stableCount = keep;
	for (let i = blocks.length - 3; i >= keep; i--) {
		if (/(?:\r?\n)[\t ]*(?:\r?\n)[\s]*$/.test(blocks[i].source)) {
			stableCount = i + 1;
			break;
		}
	}
	return { source, blocks, stableCount };
}
