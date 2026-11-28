import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { DESKTOP_ASSETS, readDesktopLogos, sourceHash } from "../scripts/desktop-harness-logos.mjs";
import { harnessInitial, hasLogo, LOGO_KEYS } from "./harnessLogo";
import { HARNESS_LOGO_MANIFEST } from "./harnessLogoManifest";
import { darkTheme, lightTheme } from "./theme";

const PNG = (require("pngjs") as {
	PNG: { sync: { read: (data: Buffer) => { width: number; height: number; data: Buffer } } };
}).PNG;

const repo = path.join(__dirname, "..", "..", "..");
const assets = path.join(__dirname, "..", "assets", "agents");
const SYNC = "run `node packages/mobile/scripts/sync-harness-logos.mjs`";

// The harnesses the daemon accepts, read from the daemon itself so a new one
// cannot ship without a mark here.
const daemonHarnesses = [
	...fs.readFileSync(path.join(repo, "backend", "internal", "domain", "harness.go"), "utf8")
		.matchAll(/^\s*Harness\w+\s+AgentHarness = "([^"]+)"$/gm),
].map(([, id]) => id);

// The fake harness exists only for tests and intentionally has no brand asset.
const NO_ASSET = ["fake"];

function luminance([red, green, blue]: readonly number[]): number {
	const linear = (value: number) => {
		const channel = value / 255;
		return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
	};
	return 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue);
}

function hexRgb(hex: string): [number, number, number] {
	return [1, 3, 5].map((index) => Number.parseInt(hex.slice(index, index + 2), 16)) as [number, number, number];
}

/** The share of a mark's visible pixels that stand out from the background (at least 1.5:1). */
function visibleShare(data: Buffer, background: readonly [number, number, number]): number {
	const backgroundLuminance = luminance(background);
	let visible = 0;
	let distinct = 0;
	for (let i = 0; i < data.length; i += 4) {
		const alpha = data[i + 3] / 255;
		if (alpha === 0) continue;
		visible++;
		const composited = [0, 1, 2].map((channel) =>
			Math.round(data[i + channel] * alpha + background[channel] * (1 - alpha)),
		);
		const foregroundLuminance = luminance(composited);
		const contrast = (Math.max(foregroundLuminance, backgroundLuminance) + 0.05) /
			(Math.min(foregroundLuminance, backgroundLuminance) + 0.05);
		if (contrast >= 1.5) distinct++;
	}
	return distinct / visible;
}

const readAsset = (asset: string) => PNG.sync.read(fs.readFileSync(path.join(assets, `${asset}.png`)));

describe("logo registry", () => {
	it("has a mark for every harness the daemon accepts", () => {
		expect(daemonHarnesses.length).toBeGreaterThan(40);
		for (const h of daemonHarnesses.filter((h) => !NO_ASSET.includes(h))) {
			expect(hasLogo(h), h).toBe(true);
		}
	});

	it("has no mark for the harnesses that ship none, rather than a stand-in", () => {
		for (const h of NO_ASSET) expect(hasLogo(h), h).toBe(false);
	});

	// Desktop's toAgentProvider() funnels anything unrecognised into "codex", so
	// an unknown agent renders as the Codex logo — confidently wrong.
	it("does not claim a mark for an unknown harness", () => {
		expect(hasLogo("some-new-agent")).toBe(false);
		expect(hasLogo(undefined)).toBe(false);
		expect(hasLogo(null)).toBe(false);
		expect(hasLogo("  ")).toBe(false);
	});

	it("is case- and whitespace-insensitive", () => {
		expect(hasLogo("Claude-Code")).toBe(true);
		expect(hasLogo(" codex ")).toBe(true);
	});
});

