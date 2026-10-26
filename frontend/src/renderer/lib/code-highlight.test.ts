import { describe, expect, it } from "vitest";
import type { Element, Root, RootContent } from "hast";
import { canonicalLanguage, highlight, highlightSync, type GrammarName } from "./code-highlight";

// The contract these pin down is not "the colours are right" — Shiki owns
// that — but the four things the chat surface depends on: a fence label resolves
// to a grammar or to nothing, tokenizing produces classed spans, the same block is
// never parsed twice, and nothing here can throw into a message.

function classesIn(tree: Root): string[] {
	const found: string[] = [];
	const walk = (nodes: readonly RootContent[]): void => {
		for (const node of nodes) {
			if (node.type !== "element") continue;
			const className = (node as Element).properties.className;
			if (Array.isArray(className)) found.push(...className.map(String));
			walk((node as Element).children);
		}
	};
	walk(tree.children);
	return found;
}

function textIn(tree: Root): string {
	let text = "";
	const walk = (nodes: readonly RootContent[]): void => {
		for (const node of nodes) {
			if (node.type === "text") text += node.value;
			else if (node.type === "element") walk(node.children);
		}
	};
	walk(tree.children);
	return text;
}

describe("canonicalLanguage", () => {
	it("resolves the aliases agents actually write", () => {
		expect(canonicalLanguage("ts")).toBe("typescript");
		expect(canonicalLanguage("tsx")).toBe("tsx");
		expect(canonicalLanguage("sh")).toBe("bash");
		expect(canonicalLanguage("zsh")).toBe("bash");
		expect(canonicalLanguage("console")).toBe("bash");
		expect(canonicalLanguage("golang")).toBe("go");
		expect(canonicalLanguage("yml")).toBe("yaml");
		expect(canonicalLanguage("html")).toBe("html");
		expect(canonicalLanguage("patch")).toBe("diff");
	});

	it("is case-insensitive, because a fence label is free text", () => {
		expect(canonicalLanguage("Go")).toBe("go");
		expect(canonicalLanguage("JSON")).toBe("json");
	});

	it("returns nothing for a label with no grammar, so no chunk is fetched", () => {
		expect(canonicalLanguage("brainfuck")).toBeUndefined();
		expect(canonicalLanguage("text")).toBeUndefined();
		expect(canonicalLanguage("log")).toBeUndefined();
		expect(canonicalLanguage("constructor")).toBeUndefined();
		expect(canonicalLanguage("__proto__")).toBeUndefined();
		expect(canonicalLanguage(undefined)).toBeUndefined();
	});
});

describe("highlight", () => {
	it("tokenizes a known language into classed spans without changing the code", async () => {
		const code = "func main() {\n\tprintln(\"hi\")\n}";
		const tree = await highlight(code, "go");
		expect(tree).toBeDefined();
		expect(classesIn(tree!)).toContain("hljs-keyword");
		// The rendered text has to be the code, character for character: a
		// highlighter that quietly reformats would make the block disagree with the
		// record the daemon stored.
		expect(textIn(tree!)).toBe(code);
	});

	it("reuses the cached tree instead of parsing the same block twice", async () => {
		const code = "const answer: number = 42;";
		const first = await highlight(code, "typescript");
		const second = await highlight(code, "typescript");
		expect(first).toBeDefined();
		expect(second).toBe(first);
	});

	it("keys the cache on the language too, so the same text can be read two ways", async () => {
		const code = "name: value";
		const asYaml = await highlight(code, "yaml");
		const asSql = await highlight(code, "sql");
		expect(asYaml).toBeDefined();
		expect(asSql).toBeDefined();
		expect(asSql).not.toBe(asYaml);
	});

	it("degrades to no tree for a grammar that was never registered", async () => {
		// Reachable only if the alias table and the engine's language list drift, and
		// the answer then has to be plain monospace rather than a thrown render.
		const tree = await highlight("x = 1", "elixir" as GrammarName);
		expect(tree).toBeUndefined();
	});
});

describe("highlightSync", () => {
	it("answers with the very tree the async path cached", async () => {
		const code = "SELECT id FROM sessions WHERE mode = 'chat';";
		const awaited = await highlight(code, "sql");
		expect(awaited).toBeDefined();
		expect(highlightSync(code, "sql")).toBe(awaited);
	});

	it("tokenizes on the spot once the grammars are warm", async () => {
		await highlight("x = 1", "python");
		// Nothing has ever asked for this block, so a synchronous answer proves the
		// warm path — which is what keeps every fence after the first from flashing.
		expect(highlightSync("def later():\n\treturn 1", "python")).toBeDefined();
	});
});
