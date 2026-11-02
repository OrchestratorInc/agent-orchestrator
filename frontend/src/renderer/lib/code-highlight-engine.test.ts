import { describe, expect, it } from "vitest";
import type { Root, RootContent } from "hast";
import { createEngine } from "./code-highlight-engine";
import type { GrammarName } from "./code-highlight";

const engine = await createEngine();
function flatten(node: Root | RootContent): string {
	if (node.type === "text") return node.value;
	return "children" in node ? node.children.map(flatten).join("") : "";
}

function textWithClass(node: Root | RootContent, className: string): string {
	if (node.type === "element" && Array.isArray(node.properties.className) && node.properties.className.includes(className)) return flatten(node);
	return "children" in node ? node.children.map((child) => textWithClass(child, className)).join("") : "";
}

describe("Shiki streaming grammar state", () => {
	it.each([
		["json", '{"name":"value"}', "name"],
		["css", ".x { color: red; }", "color"],
	] satisfies [GrammarName, string, string][])("keeps %s property names in AO's red palette", (language, source, property) => {
		const tree = engine.highlight(language, source);
		expect(textWithClass(tree, "hljs-variable")).toContain(property);
		expect(textWithClass(tree, "hljs-type")).not.toContain(property);
		expect(flatten(tree)).toBe(source);
	});

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
