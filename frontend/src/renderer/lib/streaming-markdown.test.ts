import { describe, expect, it } from "vitest";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import { remarkMath } from "./remark-math";
import { splitStreamingMarkdown, type MarkdownBlocks } from "./streaming-markdown";

const parser = unified().use(remarkParse).use(remarkGfm);
const mathParser = unified().use(remarkParse).use(remarkGfm).use(remarkMath);
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

// A display formula's lines can look like a list item or end at a blank line.
const mathExamples = [
	"A\n\nB\n\n$$\n(a-b)^2 = a^2\n- 2ab + b^2\n\n# not a heading\n$$\n\nlast",
	"A\n\nB\n\n\\[\nx\n\n- y\n\\]\n\nlast",
	"A\n\nB\n\nInline $a_i *b*$ and \\(x_1\\) here.\n\nlast",
];

function expectSplitMatchesParse(source: string, parse: typeof parser, math: boolean) {
	let previous: MarkdownBlocks | undefined;
	for (let length = 0; length <= source.length; length++) {
		const text = source.slice(0, length);
		const result = splitStreamingMarkdown(text, previous, math);
		expect(result.blocks.map((block) => block.source).join("")).toBe(text);
		for (const block of result.blocks) expect(text.slice(block.start, block.start + block.source.length)).toBe(block.source);
		const splitTree = result.blocks.flatMap((block) => parse.parse(block.source).children);
		expect(withoutPositions(splitTree), `prefix ${JSON.stringify(text)}`).toEqual(withoutPositions(parse.parse(text).children));
		previous = result;
	}
}

describe("splitStreamingMarkdown", () => {
	it.each(examples)("matches a full GFM parse at every character: %s", (source) => {
		expectSplitMatchesParse(source, parser, false);
	});

	it.each(mathExamples)("matches a full math parse at every character: %s", (source) => {
		expectSplitMatchesParse(source, mathParser, true);
	});

	it("leaves formulas out of prose repair and re-cuts when the dialect changes", () => {
		const text = "A\n\n$$\nx_1 * y\n$$\n\nInline $a_i$\n\nprose *open";
		const math = splitStreamingMarkdown(text, undefined, true);
		expect(math.blocks.map((block) => block.repairInline)).toEqual([true, false, false, true]);
		expect(splitStreamingMarkdown(text, math, false)).toEqual(splitStreamingMarkdown(text));
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
