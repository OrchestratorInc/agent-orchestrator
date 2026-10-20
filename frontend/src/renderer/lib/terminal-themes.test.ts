import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { buildTerminalThemes } from "./terminal-themes";

const tokensCss = readFileSync(resolve(__dirname, "../../styles/tokens.css"), "utf8");

// DESIGN.md: "The terminal is a working surface … Preserve its own palette."
// Agent TUIs print 24-bit text designed for a dark plate; a plate that follows
// the app theme turns that output white-on-white in light theme (#6861). The
// xterm palette therefore reads dedicated plate tokens pinned to literal dark
// values, which the light theme block must never re-theme.

const PLATE_TOKENS = ["--terminal-plate", "--terminal-plate-foreground", "--terminal-plate-dim"];

function cssBlock(selector: RegExp): string {
	const match = tokensCss.match(selector);
	if (!match || match.index === undefined) return "";
	const bodyStart = tokensCss.indexOf("{", match.index);
	let depth = 0;
	for (let i = bodyStart; i < tokensCss.length; i += 1) {
		if (tokensCss[i] === "{") depth += 1;
		if (tokensCss[i] === "}") {
			depth -= 1;
			if (depth === 0) return tokensCss.slice(bodyStart + 1, i);
		}
	}
	return "";
}

const DARK_BLOCK = /:root,\s*\n\s*:root\.dark,\s*\n\s*\.dark\s*\{/;
const LIGHT_BLOCK = /:root\[data-theme="light"\]\s*\{/;

describe("terminal plate palette stability (#6861)", () => {
	afterEach(() => {
		for (const name of PLATE_TOKENS) {
			document.documentElement.style.removeProperty(name);
		}
		delete document.documentElement.dataset.styleTheme;
	});

	it("pins the plate tokens to literal dark values in the dark block", () => {
		const dark = cssBlock(DARK_BLOCK);
		expect(dark).not.toBe("");
		for (const name of PLATE_TOKENS) {
			const line = dark.split("\n").find((candidate) => candidate.trim().startsWith(`${name}:`));
			expect(line, `${name} declared in the dark block`).toBeDefined();
			expect(line).toMatch(/:\s*(oklch\(|#)/);
		}
	});

	it("never re-themes the plate tokens in the light theme block", () => {
		const light = cssBlock(LIGHT_BLOCK);
		expect(light).not.toBe("");
		const rethemed = PLATE_TOKENS.filter((name) => light.includes(`${name}:`));
		expect(rethemed).toEqual([]);
	});

	it("re-points the plate tokens for named themes so they keep coordinating their palette", () => {
		const named = cssBlock(/:root\[data-style-theme\]\s*\{/);
		expect(named).not.toBe("");
		for (const name of PLATE_TOKENS) {
			const line = named.split("\n").find((candidate) => candidate.trim().startsWith(`${name}:`));
			expect(line, `${name} coordinated for named themes`).toBeDefined();
			expect(line).toMatch(/var\(--(background|foreground|muted-foreground)\)/);
		}
	});

	it("builds both xterm themes from the plate tokens so TUI output keeps its contrast", () => {
		document.documentElement.style.setProperty("--terminal-plate", "oklch(0.210 0.002 250)");
		document.documentElement.style.setProperty("--terminal-plate-foreground", "#d6d6d6");
		const { dark, light } = buildTerminalThemes();
		expect(dark.background).toBe("oklch(0.210 0.002 250)");
		expect(light.background).toBe("oklch(0.210 0.002 250)");
		expect(dark.foreground).toBe("#d6d6d6");
		expect(light.foreground).toBe("#d6d6d6");
	});
});
