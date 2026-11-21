import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
	clampRenderHeight,
	measuredRenderHeight,
	readArtifactRef,
	readRenderContentHeight,
	readRenderImageRequest,
	readRenderLinkRequest,
	readRenderRef,
	readRenderTheme,
	renderFileName,
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

	it("accepts only a relative artifact path with an artifact-files URL", () => {
		const artifact = { path: "q3/Q3 (final).html", name: "Q3 (final).html", url: "/api/v1/sessions/p-1/artifact-files/q3/Q3%20%28final%29.html" };
		const read = (fields: Record<string, unknown>) => readArtifactRef({ event: "artifact" as const, artifact: { ...artifact, ...fields } as never });
		expect(read({})).toEqual(artifact);
		for (const bad of [
			{ path: "" },
			{ path: "/etc/report.html" },
			{ path: "\\share\\report.html" },
			{ path: "C:/report.html" },
			{ path: "../report.html" },
			{ path: "q3/../../report.html" },
			{ path: "q3\\..\\report.html" },
			{ path: 7 },
			{ name: undefined },
			{ url: "/api/v1/sessions/p-1/renders/r1" },
			{ url: "/api/v1/sessions/p-1/artifact-files/" },
			{ url: "https://evil.example/api/v1/sessions/p-1/artifact-files/x.html" },
			{ url: "/api/v1/sessions/p-1/artifact-files/../../../config" },
			{ url: "/api/v1/sessions/p-1/artifact-files/%2e%2e/renders/r1" },
			{ url: "/api/v1/sessions/../artifact-files/x.html" },
		]) {
			expect(read(bad), JSON.stringify(bad)).toBeUndefined();
		}
		expect(readArtifactRef({ event: "render" as const, artifact } as never)).toBeUndefined();
		expect(readArtifactRef(undefined)).toBeUndefined();
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

	it("sizes a frame from the heights measured nearest its width", () => {
		const heights: Array<[number, number]> = [
			[360, 1400],
			[600, 700],
			[1024, 820],
		];
		expect(measuredRenderHeight(heights, 600)).toBe(700);
		expect(measuredRenderHeight(heights, 480)).toBe(1400);
		expect(measuredRenderHeight(heights, 800)).toBe(820);
		expect(measuredRenderHeight(heights, 300)).toBe(1400);
		expect(measuredRenderHeight(heights, 1600)).toBe(820);
		expect(measuredRenderHeight([[500, 250]], 90)).toBe(250);
	});

	it("names a saved page with the words of its title", () => {
		expect(renderFileName("Weekly spend")).toBe("Weekly spend.html");
		expect(renderFileName('CI <main>: p95 / p99 | "flaky"?')).toBe("CI main p95 p99 flaky.html");
		expect(renderFileName("line one\r\nline two\u0000\u007f")).toBe("line one line two.html");
		expect(renderFileName("")).toBe("Page.html");
		expect(renderFileName("  ***  ")).toBe("Page.html");
		expect(renderFileName("\ufeffBOM\ufefftitle\ufeff")).toBe("BOM title.html");
		expect(renderFileName("ab ".repeat(50))).toBe(`${"ab ".repeat(40).trim()}.html`);
		expect(renderFileName("ж".repeat(125))).toBe(`${"ж".repeat(120)}.html`);
		expect(renderFileName("😀".repeat(121))).toBe(`${"😀".repeat(120)}.html`);
	});

	it("reads only the bootstrap's own protocol messages", () => {
		expect(readRenderContentHeight({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 412.4 } })).toBe(412.4);
		expect(readRenderContentHeight({ method: "ui/notifications/size-changed", params: { height: 412 } })).toBeUndefined();
		expect(readRenderLinkRequest({ jsonrpc: "2.0", id: 1, method: "ui/open-link", params: { url: "https://x.dev/a" } })).toBe("https://x.dev/a");
		expect(readRenderLinkRequest({ jsonrpc: "2.0", id: 1, method: "ui/open-link", params: { url: "javascript:alert(1)" } })).toBeUndefined();
		const image = (params: unknown) => readRenderImageRequest({ jsonrpc: "2.0", method: "ao/open-image", params });
		expect(image({ src: "data:image/png;base64,AA==", alt: "Before" })).toEqual({ src: "data:image/png;base64,AA==", alt: "Before" });
		expect(image({ src: "DATA:IMAGE/svg+xml,<svg/>" })).toEqual({ src: "DATA:IMAGE/svg+xml,<svg/>", alt: "" });
		for (const src of [
			"https://evil.example/?d=secret",
			"https://192.168.1.1/x.png",
			"http://127.0.0.1:3001/api/v1/sessions",
			"//evil.example/x.png",
			"blob:null/1",
			"data:text/html,<b>x</b>",
			"javascript:alert(1)",
			7,
		]) {
			expect(image({ src })).toBeUndefined();
		}
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
			wrapContent: false,
			openImages: true,
		});
		expect(renderThemeMessage(theme, "inline")).toEqual({
			jsonrpc: "2.0",
			method: "ui/notifications/host-context-changed",
			params: { theme: "light", styles: { variables: { "--background": "#fff" } }, displayMode: "inline", wrapContent: false, openImages: true },
		});
	});

	it("carries the wrapping choice in initial and live context for renders and artifacts", () => {
		const theme = { appearance: "dark" as const, variables: {} };
		for (const scrollable of [false, true]) {
			const fragment = renderThemeFragment(theme, "inline", scrollable, true);
			expect(JSON.parse(decodeURIComponent(fragment.slice("#ao-theme=".length)))).toMatchObject({ wrapContent: true });
			expect(renderThemeMessage(theme, "fullscreen", scrollable, true).params.wrapContent).toBe(true);
			expect(renderThemeMessage(theme, "inline", scrollable, false).params.wrapContent).toBe(false);
		}
	});
});

