import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import Markdown from "react-markdown";
import { createElement } from "react";
import { rehypeStreamFade } from "./rehype-stream-fade";

const html = (text: string) =>
	renderToStaticMarkup(createElement(Markdown, { rehypePlugins: [rehypeStreamFade] }, text));
const spans = (out: string) => out.match(/class="stream-char"/g)?.length ?? 0;

describe("rehypeStreamFade", () => {
	it("wraps prose characters, including inside emphasis", () => {
		const out = html("a **b**");
		expect(spans(out)).toBe(3);
		expect(out).toContain("<strong><span");
	});

	it("wraps only the last block", () => {
		const out = html("first\n\nsecond");
		expect(out).toContain("<p>first</p>");
		expect(spans(out)).toBe(6);
	});

	it("leaves code untouched", () => {
		expect(html("`x` and")).toContain("<code>x</code>");
		expect(html("```\nfoo\n```")).not.toContain("stream-char");
		expect(html("text\n\n```\nfoo\n```")).not.toContain("stream-char");
	});

	it("keeps table whitespace structural", () => {
		const out = html("|a|\n|-|\n|b|");
		expect(out).not.toMatch(/<tr>\s*<span/);
		expect(spans(out)).toBeGreaterThan(0);
	});

	it("keeps a grapheme cluster in one span and emoji as plain text", () => {
		expect(spans(html("é"))).toBe(1);
		const out = html("a👨‍👩‍👧b");
		expect(spans(out)).toBe(2);
		expect(out).toContain("👨‍👩‍👧");
		expect(out).not.toMatch(/stream-char">👨/);
	});

	it("leaves a block over the size cap plain", () => {
		expect(spans(html("x".repeat(4000)))).toBe(4000);
		expect(spans(html("x".repeat(4001)))).toBe(0);
	});

	it("leaves source text before the settled offset plain", () => {
		const settled = (text: string, settledOffset: number) =>
			renderToStaticMarkup(createElement(Markdown, { rehypePlugins: [[rehypeStreamFade, { settledOffset }]] }, text));
		expect(spans(settled("hello world", 5))).toBe(" world".length);
		expect(settled("hello world", 5)).toContain("<p>hello<span");
		expect(spans(settled("hello", 5))).toBe(0);
		expect(spans(settled("first\n\nsecond", 10))).toBe(3);
	});

	it("never splits a grapheme at the settled offset", () => {
		const settled = (text: string, settledOffset: number) =>
			renderToStaticMarkup(createElement(Markdown, { rehypePlugins: [[rehypeStreamFade, { settledOffset }]] }, text));
		// The offset lands inside "é" (e + combining accent): the whole cluster fades.
		const out = settled("caf\u0065\u0301 ok", 4);
		expect(out).toContain("<p>caf<span");
		expect(out).not.toMatch(/<p>cafe<span/);
	});

	it("keeps positionless text plain inside an element that was already settled", () => {
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
		expect(JSON.stringify(tree)).not.toContain("stream-char");
	});

	it("never throws on a tree it does not expect", () => {
		const transform = rehypeStreamFade();
		expect(() => transform({ type: "root", children: [] })).not.toThrow();
		expect(() => transform({ type: "root" } as never)).not.toThrow();
		expect(() => transform(undefined as never)).not.toThrow();
		expect(html("")).toBe("");
	});
});
