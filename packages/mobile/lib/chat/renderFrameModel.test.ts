import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { darkTheme, lightTheme } from "../theme";
import {
	RENDER_BRIDGE_SCRIPT,
	RENDER_MAX_HEIGHT,
	REPORT_SETTLE_LIMIT_MS,
	REPORT_SETTLE_MS,
	RENDER_ZOOM_SCRIPT,
	RENDER_MIN_HEIGHT,
	initialRenderHeight,
	notifyRenderViewport,
	rememberRenderHeight,
	rememberedRenderHeight,
	renderFrameLive,
	restoreRememberedHeights,
	serializeRememberedHeights,
	subscribeRememberedHeights,
	renderSkeletonLines,
	renderViewerStyleScript,
	subscribeRenderViewport,
	isRenderPageLoad,
	readRenderBridgeMessage,
	readRenderRef,
	renderTheme,
	renderThemeFragment,
	renderThemeScript,
} from "./renderFrameModel";

const path = "/api/v1/sessions/ao-215/renders/render-be45c556";

describe("readRenderRef", () => {
	it("reads the page a render activity points at", () => {
		expect(readRenderRef({ event: "render", render: { id: "render-be45c556", title: "Swipe states", height: 520, path } }))
			.toEqual({ id: "render-be45c556", title: "Swipe states", height: 520, path });
	});

	it("keeps measured heights and clamps the agent's height", () => {
		const heights = [[320, 900], [430, 700]];
		expect(readRenderRef({ event: "render", render: { id: "r", title: "T", height: 9000, path, heights } }))
			.toEqual({ id: "r", title: "T", height: RENDER_MAX_HEIGHT, path, heights });
		expect(readRenderRef({ event: "render", render: { id: "r", title: "T", height: 1, path } })?.height).toBe(RENDER_MIN_HEIGHT);
	});

	it("drops malformed measurements but keeps the page", () => {
		expect(readRenderRef({ event: "render", render: { id: "r", title: "T", height: 400, path, heights: [[430, 1], [320, 2]] } }))
			.toEqual({ id: "r", title: "T", height: 400, path });
	});

	it("refuses anything that is not a daemon render route", () => {
		for (const bad of ["https://example.com/page.html", "/api/v1/sessions/a/renders/b/../../files", "/api/v1/sessions/a/artifact-files/x.html"]) {
			expect(readRenderRef({ event: "render", render: { id: "r", title: "T", height: 400, path: bad } })).toBeUndefined();
		}
		expect(readRenderRef({ event: "steer", render: { id: "r", title: "T", height: 400, path } })).toBeUndefined();
		expect(readRenderRef({ event: "render" })).toBeUndefined();
		expect(readRenderRef(undefined)).toBeUndefined();
	});
});

describe("initialRenderHeight", () => {
	const render = { id: "r", title: "T", height: 400, path, heights: [[320, 900], [430, 700], [640, 500]] as Array<[number, number]> };

	it("opens at the measured height for the frame width, the taller neighbour between widths", () => {
		expect(initialRenderHeight(render, 430)).toBe(700);
		expect(initialRenderHeight(render, 390)).toBe(900);
		expect(initialRenderHeight(render, 640)).toBe(500);
		expect(initialRenderHeight(render, 2000)).toBe(700);
	});

	it("falls back to the agent's height before layout or without measurements", () => {
		expect(initialRenderHeight(render, undefined)).toBe(400);
		expect(initialRenderHeight({ ...render, heights: undefined }, 390)).toBe(400);
	});
});

describe("render theme", () => {
	it("hands the page the app's colours under the agent-facing names", () => {
		const dark = renderTheme(darkTheme, "dark");
		expect(dark.appearance).toBe("dark");
		expect(dark.variables["--background"]).toBe(darkTheme.bgBase);
		expect(dark.variables["--foreground"]).toBe(darkTheme.textPrimary);
		expect(dark.variables["--chart-2"]).toBe("#2dd4bf");
		// Without these the page falls back to the WebView's serif default.
		expect(dark.variables["--font-sans"]).toContain("system-ui");
		expect(dark.variables["--font-mono"]).toContain("monospace");
		expect(renderTheme(lightTheme, "light").variables["--chart-2"]).toBe("#0d9488");
	});

	it("seeds first paint through the fragment the bootstrap reads", () => {
		const theme = renderTheme(darkTheme, "dark");
		const fragment = renderThemeFragment(theme);
		expect(fragment.startsWith("#ao-theme=")).toBe(true);
		expect(JSON.parse(decodeURIComponent(fragment.slice("#ao-theme=".length)))).toEqual({ ...theme, displayMode: "inline", wrapContent: false });
		const expanded = renderThemeFragment(theme, "fullscreen");
		expect(JSON.parse(decodeURIComponent(expanded.slice("#ao-theme=".length))).displayMode).toBe("fullscreen");
	});

	it("posts later changes as the host-context message the bootstrap listens for", () => {
		const theme = renderTheme(lightTheme, "light");
		const posted: unknown[] = [];
		new Function("window", renderThemeScript(theme))({ postMessage: (message: unknown) => posted.push(message) });
		expect(posted).toEqual([{ jsonrpc: "2.0", method: "ui/notifications/host-context-changed", params: { theme: "light", styles: { variables: theme.variables } } }]);
	});
});

