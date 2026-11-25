import { describe, expect, it } from "vitest";
import { mapOutsideMarkdownCode, markdownCodeRanges } from "./markdown-code";

const code = (text: string) => markdownCodeRanges(text).map(([start, end]) => text.slice(start, end));

// The daemon's markdown_code_test.go runs the same cases against its copy.
describe("markdownCodeRanges", () => {
	it.each([
		["inline", "a `x` b", ["`x`"]],
		["double backticks", "a ``x ` y`` b", ["``x ` y``"]],
		["double backticks around a link", "``![a](attachment:0)``", ["``![a](attachment:0)``"]],
		["a run with no partner is text", "unmatched `` here `x`", ["`x`"]],
		["an escaped backtick is text", "\\`x` y", []],
		["a blank line ends the paragraph", "`a\n\nb`", []],
		["fenced block", "```md\n![a](attachment:0)\n```\nafter", ["```md\n![a](attachment:0)\n```\n"]],
		["tilde fence closed by a longer one", "~~~\ncode\n~~~~\n", ["~~~\ncode\n~~~~\n"]],
		["unclosed fence runs to the end", "```\ncode", ["```\ncode"]],
		["indented fence", "- item\n  ```\n  x\n  ```\n", ["  ```\n  x\n  ```\n"]],
		["a backtick in the info string is not a fence", "``` a`b\nx", []],
		["no code", "plain ![a](attachment:0)", []],
	])("%s", (_name, text, want) => {
		expect(code(text)).toEqual(want);
	});
});

describe("mapOutsideMarkdownCode", () => {
	it("changes only the prose", () => {
		expect(mapOutsideMarkdownCode("a ``<b>`` c `<d>` e", (prose) => prose.toUpperCase())).toBe("A ``<b>`` C `<d>` E");
	});
});
