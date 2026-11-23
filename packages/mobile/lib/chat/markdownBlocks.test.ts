import { describe, expect, it } from "vitest";
import { parseBlocks } from "./markdownBlocks";

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
