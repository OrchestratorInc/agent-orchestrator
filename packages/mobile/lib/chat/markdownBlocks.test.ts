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
