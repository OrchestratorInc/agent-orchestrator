import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import Markdown from "react-markdown";
import { createElement } from "react";
import { rehypeStreamFade } from "./rehype-stream-fade";

const html = (text: string) =>
	renderToStaticMarkup(createElement(Markdown, { rehypePlugins: [rehypeStreamFade] }, text));
const settled = (text: string, settledOffset: number) =>
	renderToStaticMarkup(createElement(Markdown, { rehypePlugins: [[rehypeStreamFade, { settledOffset }]] }, text));
// Each stream-text wrapper as [settled characters, text].
const wrappers = (out: string) =>
	[...out.matchAll(/<span class="stream-text"(?: data-start="\d+")? data-kept="(\d+)">([^<]*)<\/span>/g)]
		.map(([, kept, text]) => [Number(kept), text] as const);

describe("rehypeStreamFade", () => {
	it("wraps each text node, including inside emphasis", () => {
		const out = html("a **b**");
		expect(wrappers(out)).toEqual([[0, "a "], [0, "b"]]);
		expect(out).toContain("<strong><span");
	});

	it("wraps only the last block", () => {
		const out = html("first\n\nsecond");
		expect(out).toContain("<p>first</p>");
		expect(wrappers(out)).toEqual([[0, "second"]]);
	});

	it("leaves code untouched", () => {
		expect(html("`x` and")).toContain("<code>x</code>");
		expect(html("```\nfoo\n```")).not.toContain("stream-text");
		expect(html("text\n\n```\nfoo\n```")).not.toContain("stream-text");
	});

	it("keeps table whitespace structural", () => {
		const out = html("|a|\n|-|\n|b|");
		expect(out).not.toMatch(/<tr>\s*<span/);
		expect(wrappers(out).length).toBeGreaterThan(0);
	});

	it("leaves a block over the size cap plain", () => {
		expect(wrappers(html("x".repeat(1000)))).toHaveLength(1);
		const text = "x".repeat(1001);
		expect(html(text)).toBe(`<p>${text}</p>`);
		expect(html(`${text} **bold**`)).toBe(`<p>${text} <strong>bold</strong></p>`);
	});

	it("keeps source text before the settled offset settled", () => {
		expect(wrappers(settled("hello world", 5))).toEqual([[5, "hello world"]]);
		// A settled text node keeps its wrapper, so later wrappers keep their positions.
		expect(wrappers(settled("hello", 5))).toEqual([[5, "hello"]]);
		expect(wrappers(settled("first\n\nsecond", 10))).toEqual([[3, "second"]]);
	});

	it("keeps text with a character reference fading until its source has settled", () => {
		// "&amp;" is five source characters but one in the text.
		expect(wrappers(settled("&amp;xy", 5))).toEqual([[0, "&amp;xy"]]);
		expect(wrappers(settled("&amp;xy", 7))).toEqual([[3, "&amp;xy"]]);
	});

	it("never splits a grapheme at the settled offset", () => {
		// The offset lands inside "é" (e + combining accent): the whole cluster fades.
		expect(wrappers(settled("caf\u0065\u0301 ok", 4))).toEqual([[3, "caf\u0065\u0301 ok"]]);
	});

	it("keeps positionless text settled inside an element that was already settled", () => {
		const plugin = rehypeStreamFade({ settledOffset: 20 });
		const tree = {
			type: "root",
			children: [{
				type: "element",
				tagName: "p",
				properties: {},
				position: { start: { line: 1, column: 1, offset: 0 }, end: { line: 1, column: 11, offset: 10 } },
				children: [{ type: "text", value: "split text" }],
			}],
		};
		plugin(tree as never);
		expect(JSON.stringify(tree)).toContain('"dataKept":10');
	});

	it("never throws on a tree it does not expect", () => {
		const transform = rehypeStreamFade();
		expect(() => transform({ type: "root", children: [] })).not.toThrow();
		expect(() => transform({ type: "root" } as never)).not.toThrow();
		expect(() => transform(undefined as never)).not.toThrow();
		expect(html("")).toBe("");
	});
});
