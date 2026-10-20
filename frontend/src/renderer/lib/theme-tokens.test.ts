import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { THEMES } from "./theme-palettes";
import { buildThemeStylesheet, deriveThemeTokens } from "./theme-tokens";

const tokensCss = readFileSync(resolve(__dirname, "../../styles/tokens.css"), "utf8");

describe("theme tokens", () => {
	it("only derives tokens that tokens.css defines", () => {
		const unknown = Object.keys(deriveThemeTokens(THEMES[1].dark, "dark")).filter(
			(name) => !tokensCss.includes(`${name}:`),
		);
		expect(unknown).toEqual([]);
	});

	it("emits a dark and a light block for every named theme, never for Orchestrate", () => {
		const css = buildThemeStylesheet();
		for (const theme of THEMES.slice(1)) {
			expect(css).toContain(`:root[data-style-theme="${theme.id}"] {`);
			expect(css).toContain(`:root[data-style-theme="${theme.id}"][data-theme="light"] {`);
		}
		expect(css).not.toContain('"orchestrate"');
	});

	it("keeps the Validating lane off the yellow that In review already uses", () => {
		for (const theme of THEMES.slice(1)) {
			for (const mode of ["dark", "light"] as const) {
				const tokens = deriveThemeTokens(theme[mode], mode);
				const validating = tokens["--color-status-validating"];
				expect(validating, `${theme.id}/${mode}`).not.toBe(theme[mode].yellow);
				expect(validating, `${theme.id}/${mode}`).not.toBe(tokens["--color-status-in-review"]);
				expect(validating, `${theme.id}/${mode}`).not.toBe(tokens["--color-status-working"]);
				expect(validating, `${theme.id}/${mode}`).not.toBe(tokens["--color-status-ready"]);
			}
		}
	});

	it("resolves every derived value to a concrete hex color for xterm", () => {
		for (const theme of THEMES) {
			for (const mode of ["dark", "light"] as const) {
				for (const value of Object.values(deriveThemeTokens(theme[mode], mode))) {
					expect(value).toMatch(/^#[0-9a-f]{6}([0-9a-f]{2})?$/);
				}
			}
		}
	});
});
