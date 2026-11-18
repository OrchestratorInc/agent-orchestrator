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

	it("matches one-shot highlighting across multiline comments and template strings", () => {
		const source = '/* open\ncontinued */\nconst value = `first\nsecond ${true}`;\n';
		const line = engine.createLines("javascript");
		const children = source.split("\n").slice(0, -1).flatMap((text) => [...line(text, true), { type: "text" as const, value: "\n" }]);
		expect(flatten({ type: "root", children })).toBe(source);
		expect(children.filter((node) => node.type === "element")).toEqual(engine.highlight("javascript", source).children.filter((node) => node.type === "element"));
	});

	it.each(["\n", "\r\n", "\r"])("preserves exact source line endings: %j", (ending) => {
		const source = `/* 👋${ending}continued */${ending}`;
		expect(flatten(engine.highlight("javascript", source))).toBe(source);
	});

	it("preserves mixed source line endings", () => {
		const source = "/*\r\nmixed\rend */\nconst value = 1;\r";
		expect(flatten(engine.highlight("javascript", source))).toBe(source);
	});

	it("keeps independent line states separate, and only commits on request", () => {
		const comment = engine.createLines("javascript");
		const normal = engine.createLines("javascript");
		comment("/* comment", true);
		const inComment = comment("const value = 1;", false);
		const outsideComment = normal("const value = 1;", true);
		expect(inComment).not.toEqual(outsideComment);
		expect(comment("const value = 1;", false)).toEqual(inComment);
		expect(flatten({ type: "root", children: inComment })).toBe("const value = 1;");
	});
});
