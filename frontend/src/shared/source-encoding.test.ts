import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// Chrome refuses to load a content script that is not valid UTF-8 or that
// contains a Unicode noncharacter (U+FDD0-U+FDEF, U+xFFFE, U+xFFFF), even
// though such a file decodes: one stray character disables the whole
// extension. Keep every shipped source, and the built extension, clean.

const TEXT = /\.(ts|tsx|js|mjs|cjs|json|css|html|md|mdx)$/;

function walk(dir: string, out: string[]): string[] {
	for (const name of readdirSync(dir)) {
		if (name === "node_modules") continue;
		const full = path.join(dir, name);
		if (statSync(full).isDirectory()) walk(full, out);
		else if (TEXT.test(name)) out.push(full);
	}
	return out;
}

/** Problems in one file's bytes: invalid UTF-8 or a noncharacter. */
export function encodingProblems(bytes: Uint8Array): string[] {
	let text: string;
	try {
		text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
	} catch {
		return ["invalid UTF-8"];
	}
	const problems: string[] = [];
	let line = 1;
	for (const char of text) {
		const code = char.codePointAt(0)!;
		if (code === 10) line++;
		if ((code >= 0xfdd0 && code <= 0xfdef) || (code & 0xfffe) === 0xfffe) {
			problems.push(`U+${code.toString(16).toUpperCase().padStart(4, "0")} on line ${line}`);
		}
	}
	return problems;
}

describe("source encoding", () => {
	it("flags noncharacters and invalid UTF-8", () => {
		// Built from code points so this file itself stays ASCII.
		const encode = (...parts: Array<string | number>) =>
			new TextEncoder().encode(parts.map((part) => (typeof part === "number" ? String.fromCodePoint(part) : part)).join(""));
		expect(encodingProblems(encode("ok ", 0xe9, " ", 0xfffd))).toEqual([]);
		expect(encodingProblems(encode("a\n", 0xffff, "b"))).toEqual(["U+FFFF on line 2"]);
		expect(encodingProblems(encode(0xfdd0))).toEqual(["U+FDD0 on line 1"]);
		expect(encodingProblems(encode(0x1fffe))).toEqual(["U+1FFFE on line 1"]);
		expect(encodingProblems(new Uint8Array([0x61, 0xff]))).toEqual(["invalid UTF-8"]);
	});

	it("keeps frontend sources and the built extension loadable by Chrome", () => {
		const roots = [path.resolve(process.cwd(), "src"), path.resolve(process.cwd(), "browser-extension")].filter(existsSync);
		const bad = roots
			.flatMap((root) => walk(root, []))
			.flatMap((file) => encodingProblems(readFileSync(file)).map((problem) => `${path.relative(process.cwd(), file)}: ${problem}`));
		expect(bad).toEqual([]);
	});
});
