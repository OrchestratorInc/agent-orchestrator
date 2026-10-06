import { afterEach, describe, expect, it } from "vitest";
import {
	clampRenderHeight,
	readRenderContentHeight,
	readRenderLinkRequest,
	readRenderRef,
	readRenderTheme,
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
});
