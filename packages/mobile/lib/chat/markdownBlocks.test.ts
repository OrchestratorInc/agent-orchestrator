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

	const explained = (second: string) => `1. Install:\n   \`\`\`sh\n   npm i\n   \`\`\`\n   Then run it.\n${second} Start the app`;

	it.each(["2.", "1."])("keeps the step after an explained code block in the list, numbered on (%s)", (second) => {
		expect(parseBlocks(explained(second))).toEqual([
			{ kind: "list", ordered: true, items: [{ text: "Install:", depth: 0, number: 1 }] },
			{ kind: "code", language: "sh", text: "npm i" },
			{ kind: "paragraph", text: "Then run it." },
			{ kind: "list", ordered: true, items: [{ text: "Start the app", depth: 0, number: 2 }] },
		]);
	});

	it("joins a multi-line explanation under a step into one paragraph, and a blank line starts another", () => {
		expect(parseBlocks("1. Build:\n   ```sh\n   make\n   ```\n   This compiles\n   everything.\n\n   It takes a minute.\n2. Ship")).toEqual([
			{ kind: "list", ordered: true, items: [{ text: "Build:", depth: 0, number: 1 }] },
			{ kind: "code", language: "sh", text: "make" },
			{ kind: "paragraph", text: "This compiles everything." },
			{ kind: "paragraph", text: "It takes a minute." },
			{ kind: "list", ordered: true, items: [{ text: "Ship", depth: 0, number: 2 }] },
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

	it("reads a tab-indented item as nested", () => {
		expect(parseBlocks("- a\n\t- b")).toMatchObject([{ items: [{ text: "a", depth: 0 }, { text: "b", depth: 1 }] }]);
	});

	it("joins an item's wrapped continuation line onto it, and keeps a line after a blank on its own line", () => {
		expect(parseBlocks("- item that\n  continues here\n- next")).toMatchObject([{ items: [{ text: "item that continues here" }, { text: "next" }] }]);
		expect(parseBlocks("- first\n\n  second paragraph\n- next")).toMatchObject([{ items: [{ text: "first\nsecond paragraph" }, { text: "next" }] }]);
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

	it("keeps a backslash that ends a block, and drops only one that breaks a line", () => {
		expect(parseBlocks("Path: C:\\")).toEqual([{ kind: "paragraph", text: "Path: C:\\" }]);
		expect(parseBlocks("- Path: C:\\")).toMatchObject([{ items: [{ text: "Path: C:\\" }] }]);
		expect(parseBlocks("one\\\ntwo")).toEqual([{ kind: "paragraph", text: "one\ntwo" }]);
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
});

describe("parseBlocks quotes", () => {
	it("keeps a quoted list one line per item, a blank line after the paragraph above it", () => {
		expect(parseBlocks("> Note:\n> - one\n> - two")).toEqual([{ kind: "quote", text: "Note:\n\n• one\n• two" }]);
	});

	it("keeps the gap between a quote's paragraphs", () => {
		expect(parseBlocks("> one\n>\n> two")).toEqual([{ kind: "quote", text: "one\n\ntwo" }]);
	});
});

describe("parseBlocks fences", () => {
	it("reads tilde fences and keeps only the info string's language", () => {
		expect(parseBlocks("~~~py title=\"a.py\"\nprint('```')\n~~~")).toEqual([{ kind: "code", language: "py", text: "print('```')" }]);
	});

	it("keeps the tabs inside fenced code, so a copied Makefile still runs", () => {
		expect(parseBlocks("```make\nbuild:\n\tgo build ./...\n```")).toEqual([{ kind: "code", language: "make", text: "build:\n\tgo build ./..." }]);
	});

	it("removes only the fence's own indent from code under a list item", () => {
		expect(parseBlocks("1. Run:\n   ```make\n   all:\n   \techo hi\n   ```")[1]).toEqual({ kind: "code", language: "make", text: "all:\n\techo hi" });
	});

	it("closes a top-level fence only on a closer indented at most three spaces", () => {
		expect(parseBlocks("```\ncode\n    ```\nmore\n```")).toEqual([{ kind: "code", text: "code\n    ```\nmore" }]);
		expect(parseBlocks("```\ncode\n   ```\nafter")).toEqual([{ kind: "code", text: "code" }, { kind: "paragraph", text: "after" }]);
	});

	it("reads lines indented four spaces as a code block, keeping their breaks, blank lines and tabs", () => {
		expect(parseBlocks("Install it:\n\n    npm install foo\n    foo --init\n\n      --verbose\n\nDone.")).toEqual([
			{ kind: "paragraph", text: "Install it:" },
			{ kind: "code", text: "npm install foo\nfoo --init\n\n  --verbose" },
			{ kind: "paragraph", text: "Done." },
		]);
		expect(parseBlocks("\tmake all\n\t\techo")).toEqual([{ kind: "code", text: "make all\n\techo" }]);
	});

	it("leaves an indented line that continues a paragraph or belongs to a list item as prose", () => {
		expect(parseBlocks("A paragraph\n    continued")).toEqual([{ kind: "paragraph", text: "A paragraph continued" }]);
		expect(parseBlocks("- item\n\n    more of it")).toMatchObject([{ kind: "list", items: [{ text: "item\nmore of it" }] }]);
	});
});

describe("parseBlocks fence guard", () => {
	it("keeps a guarded fence line inside the block and drops the guard from its text", () => {
		expect(parseBlocks("```md\n​```js\nx\n​```\n```\nafter")).toEqual([
			{ kind: "code", language: "md", text: "```js\nx\n```" },
			{ kind: "paragraph", text: "after" },
		]);
	});

	it("leaves a zero-width space that guards no fence line as written", () => {
		expect(parseBlocks("```\n​hello\n```")).toEqual([{ kind: "code", language: undefined, text: "​hello" }]);
	});
});

describe("repairStreamingInline", () => {
	it("closes an unfinished span in the reply's last paragraph", () => {
		expect(repairStreamingInline("Some **bold")).toBe("Some **bold**");
		expect(repairStreamingInline("run `npm")).toBe("run `npm`");
		expect(repairStreamingInline("**done** and **more")).toBe("**done** and **more**");
		expect(repairStreamingInline("a ~~gone")).toBe("a ~~gone~~");
		expect(repairStreamingInline("A **multi\nline")).toBe("A **multi\nline**");
	});

	it("closes before trailing whitespace", () => {
		expect(repairStreamingInline("Some **bold ")).toBe("Some **bold** ");
	});

	it("leaves finished text, escaped markers, and an open code fence alone", () => {
		expect(repairStreamingInline("**done**")).toBe("**done**");
		expect(repairStreamingInline("2 \\** 3")).toBe("2 \\** 3");
		expect(repairStreamingInline("```js\nconst a = '**'")).toBe("```js\nconst a = '**'");
		expect(repairStreamingInline("`**` is bold")).toBe("`**` is bold");
	});

	it("keeps a longer fence open past a shorter run of its character", () => {
		const text = "````\n```\n**x";
		expect(repairStreamingInline(text)).toBe(text);
		expect(repairStreamingInline("~~~\n```\n~~~\n**x")).toBe("~~~\n```\n~~~\n**x**");
	});

	it("repairs the text after a fence that just closed", () => {
		expect(repairStreamingInline("```js\nx\n```\nNow **bold")).toBe("```js\nx\n```\nNow **bold**");
	});

	it("holds back an opener that has only just arrived instead of doubling it", () => {
		expect(repairStreamingInline("Here is **")).toBe("Here is ");
		expect(repairStreamingInline("Run `")).toBe("Run ");
		expect(repairStreamingInline("Gone ~~")).toBe("Gone ");
	});

	it("counts markers only in the last list item or heading, where the closer lands", () => {
		expect(repairStreamingInline("- 2**10 is big\n- next item")).toBe("- 2**10 is big\n- next item");
		expect(repairStreamingInline("- first\n- **bold")).toBe("- first\n- **bold**");
		expect(repairStreamingInline("## A **heading")).toBe("## A **heading**");
	});

	it("starts the repair after a heading that text follows, and still repairs a final heading", () => {
		expect(repairStreamingInline("# **Title\nBody")).toBe("# **Title\nBody");
		expect(repairStreamingInline("# Title\nBody **bold")).toBe("# Title\nBody **bold**");
		expect(repairStreamingInline("# **Title")).toBe("# **Title**");
	});

	it("closes an open code span first, then the spans around it, the latest opened first", () => {
		expect(repairStreamingInline("**Use `npm")).toBe("**Use `npm`**");
		expect(repairStreamingInline("**a ~~b")).toBe("**a ~~b~~**");
		expect(repairStreamingInline("~~a **b")).toBe("~~a **b**~~");
	});

	it("leaves markers inside an open code span alone, and reads an escaped backtick as a literal", () => {
		expect(repairStreamingInline("Run `x **")).toBe("Run `x **`");
		expect(repairStreamingInline("Use \\` and **bold")).toBe("Use \\` and **bold**");
		expect(repairStreamingInline("*\\`* text")).toBe("*\\`* text");
	});
});

describe("parseBlocks on long text", () => {
	const lines = Array.from({ length: 5_000 }, (_, index) => `2026-11-24 12:00:${index} INFO worker ${index} finished a step`);

	it.each([
		["a paragraph", lines.join("\n")],
		["a list item's continuation lines", `- ${lines.join("\n  ")}`],
		["a quote", lines.map((line) => `> ${line}`).join("\n")],
		["the prose under a step", `1. Run:\n   \`\`\`sh\n   make\n   \`\`\`\n   ${lines.join("\n   ")}`],
	])("parses 5,000 lines of %s in linear time", (_name, text) => {
		const started = performance.now();
		const blocks = parseBlocks(text);
		expect(performance.now() - started).toBeLessThan(250);
		expect(JSON.stringify(blocks)).toContain("worker 4999 finished");
	});
});