describe("readRenderBridgeMessage", () => {
	it("reads heights and web links", () => {
		expect(readRenderBridgeMessage(JSON.stringify({ type: "height", height: 612 }))).toEqual({ type: "height", height: 612 });
		expect(readRenderBridgeMessage(JSON.stringify({ type: "link", url: "https://github.com/x" }))).toEqual({ type: "link", url: "https://github.com/x" });
	});

	it("ignores anything else a page posts", () => {
		for (const data of ["not json", "null", JSON.stringify({ type: "height", height: -1 }), JSON.stringify({ type: "link", url: "javascript:alert(1)" }), JSON.stringify({ type: "link", url: "aomobile://pair" })]) {
			expect(readRenderBridgeMessage(data)).toBeUndefined();
		}
	});
});

describe("isRenderPageLoad", () => {
	const uri = `http://192.168.1.5:3001${path}#ao-theme=x`;

	it("allows only the page itself, whatever its fragment", () => {
		expect(isRenderPageLoad(uri, uri)).toBe(true);
		expect(isRenderPageLoad(`http://192.168.1.5:3001${path}`, uri)).toBe(true);
		expect(isRenderPageLoad("https://example.com/", uri)).toBe(false);
		expect(isRenderPageLoad(`http://192.168.1.5:3001/api/v1/sessions`, uri)).toBe(false);
	});

	// iOS asks about the initial load too, with the URL WebKit canonicalized: the
	// default port dropped and the host lowercased. Refusing it cancels the load
	// without an error, and the frame stays a skeleton.
	it("allows the page as WebKit canonicalizes its URL", () => {
		expect(isRenderPageLoad(`https://abc.trycloudflare.com${path}#ao-theme=x`, `https://abc.trycloudflare.com:443${path}#ao-theme=x`)).toBe(true);
		expect(isRenderPageLoad(`http://my-pc.local${path}`, `http://my-pc.local:80${path}#ao-theme=x`)).toBe(true);
		expect(isRenderPageLoad(`http://localhost:3226${path}`, `http://LocalHost:3226${path}#ao-theme=x`)).toBe(true);
		expect(isRenderPageLoad(`https://my-pc.tail1234.ts.net${path}`, `HTTPS://My-PC.tail1234.ts.net:443${path}`)).toBe(true);
	});

	it("still refuses another origin or path once canonicalized", () => {
		const tunnel = `https://abc.trycloudflare.com:443${path}#ao-theme=x`;
		expect(isRenderPageLoad(`http://abc.trycloudflare.com${path}`, tunnel)).toBe(false);
		expect(isRenderPageLoad(`https://abc.trycloudflare.com:8443${path}`, tunnel)).toBe(false);
		expect(isRenderPageLoad(`https://evil.example${path}`, tunnel)).toBe(false);
		expect(isRenderPageLoad(`https://abc.trycloudflare.com${path}/other`, tunnel)).toBe(false);
		expect(isRenderPageLoad(`https://abc.trycloudflare.com${path.toUpperCase()}`, tunnel)).toBe(false);
	});
});

describe("RENDER_ZOOM_SCRIPT", () => {
	it("lifts a page's zoom lock so the expanded view always pinches", () => {
		const meta = { name: "viewport", content: "width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no" };
		const document = { querySelector: () => meta, head: null, documentElement: null };
		new Function("document", RENDER_ZOOM_SCRIPT)(document);
		expect(meta.content).toContain("user-scalable=yes");
		expect(meta.content).toContain("maximum-scale=5");
	});

	it("adds a viewport when the page has none", () => {
		const appended: Array<{ name?: string; content?: string }> = [];
		const document = {
			querySelector: () => null,
			createElement: () => ({}),
			head: { appendChild: (node: { name?: string; content?: string }) => appended.push(node) },
		};
		new Function("document", RENDER_ZOOM_SCRIPT)(document);
		expect(appended).toEqual([{ name: "viewport", content: expect.stringContaining("user-scalable=yes") }]);
	});
});