const bootstrapSource = readFileSync("../backend/internal/renderpage/render_bootstrap.js", "utf8");

function bootPage(fragment = "") {
	const location = new URL(`https://reports.example/review.html?view=chart${fragment}`);
	const frame = document.createElement("iframe");
	document.body.append(frame);
	const page = frame.contentDocument!;
	page.head.innerHTML = `<base href="${location.href}"><style id="ao-theme">:root{--background:black}@media(prefers-color-scheme:light){:root{--background:white}}</style><style id="page-style">:root{--background:tan}</style>`;
	Object.defineProperty(page, "readyState", { configurable: true, value: "loading" });
	const documentEvents = vi.spyOn(page, "addEventListener");
	const windowEvents = new Map<string, (event: unknown) => void>();
	const frames: Array<() => void> = [];
	const postMessage = vi.fn();
	const host = { postMessage };
	const observed: Element[] = [];
	let onResize = () => {};
	const view = {
		parent: host,
		innerWidth: 720,
		innerHeight: 480,
		scrollX: 0,
		scrollY: 0,
		scrollTo: vi.fn(),
		addEventListener: (name: string, callback: (event: unknown) => void) => windowEvents.set(name, callback),
		requestAnimationFrame: (callback: () => void) => frames.push(callback),
		ResizeObserver: class {
			constructor(callback: () => void) { onResize = callback; }
			observe(element: Element) { observed.push(element); }
		},
	};
	const history = { state: { retained: true }, replaceState: vi.fn() };
	runInNewContext(bootstrapSource, { window: view, document: page, location, history, URL, URLSearchParams });
	const emitDocument = (name: string, event: object = {}) => {
		for (const [type, callback] of documentEvents.mock.calls) {
			if (type === name && typeof callback === "function") Reflect.apply(callback, page, [event]);
		}
	};
	const context = (params: object, source: unknown = host, jsonrpc = "2.0") =>
		windowEvents.get("message")?.({ source, data: { jsonrpc, method: "ui/notifications/host-context-changed", params } });
	const theme = () => ((page.getElementById("ao-theme") as HTMLStyleElement).sheet!.cssRules[0] as CSSStyleRule).style;
	const click = (href: string, trusted = true) => {
		const link = page.createElement("a");
		link.href = href;
		const child = page.createElement("span");
		link.append(child);
		const event = { isTrusted: trusted, composedPath: () => [child, link, page], preventDefault: vi.fn() };
		emitDocument("click", event);
		return event;
	};
	return { page, view, history, observed, frames, postMessage, theme, context, click, emitDocument, windowEvents, resize: () => onResize() };
}

