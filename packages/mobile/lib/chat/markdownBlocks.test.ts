import { describe, expect, it } from "vitest";
import { parseBlocks, repairStreamingInline } from "./markdownBlocks";

describe("parseBlocks images", () => {
	const text = "![chart](https://example.com/c.png)\n\n![shot](shots/a.png)";

	it("reads only web images in a chat reply", () => {
		expect(parseBlocks(text).map((block) => block.kind)).toEqual(["image", "paragraph"]);
	});

	it("reads a document's relative images when asked", () => {
		expect(parseBlocks(text, { relativeImages: true })).toEqual([
			{ kind: "image", alt: "chart", url: "https://example.com/c.png" },
			{ kind: "image", alt: "shot", url: "shots/a.png" },
		]);
	});
});

describe("parseBlocks lists", () => {
	it("keeps counting an agent's numbered steps across a code fence indented under a step", () => {
		expect(parseBlocks("1. Install deps:\n   ```bash\n   npm ci\n   ```\n2. Run tests\n3. Ship")).toEqual([
			{ kind: "list", ordered: true, items: [{ text: "Install deps:", depth: 0, number: 1 }] },
			{ kind: "code", language: "bash", text: "npm ci" },
			{ kind: "list", ordered: true, items: [{ text: "Run tests", depth: 0, number: 2 }, { text: "Ship", depth: 0, number: 3 }] },
		]);
	});

	it("keeps a loose list (blank lines between items) as one list", () => {
		expect(parseBlocks("1. First\n\n2. Second\n\n3. Third")).toEqual([
			{ kind: "list", ordered: true, items: [{ text: "First", depth: 0, number: 1 }, { text: "Second", depth: 0, number: 2 }, { text: "Third", depth: 0, number: 3 }] },
		]);
	});

	it("numbers from the list's first number, as CommonMark does", () => {
		expect(parseBlocks("6. sixth\n7. seventh")).toMatchObject([{ items: [{ number: 6 }, { number: 7 }] }]);
		expect(parseBlocks("1. a\n1. b\n1. c")).toMatchObject([{ items: [{ number: 1 }, { number: 2 }, { number: 3 }] }]);
	});

	it("nests indented items, with an ordered list inside a bulleted one", () => {
		expect(parseBlocks("- Parent\n  - Child a\n  - Child b\n    1. deep\n- Parent 2")).toEqual([{ kind: "list", ordered: false, items: [
			{ text: "Parent", depth: 0 },
			{ text: "Child a", depth: 1 },
			{ text: "Child b", depth: 1 },
			{ text: "deep", depth: 2, number: 1 },
			{ text: "Parent 2", depth: 0 },
		] }]);
	});

	it("joins an item's wrapped continuation line onto it", () => {
		expect(parseBlocks("- item that\n  continues here\n- next")).toMatchObject([{ items: [{ text: "item that continues here" }, { text: "next" }] }]);
	});

	it("starts a new list when a top-level item switches between bullets and numbers", () => {
		expect(parseBlocks("- a\n- b\n1. one\n2. two").map((block) => block.kind === "list" && block.ordered)).toEqual([false, true]);
	});

	it("does not read a numbered line inside a paragraph as a list unless it starts at 1", () => {
		expect(parseBlocks("The year was\n1999. A good one.")).toEqual([{ kind: "paragraph", text: "The year was 1999. A good one." }]);
	});
});

describe("parseBlocks prose", () => {
	it("reads a plain line break as a space and two trailing spaces as a break, as the desktop does", () => {
		expect(parseBlocks("This line wraps\nonto the next.  \nHard break here.")).toEqual([
			{ kind: "paragraph", text: "This line wraps onto the next.\nHard break here." },
		]);
	});

	it("reads setext headings, closing hashes, and every thematic break", () => {
		expect(parseBlocks("Title\n=====\n\nSub\n---\n\n## Heading ##\n\n### C#\n\n***\n\n___\n\n- - -")).toEqual([
			{ kind: "heading", level: 1, text: "Title" },
			{ kind: "heading", level: 2, text: "Sub" },
			{ kind: "heading", level: 2, text: "Heading" },
			{ kind: "heading", level: 3, text: "C#" },
			{ kind: "rule" }, { kind: "rule" }, { kind: "rule" },
		]);
	});

	it("reads tilde fences and keeps only the info string's language", () => {
		expect(parseBlocks("~~~py title=\"a.py\"\nprint('```')\n~~~")).toEqual([{ kind: "code", language: "py", text: "print('```')" }]);
	});

	it("keeps a quoted list one line per item", () => {
		expect(parseBlocks("> Note:\n> - one\n> - two")).toEqual([{ kind: "quote", text: "Note:\n• one\n• two" }]);
	});
});

describe("repairStreamingInline", () => {
	it("closes an unfinished span in the reply's last paragraph", () => {
		expect(repairStreamingInline("Some **bold")).toBe("Some **bold**");
		expect(repairStreamingInline("run `npm")).toBe("run `npm`");
		expect(repairStreamingInline("**done** and **more")).toBe("**done** and **more**");
		expect(repairStreamingInline("a ~~gone")).toBe("a ~~gone~~");
	});

	it("leaves finished text, escaped markers, and an open code fence alone", () => {
		expect(repairStreamingInline("**done**")).toBe("**done**");
		expect(repairStreamingInline("2 \\** 3")).toBe("2 \\** 3");
		expect(repairStreamingInline("```js\nconst a = '**'")).toBe("```js\nconst a = '**'");
		expect(repairStreamingInline("`**` is bold")).toBe("`**` is bold");
	});
});
