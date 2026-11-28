import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { THEMES } from "./theme-palettes";
import { buildThemeStylesheet, deriveThemeTokens } from "./theme-tokens";

const tokensCss = readFileSync(resolve(__dirname, "../../styles/tokens.css"), "utf8");

describe("theme tokens", () => {
	it.each([
		["#0078d4", "#ffffff"],
		["#4493f8", "#ffffff"],
		["#cba6f7", "#000000"],
		["#ffdf60", "#000000"],
		["#000000", "#ffffff"],
		["#ffffff", "#000000"],
	])("uses the selected lighter label preference on %s", (fill, label) => {
		for (const mode of ["dark", "light"] as const) {
			const tokens = deriveThemeTokens({ ...THEMES[0][mode], primary: fill, accent: fill }, mode);
			expect(tokens["--primary-foreground"]).toBe(label);
			expect(tokens["--color-brand-logo-foreground"]).toBe(label);
		}
	});

	it("only derives tokens that tokens.css defines", () => {
		const unknown = Object.keys(deriveThemeTokens(THEMES[1].dark, "dark")).filter(
			(name) => !tokensCss.includes(`${name}:`),
		);
		expect(unknown).toEqual([]);
	});

	it("emits a dark and a light block for every named theme, never for Orchestrate", () => {
		const css = buildThemeStylesheet();
		for (const theme of THEMES.slice(1)) {
			for (const mode of ["dark", "light"] as const) {
				const selector = `:root[data-style-theme="${theme.id}"]${mode === "light" ? '[data-theme="light"]' : ""}`;
				const block = css.split(`${selector} {`)[1]?.split("}")[0];
				expect(block).toContain(`--background: ${theme[mode].bg};`);
			}
		}
		expect(css).not.toContain('"orchestrate"');
		expect(css).not.toContain("html:root");
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

	// Add project, Clone repository and their pickers paint fields with the
	// import-card surface on the import-modal surface. Dark lifts the field
	// above the dialog; light must not paint both the same white, or the
	// fields (folder, owner, repository URL) have no visible edge.
	it("keeps light-theme import fields off the dialog surface", () => {
		const block = tokensCss.match(/:root\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";
		const declared = new Map([...block.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map(([, name, value]) => [name, value.trim()]));
		const resolve = (value: string, seen = new Set<string>()): string => {
			const reference = value.match(/^var\((--[\w-]+)\)$/)?.[1];
			if (!reference || seen.has(reference) || !declared.has(reference)) return value;
			return resolve(declared.get(reference)!, new Set(seen).add(reference));
		};
		const field = resolve("var(--color-bg-import-card)");
		const dialog = resolve("var(--color-bg-import-modal)");
		expect(field).not.toBe("var(--color-bg-import-card)");
		expect(field).not.toBe(dialog);
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
