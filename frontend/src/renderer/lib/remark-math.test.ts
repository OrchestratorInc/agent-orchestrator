import { describe, expect, it } from "vitest";
import { createDollarDelimiters } from "./remark-math";

describe("createDollarDelimiters", () => {
	const text = "a \\(x\\) b\n\n    \\(indented\\)\n\n> ~~~\n> \\(quoted\\)\n> ~~~\n\n- `\\(inline\\)` and \\[ y \\]\n\n\\[\nz\n\\]\n\n```ts\n\\(fenced\\)\n```\n\nc \\( w \\)";
	const expected = "a $x$ b\n\n    \\(indented\\)\n\n> ~~~\n> \\(quoted\\)\n> ~~~\n\n- `\\(inline\\)` and $$y$$\n\n$$\nz\n$$\n\n```ts\n\\(fenced\\)\n```\n\nc $w$";

	it("rewrites backslash formulas as dollars, leaving every kind of code alone", () => {
		expect(createDollarDelimiters()(text)).toBe(expected);
	});

	it("gives the same text streamed one character at a time", () => {
		const convert = createDollarDelimiters();
		let out = "";
		for (let end = 1; end <= text.length; end++) out = convert(text.slice(0, end));
		expect(out).toBe(expected);
	});

	it("keeps a one-line display formula a block, inside its container", () => {
		expect(createDollarDelimiters()("\\[w\\]\n\n> - \\[v\\]")).toBe("$$\nw\n$$\n\n> - $$\n>   v\n>   $$");
	});

	it("unpads dollar formulas and doubles one a digit follows, as Words reads only those", () => {
		const convert = createDollarDelimiters();
		const padded = "The result is $ x^2 $. Costs $5 and $10, or $y$, $  w $ and $$ z $$.";
		const expected = "The result is $x^2$. Costs $$5 and$$10, or $y$, $w$ and $$ z $$.";
		let out = "";
		for (let end = 1; end <= padded.length; end++) out = convert(padded.slice(0, end));
		expect(out).toBe(expected);
		expect(createDollarDelimiters()(padded)).toBe(expected);
		expect(createDollarDelimiters()("`$ x $` and\n\n```sh\necho $ HOME $\n```")).toBe("`$ x $` and\n\n```sh\necho $ HOME $\n```");
	});

	it("keeps an open display formula open", () => {
		expect(createDollarDelimiters()("\\[\nE = m")).toBe("$$\nE = m");
	});
});