// The desktop's marks are the source of truth. The app drifted from them once
// (wrong brands for grok, goose, crush and pi, an empty auggie, eleven harnesses
// missing), so these fail as soon as the two disagree.
describe("parity with the desktop", () => {
	const desktop = readDesktopLogos(repo);

	it(`ships the desktop's harnesses and no others (${SYNC})`, () => {
		expect([...LOGO_KEYS].sort()).toEqual(Object.keys(desktop).sort());
	});

	it(`uses the desktop's file for each mark, as a light/dark pair where the desktop has one (${SYNC})`, () => {
		for (const [key, { light, dark }] of Object.entries(desktop)) {
			const entry = HARNESS_LOGO_MANIFEST[key];
			expect(entry?.light.source, key).toBe(light);
			expect(entry?.dark?.source, key).toBe(dark);
		}
	});

	it(`was rasterised from the desktop's current files (${SYNC})`, () => {
		for (const [key, entry] of Object.entries(HARNESS_LOGO_MANIFEST)) {
			for (const file of [entry.light, entry.dark]) {
				if (!file) continue;
				const desktopFile = fs.readFileSync(path.join(repo, DESKTOP_ASSETS, file.source));
				expect(sourceHash(desktopFile), `${key}: ${file.source}`).toBe(file.hash);
			}
		}
	});
});

describe("logo assets", () => {
	// A missing asset is a bundle-time failure that no type check would catch.
	it("match the asset directory exactly, each a 128px PNG", () => {
		const onDisk = fs.readdirSync(assets).filter((f) => f.endsWith(".png")).map((f) => f.replace(/\.png$/, "")).sort();
		const expected = [...new Set(Object.values(HARNESS_LOGO_MANIFEST).flatMap((entry) =>
			[entry.light.asset, entry.dark?.asset].filter((asset): asset is string => Boolean(asset)),
		))].sort();
		expect(onDisk).toEqual(expected);
		for (const asset of onDisk) {
			const png = readAsset(asset);
			expect([png.width, png.height], asset).toEqual([128, 128]);
		}
	});

	it("are each registered for Metro under the scheme they are drawn on", () => {
		const registry = fs.readFileSync(path.join(__dirname, "harnessLogoAssets.ts"), "utf8");
		const req = (asset: string) => `require("../assets/agents/${asset}.png")`;
		for (const [key, { light, dark }] of Object.entries(HARNESS_LOGO_MANIFEST)) {
			const property = /^[a-z_$][\w$]*$/i.test(key) ? key : JSON.stringify(key);
			const value = dark ? `{ light: ${req(light.asset)}, dark: ${req(dark.asset)} }` : `{ light: ${req(light.asset)} }`;
			expect(registry, key).toContain(`\t${property}: ${value},`);
		}
	});

	// No mark sits on a chip, so each theme's mark has to show on that theme's
	// own surfaces. A mark drawn on the wrong one keeps none of itself (white
	// cursor on light, black goose on dark); the faintest right one is tau, whose
	// glyph is about 7% of a black tile.
	it("stay visible on every surface of the theme they are drawn on", () => {
		const surfaces = (theme: typeof lightTheme) => [theme.bgBase, theme.bgSurface, theme.bgElevated].map(hexRgb);
		for (const [key, { light, dark = light }] of Object.entries(HARNESS_LOGO_MANIFEST)) {
			for (const [mark, theme] of [[light, lightTheme], [dark, darkTheme]] as const) {
				const png = readAsset(mark.asset);
				for (const surface of surfaces(theme)) {
					expect(visibleShare(png.data, surface), `${key} (${mark.asset}) on ${surface}`).toBeGreaterThan(0.05);
				}
			}
		}
	});

	it("would lose a mark drawn on the wrong theme, which is what the pairs prevent", () => {
		expect(visibleShare(readAsset("cursor-dark").data, hexRgb(lightTheme.bgSurface))).toBeLessThan(0.05);
		expect(visibleShare(readAsset("goose-light").data, hexRgb(darkTheme.bgElevated))).toBeLessThan(0.05);
	});
});

describe("harnessInitial", () => {
	it("gives the uppercase initial", () => {
		expect(harnessInitial("unknown-agent")).toBe("U");
		expect(harnessInitial("fake")).toBe("F");
	});

	it("falls back to a question mark rather than rendering nothing", () => {
		expect(harnessInitial("")).toBe("?");
		expect(harnessInitial("   ")).toBe("?");
		expect(harnessInitial(undefined)).toBe("?");
	});
});
