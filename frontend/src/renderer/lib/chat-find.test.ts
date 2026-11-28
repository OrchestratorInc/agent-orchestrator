import { describe, expect, it } from "vitest";
import fixtures from "../../../../test/chat-find.json";
import { createHtmlRenderer } from "@pierre/words/html";
import { findBlocks, findTextOccurrences, renderedFindBlocks, findRange } from "./chat-find";

describe("chat find shared contract", () => {
	for (const fixture of fixtures) it(fixture.name, () => {
		const blocks = findBlocks(fixture.source, fixture.markdown);
		expect(blocks).toEqual(fixture.blocks);
		expect(blocks.flatMap((block, index) => findTextOccurrences(block, fixture.query).map((range) => ({ block: index, ...range })))).toEqual(fixture.ranges);
	});

	it("maps a match across formatted spans without including controls or hidden content", () => {
		const root = document.createElement("div");
		root.innerHTML = '<p>hello <strong>world</strong><button>Copy</button><span aria-hidden="true">secret</span></p><pre><code>a<span>.*</span></code><button>Copy</button></pre>';
		const blocks = renderedFindBlocks(root);
		expect(blocks.map((block) => block.text)).toEqual(["hello world", "a.*"]);
		const [hit] = findTextOccurrences(blocks[0].text, "HELLO WORLD");
		expect(findRange(blocks[0].nodes, hit.start, hit.end)?.toString()).toBe("hello world");
	});

	it("does not count the whitespace of a loose list item as a block", () => {
		const root = document.createElement("div");
		root.innerHTML = "<ol>\n<li>\n<p>first</p>\n</li>\n<li>\n<p>second</p>\n</li>\n</ol>";
		expect(renderedFindBlocks(root).map((block) => block.text)).toEqual(["first", "second"]);
	});

	it("keeps nested list prose and hard line breaks in their own blocks", () => {
		const root = document.createElement("div");
		root.innerHTML = "<ul><li>parent<ul><li>child</li></ul></li></ul><p>hello<br>world</p>";
		const blocks = renderedFindBlocks(root);
		expect(blocks.map((block) => block.text)).toEqual(["parent", "child", "hello\nworld"]);
		const [hit] = findTextOccurrences(blocks[2].text, "hello world");
		expect(findRange(blocks[2].nodes, hit.start, hit.end)?.toString()).toBe("helloworld");
	});

	it("indexes a streaming reply's blocks like its source, skipping diagram and math fences", () => {
		const source = "Intro\n\n```mermaid\ngraph TD\n```\n\n```Math\nx^2\n```\n\n```ts\nconst a = 1\n```\n\nOutro";
		const root = document.createElement("div");
		const renderer = createHtmlRenderer();
		root.innerHTML = renderer.render(source, true, true).html;
		renderer.dispose();
		expect(renderedFindBlocks(root).map((block) => block.text)).toEqual(findBlocks(source, true));
	});
});
