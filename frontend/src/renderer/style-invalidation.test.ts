import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Some Tailwind variants compile to selectors that make Chromium restyle the
 * whole app on every DOM change: each tooltip, hover card, streamed chat line
 * and keystroke then costs 10-30 ms of style recalc. jsdom applies no
 * stylesheet, so no behaviour test can see this; pin it at the source.
 *
 * - group-has-* / peer-has-*: `:is(:where(.group):has(...) *)`. A :has() inside
 *   :is() turns every ancestor into a :has() anchor. Use the focus-visible-in-*
 *   variants in styles.css, or a plain arbitrary variant such as
 *   `[.x:has(...)>&]`.
 * - group-last, group-first, group-odd, ... and in-[...] with a structural
 *   pseudo-class: `:last-child` on any ancestor, so adding a node to <body>
 *   restyles the whole document. Match the one ancestor with child
 *   combinators instead, e.g. `[:last-child>*>&]`.
 *
 * The rule only has to exist in the stylesheet to cost this, and Tailwind
 * generates CSS for class names it finds in any file it scans, tests included.
 * So every scanned file is checked, and the samples below are escaped
 * (\u005b is "[", \u002d is "-") so Tailwind does not read them as classes.
 */

const ROOTS = [path.resolve(process.cwd(), "src"), path.resolve(process.cwd(), "../packages/product-ui/src")];

// Variant names only: suffixes such as -of-type, -last, -3 or -[2n+1] are matched after them.
const STRUCTURAL = "has|first|last|only|odd|even|empty|nth";
const FORBIDDEN = [
	new RegExp(`\\b(?:group|peer)-(?:${STRUCTURAL})(?:-[\\w-]*\\[[^\\s"'\`]*?\\]|-[\\w-]+)?(?:/[\\w-]+)?:[\\w[!-]`, "g"),
	/\bin-\[[^\s"'`\]]*(?::has\(|:first|:last|:only|:nth|:empty)[^\s"'`]*\]:[\w[!-]/g,
];

function sourceFiles(directory: string): string[] {
	return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
		const absolute = path.join(directory, entry.name);
		if (entry.isDirectory()) return sourceFiles(absolute);
		if (!/\.(tsx?|jsx?|html|md)$/.test(entry.name)) return [];
		return [absolute];
	});
}

describe("style invalidation", () => {
	it("uses no Tailwind variant that restyles the whole app on DOM changes", () => {
		const offenders = ROOTS.flatMap(sourceFiles).flatMap((file) => {
			const source = readFileSync(file, "utf8");
			return FORBIDDEN.flatMap((pattern) => [...source.matchAll(pattern)].map((match) => `${path.relative(process.cwd(), file)}: ${match[0]}`));
		});
		expect(offenders).toEqual([]);
	});

	it("catches the forms it is meant to catch", () => {
		const hits = (value: string) => FORBIDDEN.some((pattern) => value.match(new RegExp(pattern.source)) !== null);
		expect(hits("group-has-\u005b:focus-visible]/session-row:opacity-100")).toBe(true);
		expect(hits("group-has-\u005b:focus-visible]:block")).toBe(true);
		expect(hits("group-has-data-\u005bstate=open]:opacity-100")).toBe(true);
		expect(hits("group\u002dlast/inspector-section:pb-0")).toBe(true);
		expect(hits("peer\u002dfirst:mt-0")).toBe(true);
		expect(hits("group\u002dnth-\u005b2n+1]:block")).toBe(true);
		expect(hits("group\u002dnth-last-of-type-2:block")).toBe(true);
		expect(hits("peer\u002dfirst-of-type:mt-0")).toBe(true);
		expect(hits("in-\u005btr:first-child]:pt-3")).toBe(true);
		expect(hits("focus-visible-in-session-row:opacity-100")).toBe(false);
		expect(hits("[:last-child>*>&]:pb-0")).toBe(false);
		expect(hits("group-hover/session-row:opacity-100")).toBe(false);
		expect(hits("in-data-dragging:opacity-0")).toBe(false);
		expect(hits("has-[[data-state=open]]:opacity-100")).toBe(false);
	});
});
