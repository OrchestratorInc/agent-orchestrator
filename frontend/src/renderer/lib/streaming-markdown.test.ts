import { describe, expect, it } from "vitest";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import { splitStreamingMarkdown, type MarkdownBlocks } from "./streaming-markdown";

const parser = unified().use(remarkParse).use(remarkGfm);
const withoutPositions = (value: unknown) => JSON.parse(JSON.stringify(value, (key, item: unknown) => key === "position" ? undefined : item));

const examples = [
	"# Plan\n\nOne **bold** paragraph.\n\n## Next\n\nAnother paragraph.",
	"First\n\nSecond\n\nthird\n#title\n\nlast",
	"A\n\nB\n\n1. first\n\n2. second\n\n    continued\n\n3. last",
	"A\n\nB\n\n- list\n\n  paragraph\n\n      code\n\n- tail",
	"A\n\nB\n\n> quote\n>\n> - nested\n\nlast",
	"A\n\nB\n\nTitle\n---\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\nlast",
	"A\n\nB\n\n```ts\nconst x = `a\n\nb`;\n```\n\nlast",
	"A\n\nB\n\n~~~\n**literal\n\n~~~\n\nlast",
	"A\n\nB\n\n<div>\n\ntext\n\n</div>\n\nlast",
	"A\n\nB\n\n    indented\n\n    code\n\nlast",
	"  A\r\n\r\n  B\r\n\r\n  C\r\n\r\n  D",
	"😀 A\n\n**B**\n\n[docs][ref]\n\n[ref]: https://example.com",
	"[^first]\n\nOther[^second]\n\n[^first]: one\n\n[^second]: two",
];

describe("splitStreamingMarkdown", () => {
	it.each(examples)("matches a full GFM parse at every character: %s", (source) => {
		let previous: MarkdownBlocks | undefined;
		for (let length = 0; length <= source.length; length++) {
			const text = source.slice(0, length);
			const result = splitStreamingMarkdown(text, previous);
			expect(result.blocks.map((block) => block.source).join("")).toBe(text);
			for (const block of result.blocks) expect(text.slice(block.start, block.start + block.source.length)).toBe(block.source);
			const splitTree = result.blocks.flatMap((block) => parser.parse(block.source).children);
			expect(withoutPositions(splitTree), `prefix ${JSON.stringify(text)}`).toEqual(withoutPositions(parser.parse(text).children));
			previous = result;
		}
	});

	it("reuses stable blocks across interleaved messages and resets for edits/truncation", () => {
		const first = splitStreamingMarkdown("One\n\nTwo\n\nThree\n\nFour");
		splitStreamingMarkdown("Unrelated\n\nreply");
		const next = splitStreamingMarkdown(first.source + " five", first);
		expect(next.blocks[0]).toBe(first.blocks[0]);
		expect(next.blocks[1]).toBe(first.blocks[1]);
		for (const text of ["Replaced\n\nreply", "One", ""]) {
			expect(splitStreamingMarkdown(text, next)).toEqual(splitStreamingMarkdown(text));
		}
	});
});
