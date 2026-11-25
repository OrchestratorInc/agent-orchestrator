import { describe, expect, it } from "vitest";
import { cssColorToHex } from "./theme-css-color";

describe("cssColorToHex", () => {
	it("reads hex in every length", () => {
		expect(cssColorToHex("#FFF")).toBe("#ffffff");
		expect(cssColorToHex(" #3f0e40 ")).toBe("#3f0e40");
		expect(cssColorToHex("#3f0e4080")).toBe("#3f0e4080");
		expect(cssColorToHex("#3f0e40ff")).toBe("#3f0e40");
		expect(cssColorToHex("#f008")).toBe("#ff000088");
	});

	it("reads rgb() and hsl() in comma and space syntax", () => {
		expect(cssColorToHex("rgb(63, 14, 64)")).toBe("#3f0e40");
		expect(cssColorToHex("rgb(63 14 64)")).toBe("#3f0e40");
		expect(cssColorToHex("rgba(63, 14, 64, 0.5)")).toBe("#3f0e4080");
		expect(cssColorToHex("rgb(100% 0% 0% / 50%)")).toBe("#ff000080");
		expect(cssColorToHex("hsl(0 100% 50%)")).toBe("#ff0000");
		expect(cssColorToHex("hsl(120deg, 100%, 25%)")).toBe("#008000");
		expect(cssColorToHex("hsl(0.5turn 100% 50%)")).toBe("#00ffff");
	});

	it("reads oklch() and oklab(), the form many shared theme files use", () => {
		expect(cssColorToHex("oklch(1 0 0)")).toBe("#ffffff");
		expect(cssColorToHex("oklch(0 0 0)")).toBe("#000000");
		expect(cssColorToHex("oklch(100% 0 0 / 50%)")).toBe("#ffffff80");
		expect(cssColorToHex("oklab(1 0 0)")).toBe("#ffffff");
		// sRGB red is oklch(0.628 0.2577 29.23).
		expect(cssColorToHex("oklch(0.62796 0.25768 29.234)")).toMatch(/^#f[ef]0[0-3]0[0-3]$/);
		// A near-black, slightly blue canvas from a shared theme.
		expect(cssColorToHex("oklch(0.133499 0.003187 246.06)")).toMatch(/^#0[0-9a-f]0[0-9a-f]0[0-9a-f]$/);
	});

	it("brings a color outside sRGB into gamut by dropping chroma", () => {
		const vivid = cssColorToHex("oklch(0.7 0.4 145)");
		expect(vivid).toMatch(/^#[0-9a-f]{6}$/);
		// Still clearly green: green is the largest channel.
		const [r, g, b] = [1, 3, 5].map((start) => Number.parseInt((vivid as string).slice(start, start + 2), 16));
		expect(g).toBeGreaterThan(r);
		expect(g).toBeGreaterThan(b);
	});

	it("knows black, white and transparent", () => {
		expect(cssColorToHex("White")).toBe("#ffffff");
		expect(cssColorToHex("black")).toBe("#000000");
		expect(cssColorToHex("transparent")).toBe("#00000000");
	});

	it("returns no color, and never throws, for numbers that overflow", () => {
		// Each passes the daemon's spelling check; one of them used to crash the
		// theme picker, which resolves a swatch for every saved theme.
		for (const value of ["hsl(1e308turn 50% 50%)", "hsl(1e400 50% 50%)", "hsl(1e308rad 50% 50%)", "rgb(1e400 0 0)", "oklch(1e400 0.1 20)", "oklch(0.5 1e400 20)", "oklch(0.5 0.1 1e400)", "oklab(0.5 1e400 1e400)", "rgb(0 0 0 / 1e400)", "constructor", "tostring", "__proto__"]) {
			expect(() => cssColorToHex(value)).not.toThrow();
			const hex = cssColorToHex(value);
			if (hex !== null) expect(hex).toMatch(/^#[0-9a-f]{6}(?:[0-9a-f]{2})?$/);
		}
		expect(cssColorToHex("hsl(1e308turn 50% 50%)")).toBeNull();
		expect(cssColorToHex("constructor")).toBeNull();
	});

	it("refuses anything that is not a literal color", () => {
		for (const value of ["", "red", "var(--background)", "color-mix(in srgb, red, blue)", "#12", "#12345", "rgb(1 2)", "oklch(a b c)", "url(https://evil.example/a)", "hsl(0 1 0.5)", "rgb(1 2 3 / 4 / 5)", 7, null]) {
			expect(cssColorToHex(value)).toBeNull();
		}
	});
});
