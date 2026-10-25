import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { resolveChromeIcon } from "./chromeIcon";
import { symbols } from "./symbols";

describe("resolveChromeIcon", () => {
	it("draws a registry name as its symbol", () => {
		expect(resolveChromeIcon("qrScanner")).toEqual({ symbol: symbols.qrScanner });
	});

	it("passes a one-off pair through", () => {
		const pair = { ios: "tray", android: "inbox" } as const;
		expect(resolveChromeIcon(pair)).toEqual({ symbol: pair });
	});

	it("draws a chrome Lucide name as the system symbol for the same job", () => {
		expect(resolveChromeIcon("refresh-cw")).toEqual({ symbol: symbols.refresh });
		expect(resolveChromeIcon("x")).toEqual({ symbol: symbols.close });
	});

	it("keeps Lucide for a name with no system symbol", () => {
		expect(resolveChromeIcon("git-pull-request")).toEqual({ lucide: "git-pull-request" });
	});

	it("does not mistake an inherited property for a registry name", () => {
		expect(resolveChromeIcon("constructor" as never)).toEqual({ lucide: "constructor" });
	});
});

describe("the icons the shared controls are given", () => {
	const root = fileURLToPath(new URL("..", import.meta.url));
	function sources(dir: string): string[] {
		return readdirSync(join(root, dir)).flatMap((name) => {
			const path = join(dir, name);
			if (statSync(join(root, path)).isDirectory()) return sources(path);
			return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [readFileSync(join(root, path), "utf8")] : [];
		});
	}

	// Lucide is for domain glyphs only. Any other name an empty state or button
	// is handed has to draw as a system symbol.
	const domainGlyphs = new Set(["git-pull-request"]);

	it("draws every chrome glyph as a system symbol", () => {
		const code = [...sources("app"), ...sources("lib")];
		const elements = code.flatMap((source) => [...source.matchAll(/<(?:EmptyState|Button)\b[\s\S]*?\/>/g)].map((match) => match[0]));
		const names = elements.flatMap((element) => [...element.matchAll(/\bicon=(\{[^}\n]*\}|"[^"]*")/g)].flatMap((match) => [...match[1].matchAll(/"([^"]+)"/g)].map((name) => name[1])));
		// The connection failures hand the board's empty state an icon from data.
		const failures = [...readFileSync(join(root, "lib/connectionError.ts"), "utf8").matchAll(/\bicon: "([^"]+)"/g)].map((match) => match[1]);
		expect(names.length).toBeGreaterThan(40);
		const lucide = [...new Set([...names, ...failures])].filter((name) => resolveChromeIcon(name as never).lucide && !domainGlyphs.has(name));
		expect(lucide).toEqual([]);
	});
});
