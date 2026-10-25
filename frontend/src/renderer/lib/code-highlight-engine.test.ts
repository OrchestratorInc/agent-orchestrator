import { describe, expect, it } from "vitest";
import type { Root, RootContent } from "hast";
import { createEngine } from "./code-highlight-engine";

const engine = await createEngine();
function flatten(node: Root | RootContent): string {
	if (node.type === "text") return node.value;
	return "children" in node ? node.children.map(flatten).join("") : "";
}

describe("Shiki streaming grammar state", () => {
	it("matches one-shot highlighting across multiline comments and template strings", async () => {
		const chunks = ['/* open\n', 'continued */\nconst value = `first\n', 'second ${true}`;\n'];
		const append = engine.createStream("javascript");
		const lines = [];
		for (const chunk of chunks) lines.push(...await append(chunk));
		const source = chunks.join("");
		expect({ type: "root", children: lines.flatMap((line) => line.tokens) }).toEqual(engine.highlight("javascript", source));
	});

	it.each(["\n", "\r\n", "\r"])("preserves exact source line endings: %j", async (ending) => {
		const source = `/* 👋${ending}continued */${ending}`;
		expect(flatten(engine.highlight("javascript", source))).toBe(source);
		const lines = await engine.createStream("javascript")(source);
		expect(flatten({ type: "root", children: lines.flatMap((line) => line.tokens) })).toBe(source);
	});

	it("preserves mixed source line endings when highlighting and streaming", async () => {
		const source = "/*\r\nmixed\rend */\nconst value = 1;\r";
		expect(flatten(engine.highlight("javascript", source))).toBe(source);
		const lines = await engine.createStream("javascript")(source);
		expect(flatten({ type: "root", children: lines.flatMap((line) => line.tokens) })).toBe(source);
	});

	it("keeps independently streamed grammar states separate", async () => {
		const comment = engine.createStream("javascript");
		const normal = engine.createStream("javascript");
		await comment("/* comment\n");
		const inComment = await comment("const value = 1;\n");
		const outsideComment = await normal("const value = 1;\n");
		expect(inComment[0].signature).not.toBe(outsideComment[0].signature);
		expect(flatten({ type: "root", children: inComment[0].tokens })).toBe("const value = 1;\n");
	});
});
