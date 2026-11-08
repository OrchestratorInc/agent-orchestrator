import { describe, expect, it } from "vitest";
import { createPaintDetector } from "./terminalPaint";

const bytes = (text: string) => new TextEncoder().encode(text);

describe("createPaintDetector", () => {
	it("ignores escapes, controls and spaces", () => {
		const paints = createPaintDetector();
		expect(paints(bytes("\x1bc\x1b[?1049h\x1b[2J\x1b[H\r\n   \t"))).toBe(false);
		expect(paints(bytes("\x1b]0;claude\x07\x1b]2;title\x1b\\"))).toBe(false);
	});

	it("reports the first printable byte", () => {
		const paints = createPaintDetector();
		expect(paints(bytes("\x1b[1;1H> Ready"))).toBe(true);
	});

	it("counts non-ASCII glyphs as paint", () => {
		expect(createPaintDetector()(bytes("\x1b[H●"))).toBe(true);
	});

	it("keeps an escape split across chunks from reading as text", () => {
		const paints = createPaintDetector();
		expect(paints(bytes("\x1b[?25"))).toBe(false);
		expect(paints(bytes("h\x1b]0;ti"))).toBe(false);
		expect(paints(bytes("tle\x07"))).toBe(false);
		expect(paints(bytes("$"))).toBe(true);
	});

	it("treats escapes with intermediate bytes as escapes, not text", () => {
		const paints = createPaintDetector();
		// xterm-256color's sgr0, which ncurses apps and tmux redraws emit.
		expect(paints(bytes("\x1b(B\x1b[m"))).toBe(false);
		expect(paints(bytes("\x1b)0\x1b#8\x1b%G"))).toBe(false);
		expect(paints(bytes("x"))).toBe(true);
	});

	it("keeps an intermediate escape split across chunks", () => {
		const paints = createPaintDetector();
		expect(paints(bytes("\x1b("))).toBe(false);
		expect(paints(bytes("B"))).toBe(false);
		expect(paints(bytes("A"))).toBe(true);
	});
});