describe("renderFrameLive", () => {
	const viewport = 800;

	it("mounts a page in view or within half a screen of it", () => {
		expect(renderFrameLive(100, 300, viewport, false)).toBe(true);
		expect(renderFrameLive(viewport + 350, 300, viewport, false)).toBe(true);
		expect(renderFrameLive(-650, 300, viewport, false)).toBe(true);
		expect(renderFrameLive(viewport + 450, 300, viewport, false)).toBe(false);
		expect(renderFrameLive(-800, 300, viewport, false)).toBe(false);
	});

	it("keeps a mounted page until it is a screen and a half away", () => {
		expect(renderFrameLive(viewport + 450, 300, viewport, true)).toBe(true);
		expect(renderFrameLive(viewport + 1150, 300, viewport, true)).toBe(true);
		expect(renderFrameLive(viewport + 1250, 300, viewport, true)).toBe(false);
		expect(renderFrameLive(-1550, 300, viewport, true)).toBe(false);
	});

	it("measures a page this device has never sized two screens before it arrives", () => {
		expect(renderFrameLive(viewport + 1500, 300, viewport, false, false)).toBe(true);
		expect(renderFrameLive(viewport + 1700, 300, viewport, false, false)).toBe(false);
		// Mounted and still measuring: it stays a little further out, so it cannot flap.
		expect(renderFrameLive(viewport + 1900, 300, viewport, true, false)).toBe(true);
		// Once measured, the usual range applies: a page that far out lets go.
		expect(renderFrameLive(viewport + 1900, 300, viewport, true, true)).toBe(false);
	});

	it("keeps a page taller than the screen while any part is near", () => {
		expect(renderFrameLive(-1800, 2000, viewport, false)).toBe(true);
	});
});

describe("remembered render heights", () => {
	const page = (name: string) => `/api/v1/sessions/s/renders/${name}`;

	it("returns the last height a page reported at this frame width", () => {
		rememberRenderHeight(page("a"), 362, 420);
		rememberRenderHeight(page("a"), 362, 610);
		expect(rememberedRenderHeight(page("a"), 362)).toBe(610);
		expect(rememberedRenderHeight(page("never"), 362)).toBeUndefined();
	});

	it("does not reuse a height measured at another width, but offers it before layout", () => {
		rememberRenderHeight(page("rotated"), 362, 500);
		expect(rememberedRenderHeight(page("rotated"), 800)).toBeUndefined();
		expect(rememberedRenderHeight(page("rotated"))).toBe(500);
	});

	it("forgets the oldest page past the cap", () => {
		rememberRenderHeight(page("first"), 362, 100);
		for (let index = 0; index < 200; index++) rememberRenderHeight(page(`p${index}`), 362, 100 + index);
		expect(rememberedRenderHeight(page("first"), 362)).toBeUndefined();
		expect(rememberedRenderHeight(page("p199"), 362)).toBe(299);
	});

	it("tells the saver about changes, but not about a height it already has", () => {
		let saves = 0;
		const unsubscribe = subscribeRememberedHeights(() => { saves += 1; });
		rememberRenderHeight(page("saved"), 362, 300);
		rememberRenderHeight(page("saved"), 362, 300);
		rememberRenderHeight(page("saved"), 362, 320);
		unsubscribe();
		expect(saves).toBe(2);
	});

	it("survives a launch: serialized heights restore into a fresh session", async () => {
		rememberRenderHeight(page("kept"), 362, 444);
		const saved = serializeRememberedHeights();
		// A new launch: the module's cache starts empty.
		vi.resetModules();
		const launch = await import("./renderFrameModel");
		expect(launch.rememberedRenderHeight(page("kept"), 362)).toBeUndefined();
		launch.rememberRenderHeight(page("reported"), 362, 999);
		launch.restoreRememberedHeights(saved);
		expect(launch.rememberedRenderHeight(page("kept"), 362)).toBe(444);
		launch.restoreRememberedHeights(JSON.stringify([[page("reported"), 362, 100]]));
		// This launch reported 999; an older saved height does not replace it.
		expect(launch.rememberedRenderHeight(page("reported"), 362)).toBe(999);
	});

	it("drops restored heights before this launch's when over the cap", async () => {
		vi.resetModules();
		const launch = await import("./renderFrameModel");
		for (let index = 0; index < 200; index++) launch.rememberRenderHeight(page(`now${index}`), 362, 100);
		launch.restoreRememberedHeights(JSON.stringify([[page("old"), 362, 512]]));
		expect(launch.rememberedRenderHeight(page("old"), 362)).toBeUndefined();
		expect(launch.rememberedRenderHeight(page("now0"), 362)).toBe(100);
	});

	it("skips anything malformed in what was saved", () => {
		for (const saved of [null, "", "not json", "{}", JSON.stringify([["https://example.com/x", 362, 100], [page("bad-width"), 0, 100], [page("bad-height"), 362, -1], [page("short"), 362]])]) {
			expect(() => restoreRememberedHeights(saved)).not.toThrow();
		}
		expect(rememberedRenderHeight(page("bad-width"))).toBeUndefined();
		expect(rememberedRenderHeight(page("bad-height"))).toBeUndefined();
	});
});

