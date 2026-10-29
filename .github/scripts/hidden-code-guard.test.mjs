import assert from "node:assert/strict";
import test from "node:test";

import { formatFindings, parseAddedLines, scan } from "./hidden-code-guard.mjs";

const fakePayload = ["global", ".o='x';var _", "$_abcd=(function(a,q){"].join("").padEnd(1200, "x");
const padded = (head) => head + " ".repeat(709) + fakePayload;
const diff = (file, text, line = 1) => `diff --git a/${file} b/${file}\n--- a/${file}\n+++ b/${file}\n@@ -${line} +${line} @@\n-old\n+${text}\n`;
const check = (file, text) => scan(parseAddedLines(diff(file, text)));

test("flags all three rules on a synthetic padded next config line", () => {
	const file = "frontend/src/docs/next.config.mjs";
	const findings = scan(parseAddedLines(diff(file, padded("export default withMDX(config);"), 51)));
	assert.deepEqual(findings.map(({ rule, file, line }) => ({ rule, file, line })),
		["R1", "R2", "R3"].map((rule) => ({ rule, file, line: 51 })));
	const output = formatFindings(findings);
	assert.match(output, /:51 padding: 709 spaces inside a line \(limit 150\)/);
	assert.match(output, /<709 spaces>/);
	assert.match(output, /skip-hidden-code-guard/);
	for (const excerpt of output.split("\n").filter((line) => line.startsWith("  "))) {
		assert.ok(excerpt.slice(2).length <= 120);
		assert.ok(excerpt.endsWith("..."));
	}
	const sanitized = formatFindings([{ ...findings[0], text: "a\u0000\u007fb\u202ec" }]);
	assert.match(sanitized, /\n  abc\n/);
});

test("flags all three rules on a synthetic padded mobile babel config line", () => {
	const file = "packages/mobile/babel.config.js";
	const findings = scan(parseAddedLines(diff(file, padded("};"), 12)));
	assert.deepEqual(findings.map(({ rule, file, line }) => ({ rule, file, line })),
		["R1", "R2", "R3"].map((rule) => ({ rule, file, line: 12 })));
});

test("allows terminal fixture lines with 115 inner spaces", () => {
	assert.deepEqual(check("backend/terminal_test.go", "visible" + " ".repeat(115) + "screen"), []);
});

test("allows markdown tables with 250 inner spaces", () => {
	for (const file of ["README.md", "docs/guide.mdx"]) {
		assert.deepEqual(check(file, "| left" + " ".repeat(250) + "| right |"), []);
	}
});

test("allows long prose and ordinary source lines", () => {
	for (const [file, length] of [["guide.md", 605], ["guide.mdx", 401], ["source.ts", 299]]) {
		assert.deepEqual(check(file, "x".repeat(length)), []);
	}
});

test("enforces the exact line length and padding boundaries", () => {
	for (const file of ["vite.config.js", "next.config.mjs", "babel.config.cjs", "jest.config.ts", "package.json", "packages/app/package.json", ".github/workflows/check.yml"]) {
		assert.deepEqual(check(file, "x".repeat(500)), []);
		assert.deepEqual(check(file, "x".repeat(501)).map(({ rule }) => rule), ["R2"]);
	}
	assert.deepEqual(check("source.ts", "a" + " ".repeat(149) + "b"), []);
	for (const whitespace of [" ".repeat(150), "\t".repeat(150), " \t".repeat(75)]) {
		assert.deepEqual(check("source.ts", `a${whitespace}b`).map(({ rule }) => rule), ["R1"]);
	}
});

test("allows large single line JSON data files", () => {
	assert.deepEqual(check("pricing/catalog/v1/providers/x.json", "x".repeat(16843)), []);
	assert.deepEqual(check(".github/scripts/contributor-project/data/blame-lines.json", "x".repeat(2000)), []);
});

test("ignores removed payloads and deleted files", () => {
	const removed = diff("next.config.mjs", "safe").replace("-old", `-${padded("visible;")}`);
	const deleted = `diff --git a/babel.config.js b/babel.config.js\n--- a/babel.config.js\n+++ /dev/null\n@@ -1 +0,0 @@\n-${padded("};")}\n`;
	assert.deepEqual(scan(parseAddedLines(removed + deleted)), []);
});

test("tracks new line numbers across hunks and renamed files", () => {
	const patch = [
		"diff --git a/old.ts b/renamed.ts", "similarity index 90%", "rename from old.ts", "rename to renamed.ts",
		"--- a/old.ts", "+++ b/renamed.ts", "@@ -2,0 +3,2 @@", "+first", "+second",
		"@@ -8,2 +10,3 @@", "-old", "+third", " context", "+fourth", "\\ No newline at end of file",
		"diff --git a/old.config.js b/new.config.js", "--- a/old.config.js", "+++ b/new.config.js",
		"@@ -5 +6 @@", "-old", "+fifth", "@@ -20,0 +22 @@", "+++ b/content is not a header",
	].join("\n");
	assert.deepEqual(parseAddedLines(patch), [
		{ file: "renamed.ts", line: 3, text: "first" },
		{ file: "renamed.ts", line: 4, text: "second" },
		{ file: "renamed.ts", line: 10, text: "third" },
		{ file: "renamed.ts", line: 12, text: "fourth" },
		{ file: "new.config.js", line: 6, text: "fifth" },
		{ file: "new.config.js", line: 22, text: "++ b/content is not a header" },
	]);
	const quoted = diff("file.config.js", "x".repeat(501)).replace("+++ b/file.config.js", "+++ \"b/renamed\\t\\303\\251.config.js\"");
	assert.equal(scan(parseAddedLines(quoted))[0].file, "renamed\té.config.js");
	const spaced = diff("x", "x".repeat(501)).replace("+++ b/x", "+++ b/some dir/vite.config.js\t");
	assert.equal(scan(parseAddedLines(spaced))[0].file, "some dir/vite.config.js");
});

test("flags each family marker in ordinary TSX without padding", () => {
	for (const marker of [["global", ".o='x';"], ["var _", "$_abcd=0;"], ["String.fromCharCode", "(127)"]]) {
		assert.deepEqual(check("frontend/component.tsx", marker.join("")).map(({ rule }) => rule), ["R3"]);
	}
});
