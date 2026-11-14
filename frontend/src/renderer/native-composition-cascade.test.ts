import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve(process.cwd(), "src/renderer/styles.css"), "utf8");

const NATIVE_COMPOSITION = 'html[data-native-browser-composition="true"]';
const LIVE_PAGE = '.browser-panel[data-browser-native-page="live"]';
const DOCKED_LIVE = "[data-browser-native-live]";
const POPOUT_OPEN = "[data-browser-popout-open]";

type Rule = { selector: string; body: string };

function rules(): Rule[] {
	return css
		.split("}")
		.map((chunk) => {
			const open = chunk.indexOf("{");
			if (open === -1) return null;
			return { selector: chunk.slice(0, open).trim(), body: chunk.slice(open + 1).trim() };
		})
		.filter((rule): rule is Rule => rule !== null);
}

function clearsBackgroundFor(match: (selector: string) => boolean): boolean {
	return rules().some(
		(rule) =>
			rule.selector.includes(NATIVE_COMPOSITION) &&
			/background:\s*transparent/.test(rule.body) &&
			rule.selector
				.split(",")
				.map((part) => part.trim())
				.some(match),
	);
}

describe("native-composition transparency cascade", () => {
	it("clears the docked inspector container that wraps the browser panel", () => {
		expect(
			clearsBackgroundFor(
				(selector) => selector.includes('[data-slot="inspector-container"]') && selector.includes(LIVE_PAGE),
			),
		).toBe(true);
	});

	it("clears the app shell root on every platform, not just Windows/Linux", () => {
		expect(
			clearsBackgroundFor((selector) => selector.includes(".app-shell-root") && selector.includes(DOCKED_LIVE)),
		).toBe(true);
	});

	it("clears the browser panel's own viewport", () => {
		expect(clearsBackgroundFor((selector) => selector.includes(".browser-panel__viewport"))).toBe(true);
	});

	it("clears the browser panel body wrapper around the native slot", () => {
		expect(clearsBackgroundFor((selector) => selector.includes(".browser-panel__body"))).toBe(true);
	});

	it("clears the app shell root while the browser panel is popped out", () => {
		expect(
			clearsBackgroundFor(
				(selector) => selector.includes(".app-shell-root") && selector.includes(POPOUT_OPEN),
			),
		).toBe(true);
	});

	it("clears the popped-out overlay surface", () => {
		expect(clearsBackgroundFor((selector) => selector.endsWith(".browser-popout-overlay"))).toBe(true);
	});

	it("clears the popped-out frame that directly wraps the maximized browser panel", () => {
		expect(clearsBackgroundFor((selector) => selector.includes(".browser-popout-frame"))).toBe(true);
	});

	it.each([".browser-popout-frame", ".files-popout-frame"])("spans the expanded %s to both window edges", (selector) => {
		const frameRules = rules().filter((rule) => rule.selector.split(",").some((part) => part.trim().endsWith(selector)));
		const insets = frameRules.flatMap((rule) => [...rule.body.matchAll(/(?:^|[;\s])(left|right|inset-inline):\s*([^;]+);/g)]);
		expect(insets.map((match) => match[1])).toEqual(expect.arrayContaining(["left", "right"]));
		for (const [, property, value] of insets) expect(`${property}: ${value.trim()}`).toBe(`${property}: 0`);
	});

	it("masks the reserved macOS titlebar band for the files popout", () => {
		const maskRule = rules().find((rule) => rule.selector.endsWith(".files-popout-overlay--mac-windowed::before"));
		expect(maskRule?.body).toMatch(/top:\s*calc\(-1 \* var\(--size-traffic-light-clearance\)\)/);
		expect(maskRule?.body).toMatch(/height:\s*var\(--size-traffic-light-clearance\)/);
		expect(maskRule?.body).toMatch(/background:\s*var\(--bg\)/);
	});

	it("centers the browser address bar between the inspector tabs and actions", () => {
		const topbarRule = rules().find((rule) => rule.selector.endsWith(".session-inspector__topbar--browser"));
		expect(topbarRule?.body).toMatch(/grid-template-columns:\s*max-content minmax\(0, 1fr\) max-content/);
		const hostRule = rules().find((rule) => rule.selector === ".session-inspector__topbar > .browser-panel__topbar-host");
		expect(hostRule?.body).toMatch(/justify-self:\s*center/);
		expect(hostRule?.body).toMatch(/width:\s*min\(100%, 240px\)/);
		const addressBarRule = rules().find(
			(rule) => rule.selector.endsWith(".browser-panel__topbar-host > .browser-panel__address-bar"),
		);
		expect(addressBarRule?.body ?? "").not.toMatch(/translateX/);
		expect(css).toMatch(
			/\.session-inspector__topbar--browser:has\(\.browser-panel__address-bar--editing\) > \.browser-panel__topbar-host\s*{[^}]*width:\s*min\(100%, 52cqw, 560px\)/,
		);
		expect(css).toMatch(
			/@container inspector \(max-width: 440px\)[\s\S]*?\.session-inspector__topbar--browser\s*{[\s\S]*?grid-template-rows:\s*var\(--size-inspector-tabs\) var\(--size-inspector-tabs\)/,
		);
		expect(css).toMatch(
			/@container inspector \(max-width: 440px\)[\s\S]*?> \.browser-panel__topbar-host\s*{[\s\S]*?grid-row:\s*2;[\s\S]*?width:\s*180px/,
		);
	});
});