describe("render page bootstrap", () => {
	afterEach(() => document.body.replaceChildren());

	it("reads the initial context before page styles and removes its fragment", () => {
		const initial = { appearance: "light", variables: { "--background": "#edf4ee", "--label": '"A+B"' }, displayMode: "fullscreen", wrapContent: true, openImages: true };
		const page = bootPage(`#extra=kept&ao-theme=${encodeURIComponent(JSON.stringify(initial))}`);
		expect(page.theme().getPropertyValue("--background")).toBe("#edf4ee");
		expect(page.theme().getPropertyValue("--label")).toBe('"A+B"');
		expect(page.theme().getPropertyValue("color-scheme")).toBe("light");
		expect(page.history.replaceState).toHaveBeenCalledWith(page.history.state, "", "/review.html?view=chart");
		expect(page.page.getElementById("ao-display")?.textContent).toContain("margin-inline:auto");
		expect(page.page.getElementById("ao-wrap")?.textContent).toContain("overflow-wrap:anywhere");
		expect([...page.page.querySelectorAll("style")].slice(0, 2).map((style) => style.id)).toEqual(["ao-theme", "page-style"]);
		expect(page.page.getElementById("page-style")?.textContent).toBe(":root{--background:tan}");
	});

	it("keeps the OS fallback without valid host context and accepts only parent updates", () => {
		const page = bootPage("#ao-theme=%7Bbroken");
		const fallback = page.page.getElementById("ao-theme")!.textContent;
		expect(fallback).toContain("prefers-color-scheme:light");
		expect(page.history.replaceState).not.toHaveBeenCalled();
		const update = { theme: "dark", styles: { variables: { "--foreground": "#abc123" } }, scrollable: true };
		page.context(update, {});
		page.context(update, page.view.parent, "1.0");
		expect(page.page.getElementById("ao-theme")!.textContent).toBe(fallback);
		page.context(update);
		expect(page.theme().getPropertyValue("--foreground")).toBe("#abc123");
		expect(page.theme().getPropertyValue("--background")).toBe("");
		page.context({ wrapContent: true });
		expect(page.theme().getPropertyValue("--foreground")).toBe("#abc123");
		expect(page.page.getElementById("ao-display")?.textContent).toContain("scrollbar-width:auto");
		expect(page.page.getElementById("ao-wrap")?.textContent).toContain("overflow-wrap:anywhere");
		page.context({ theme: "light", styles: { variables: { color: "red", "--escape": "blue;}body{color:red}", "--bad;name": "red" } } });
		const themeStyle = page.page.getElementById("ao-theme") as HTMLStyleElement;
		expect(themeStyle.sheet!.cssRules).toHaveLength(1);
		expect(page.theme().getPropertyValue("color")).toBe("");
		expect(page.theme().getPropertyValue("--bad;name")).toBe("");
	});

	it("forwards only trusted external HTTP links and leaves same-page anchors alone", () => {
		const page = bootPage();
		for (const [href, trusted] of [["https://outside.example/", false], ["#section-2", true], ["mailto:reader@example.org", true]] as const) {
			expect(page.click(href, trusted).preventDefault).not.toHaveBeenCalled();
		}
		expect(page.postMessage).not.toHaveBeenCalled();
		expect(page.click("/details?q=4#next").preventDefault).toHaveBeenCalledOnce();
		expect(page.postMessage).toHaveBeenCalledWith({ jsonrpc: "2.0", method: "ui/open-link", id: "ao-link-1", params: { url: "https://reports.example/details?q=4#next" } }, "*");
	});

	it("coalesces layout changes and reports overflow or fractional box height once", () => {
		const page = bootPage();
		const root = page.page.documentElement;
		Object.defineProperties(root, { scrollHeight: { configurable: true, value: 915 }, clientHeight: { configurable: true, value: 480 } });
		vi.spyOn(root, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 720, 480.2));
		page.emitDocument("DOMContentLoaded");
		page.resize();
		page.resize();
		expect(page.observed).toEqual([root, page.page.body]);
		expect(page.frames).toHaveLength(1);
		page.frames.shift()!();
		expect(page.postMessage).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 915 } }, "*");
		page.resize();
		page.frames.shift()!();
		expect(page.postMessage).toHaveBeenCalledOnce();
		Object.defineProperty(root, "scrollHeight", { value: 480 });
		page.resize();
		page.frames.shift()!();
		expect(page.postMessage).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: 481 } }, "*");
	});

	it("keeps trusted image opening and host-owned zoom controls working", () => {
		const page = bootPage();
		page.context({ openImages: true, zoomable: true, zoom: 1.5 });
		expect(page.page.documentElement.style.transform).toBe("translateX(0%) scale(1.5)");
		const image = page.page.createElement("img");
		image.src = "data:image/png;base64,AA==";
		image.alt = "Chart detail";
		page.windowEvents.get("click")?.({ target: image, isTrusted: false });
		expect(page.postMessage).not.toHaveBeenCalled();
		page.windowEvents.get("click")?.({ target: image, isTrusted: true });
		expect(page.postMessage).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/open-image", params: { src: image.src, alt: image.alt } }, "*");
		const pinch = { ctrlKey: true, deltaY: -35, deltaMode: 0, clientX: 120, clientY: 95, preventDefault: vi.fn() };
		page.windowEvents.get("wheel")?.(pinch);
		expect(pinch.preventDefault).toHaveBeenCalledOnce();
		expect(page.postMessage).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/zoom", params: { deltaY: -35, deltaMode: 0, x: 120, y: 95 } }, "*");
		const reset = { metaKey: true, key: "0", preventDefault: vi.fn() };
		page.emitDocument("keydown", reset);
		expect(page.postMessage).toHaveBeenLastCalledWith({ jsonrpc: "2.0", method: "ao/zoom", params: { action: "reset" } }, "*");
		page.context({ zoom: 1 });
		expect(page.page.documentElement.style.transform).toBe("");
	});
});