describe("render viewport signal", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("tells subscribed frames to re-check once a frame, however often it is raised", () => {
		let calls = 0;
		const unsubscribe = subscribeRenderViewport(() => { calls += 1; });
		// A streaming reply raises it on every content-size change.
		for (let index = 0; index < 20; index++) notifyRenderViewport();
		vi.advanceTimersByTime(20);
		expect(calls).toBe(1);
		notifyRenderViewport();
		vi.advanceTimersByTime(20);
		expect(calls).toBe(2);
		unsubscribe();
		notifyRenderViewport();
		vi.advanceTimersByTime(20);
		expect(calls).toBe(2);
	});
});

describe("renderSkeletonLines", () => {
	it("adds caption lines only when the frame has room for them", () => {
		expect(renderSkeletonLines(80)).toEqual([]);
		expect(renderSkeletonLines(159)).toEqual([]);
		expect(renderSkeletonLines(160)).toEqual([0.7, 0.45]);
	});
});

describe("RENDER_BRIDGE_SCRIPT height report", () => {
	type Page = { scrollHeight: number; clientHeight: number; innerHeight: number; contentHeight: number };

	/** Runs the bridge against a fake page; returns the heights it posts, in order, and a way to resize. */
	function bridge(page: Page, images: Array<{ decode: () => Promise<void> }> = []) {
		const heights: number[] = [];
		let resize: (() => void) | undefined;
		const window = {
			ReactNativeWebView: { postMessage: (data: string) => { const message = readRenderBridgeMessage(data); if (message?.type === "height") heights.push(message.height); } },
			addEventListener: () => {},
			innerHeight: page.innerHeight,
			ResizeObserver: class { constructor(callback: () => void) { resize = callback; } observe() {} },
		};
		const document = {
			documentElement: { get scrollHeight() { return page.scrollHeight; }, get clientHeight() { return page.clientHeight; }, getBoundingClientRect: () => ({ height: page.contentHeight }) },
			body: null,
			images,
			addEventListener: () => {},
		};
		new Function("window", "document", "setTimeout", "clearTimeout", RENDER_BRIDGE_SCRIPT)(window, document, setTimeout, clearTimeout);
		return { heights, resize: () => resize?.() };
	}

	async function settle() {
		await vi.advanceTimersByTimeAsync(REPORT_SETTLE_MS + 1);
	}

	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("reports the content, not Android's viewport rounded a pixel taller than the frame", async () => {
		// Measured on Android: a 327dp frame, a 328px viewport, 309px of content.
		const run = bridge({ scrollHeight: 328, clientHeight: 327, innerHeight: 328, contentHeight: 308.9 });
		await settle();
		expect(run.heights).toEqual([309]);
	});

	it("grows a frame the page really overflows", async () => {
		const run = bridge({ scrollHeight: 640, clientHeight: 312, innerHeight: 312, contentHeight: 312 });
		await settle();
		expect(run.heights).toEqual([640]);
	});

	it("keeps the frame's height for a page that fills the viewport, instead of growing it", async () => {
		// Android, min-height:100vh: the content box is the viewport, a pixel taller than the 327 frame.
		const run = bridge({ scrollHeight: 328, clientHeight: 327, innerHeight: 328, contentHeight: 328 });
		await settle();
		expect(run.heights).toEqual([327]);
	});

	it("grows a viewport-filling page that really overflows, then holds", async () => {
		const page = { scrollHeight: 640, clientHeight: 312, innerHeight: 313, contentHeight: 640 };
		const run = bridge(page);
		await settle();
		// The frame now fits the content; the page still tracks the viewport, a pixel taller.
		Object.assign(page, { scrollHeight: 641, clientHeight: 640, innerHeight: 641, contentHeight: 641 });
		run.resize();
		await settle();
		expect(run.heights).toEqual([640]);
	});

	it("holds a page that follows the viewport plus a margin, instead of growing it to the cap", async () => {
		// min-height:100vh with a paragraph's 16px margin: the document is always the viewport + 16.
		const page = { scrollHeight: 329, clientHeight: 312, innerHeight: 313, contentHeight: 329 };
		const run = bridge(page);
		await settle();
		expect(run.heights).toEqual([329]);
		// The frame grew to 329; the page grew with it.
		Object.assign(page, { scrollHeight: 346, clientHeight: 329, innerHeight: 330, contentHeight: 346 });
		run.resize();
		await settle();
		expect(run.heights).toEqual([329]);
	});

	it("still follows content that really grows while the frame stays put", async () => {
		const page = { scrollHeight: 400, clientHeight: 312, innerHeight: 313, contentHeight: 400 };
		const run = bridge(page);
		await settle();
		Object.assign(page, { scrollHeight: 400, clientHeight: 400, innerHeight: 401, contentHeight: 400 });
		run.resize();
		await settle();
		// An image lands: the page grows, the frame has not moved since.
		Object.assign(page, { scrollHeight: 520, contentHeight: 520 });
		run.resize();
		await settle();
		expect(run.heights).toEqual([400, 520]);
	});

	it("shrinks a frame taller than its page to the content height", async () => {
		const run = bridge({ scrollHeight: 520, clientHeight: 520, innerHeight: 520, contentHeight: 318.2 });
		await settle();
		expect(run.heights).toEqual([319]);
	});

	it("says nothing until the images are decoded, so a half-laid-out page is never reported", async () => {
		const page = { scrollHeight: 92, clientHeight: 309, innerHeight: 309, contentHeight: 92 };
		let decoded!: () => void;
		const run = bridge(page, [{ decode: () => new Promise<void>((resolve) => { decoded = resolve; }) }]);
		run.resize();
		await settle();
		expect(run.heights).toEqual([]);
		page.contentHeight = 308.9;
		decoded();
		await settle();
		expect(run.heights).toEqual([309]);
	});

	it("reports once a burst of layout changes holds still", async () => {
		const page = { scrollHeight: 300, clientHeight: 300, innerHeight: 300, contentHeight: 100 };
		const run = bridge(page);
		page.contentHeight = 200; run.resize();
		page.contentHeight = 279; run.resize();
		await settle();
		expect(run.heights).toEqual([279]);
	});

	it("reports anyway when an image never settles", async () => {
		const run = bridge({ scrollHeight: 300, clientHeight: 300, innerHeight: 300, contentHeight: 120 }, [{ decode: () => new Promise<void>(() => {}) }]);
		await vi.advanceTimersByTimeAsync(REPORT_SETTLE_LIMIT_MS + REPORT_SETTLE_MS + 1);
		expect(run.heights).toEqual([120]);
	});
});


describe("renderViewerStyleScript", () => {
	it("keeps the gutter inside the page and hides its scrollbar", () => {
		const appended: Array<{ id?: string; textContent?: string }> = [];
		const document = {
			createElement: () => ({}),
			head: { appendChild: (node: { id?: string; textContent?: string }) => appended.push(node) },
		};
		new Function("document", renderViewerStyleScript(20))(document);
		expect(appended).toHaveLength(1);
		expect(appended[0].id).toBe("ao-viewer");
		expect(appended[0].textContent).toContain("padding-left:20px!important");
		expect(appended[0].textContent).toContain("padding-right:20px!important");
		expect(appended[0].textContent).toContain("html::-webkit-scrollbar{display:none!important}");
	});

	it("still applies before the head exists", () => {
		const appended: unknown[] = [];
		new Function("document", renderViewerStyleScript(16))({ createElement: () => ({}), head: null, documentElement: { appendChild: (node: unknown) => appended.push(node) } });
		expect(appended).toHaveLength(1);
	});
});
