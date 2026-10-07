import { afterEach, describe, expect, it } from "vitest";
import {
	clampRenderHeight,
	measuredRenderHeight,
	readRenderContentHeight,
	readRenderLinkRequest,
	readRenderRef,
	readRenderTheme,
	renderThemeFragment,
	renderThemeMessage,
} from "./render-frame";

describe("render-frame helpers", () => {
	afterEach(() => {
		document.documentElement.removeAttribute("data-theme");
		document.documentElement.style.cssText = "";
	});

	it("accepts only a well-formed render reference with a render route path", () => {
		const ok = { event: "render" as const, render: { id: "r1", title: "Chart", height: 5000, path: "/api/v1/sessions/p-1/renders/r1" } };
		expect(readRenderRef(ok)).toEqual({ id: "r1", title: "Chart", height: 2000, path: "/api/v1/sessions/p-1/renders/r1" });
		expect(readRenderRef({ event: "render" as const, render: { ...ok.render, path: "https://evil.example/x" } })).toBeUndefined();
		expect(readRenderRef({ event: "steer" as const })).toBeUndefined();
		expect(clampRenderHeight(3)).toBe(80);
	});

	it("keeps measured heights only when they are 1-16 positive integer pairs in increasing width", () => {
		const render = { id: "r1", title: "Chart", height: 400, path: "/api/v1/sessions/p-1/renders/r1" };
		const read = (heights: unknown) => readRenderRef({ event: "render" as const, render: { ...render, heights } as never });
		const heights: Array<[number, number]> = [
			[320, 600],
			[640, 300],
		];
		expect(read(heights)).toEqual({ ...render, heights });
		expect(read(undefined)).toEqual(render);
		for (const bad of [
			"320x600",
			[],
			Array.from({ length: 17 }, (_, index) => [320 + index, 100]),
			[[320, 600], [320, 300]],
			[[640, 300], [320, 600]],
			[[320, 0]],
			[[320, 412.5]],
			[[320, Number.POSITIVE_INFINITY]],
			[[320, "600"]],
			[[320, 600, 1]],
			[null],
		]) {
			// The page still shows, at the agent's height.
			expect(read(bad)).toEqual(render);
		}
	});

	it("reads the taller of the nearest measured heights on each side of a width", () => {
		const heights: Array<[number, number]> = [
			[320, 900],
			[520, 400],
			[860, 500],
		];
		expect(measuredRenderHeight(heights, 520)).toBe(400);
		expect(measuredRenderHeight(heights, 700)).toBe(500);
		expect(measuredRenderHeight(heights, 400)).toBe(900);
		expect(measuredRenderHeight(heights, 240)).toBe(900);
		expect(measuredRenderHeight(heights, 1200)).toBe(500);
		expect(measuredRenderHeight([[640, 300]], 1200)).toBe(300);
	});

	it("reads only the bootstrap's own protocol messages", () => {
		expect(readRenderContentHeight({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 412.4 } })).toBe(412.4);
		expect(readRenderContentHeight({ method: "ui/notifications/size-changed", params: { height: 412 } })).toBeUndefined();
		expect(readRenderLinkRequest({ jsonrpc: "2.0", id: 1, method: "ui/open-link", params: { url: "https://x.dev/a" } })).toBe("https://x.dev/a");
		expect(readRenderLinkRequest({ jsonrpc: "2.0", id: 1, method: "ui/open-link", params: { url: "javascript:alert(1)" } })).toBeUndefined();
	});

	it("maps AO tokens to the agent-facing variables and follows data-theme", () => {
		document.documentElement.style.setProperty("--color-bg-primary", "rgb(1, 2, 3)");
		document.documentElement.setAttribute("data-theme", "light");
		const theme = readRenderTheme();
		expect(theme.appearance).toBe("light");
		expect(theme.variables["--background"]).toBe("rgb(1, 2, 3)");
		expect(theme.variables["--chart-6"]).toBeDefined();
	});

	it("hands the page its display mode in the fragment and in every context message", () => {
		const theme = { appearance: "light" as const, variables: { "--background": "#fff" } };
		const fragment = renderThemeFragment(theme, "fullscreen");
		expect(fragment.startsWith("#ao-theme=")).toBe(true);
		expect(JSON.parse(decodeURIComponent(fragment.slice("#ao-theme=".length)))).toEqual({
			appearance: "light",
			variables: { "--background": "#fff" },
			displayMode: "fullscreen",
		});
		expect(renderThemeMessage(theme, "inline")).toEqual({
			jsonrpc: "2.0",
			method: "ui/notifications/host-context-changed",
			params: { theme: "light", styles: { variables: { "--background": "#fff" } }, displayMode: "inline" },
		});
	});
});
