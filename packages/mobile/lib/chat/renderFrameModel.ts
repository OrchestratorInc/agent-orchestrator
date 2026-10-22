// Pages an agent publishes with `ao render` / html_render. The daemon records
// each as a `system` activity whose detail is `{ event: "render", render }`, and
// serves the page, images inlined, from the session's render route.
// Mirrors frontend/src/renderer/lib/render-frame.ts.
import type { Theme } from "../theme";
import type { ActivityDetail } from "./types";

export type RenderRef = { id: string; title: string; height: number; path: string; heights?: Array<[number, number]> };

export const RENDER_MIN_HEIGHT = 80;
export const RENDER_MAX_HEIGHT = 2000;
const RENDER_PATH = /^\/api\/v1\/sessions\/[^/]+\/renders\/[^/]+$/;
const MAX_MEASURED_WIDTHS = 16;

export function clampRenderHeight(height: number): number {
	return Math.min(RENDER_MAX_HEIGHT, Math.max(RENDER_MIN_HEIGHT, Math.round(height)));
}

/** The page a `render` activity points at, or undefined for anything malformed. */
export function readRenderRef(detail: ActivityDetail | undefined): RenderRef | undefined {
	const render = detail?.event === "render" ? detail.render as Record<string, unknown> | undefined : undefined;
	if (
		!render ||
		typeof render.id !== "string" ||
		typeof render.title !== "string" ||
		typeof render.height !== "number" ||
		!Number.isFinite(render.height) ||
		typeof render.path !== "string" ||
		!RENDER_PATH.test(render.path)
	) {
		return undefined;
	}
	const ref: RenderRef = { id: render.id, title: render.title, height: clampRenderHeight(render.height), path: render.path };
	// A malformed measurement costs only the measured first height, not the page.
	if (isMeasuredHeights(render.heights)) ref.heights = render.heights;
	return ref;
}

const isPositiveInteger = (value: unknown) => typeof value === "number" && Number.isInteger(value) && value > 0;

/** 1-16 `[width, height]` pairs of positive integers, in increasing width. */
function isMeasuredHeights(heights: unknown): heights is Array<[number, number]> {
	return (
		Array.isArray(heights) &&
		heights.length > 0 &&
		heights.length <= MAX_MEASURED_WIDTHS &&
		heights.every(
			(pair, index) =>
				Array.isArray(pair) &&
				pair.length === 2 &&
				isPositiveInteger(pair[0]) &&
				isPositiveInteger(pair[1]) &&
				(index === 0 || pair[0] > heights[index - 1][0]),
		)
	);
}

/**
 * The page's height at a frame width: the taller of the heights at the nearest
 * measured widths on each side, since a breakpoint between them can make the
 * page as tall as either.
 */
export function measuredRenderHeight(heights: Array<[number, number]>, width: number): number {
	const above = heights.findIndex(([measuredWidth]) => measuredWidth >= width);
	const high = above === -1 ? heights.length - 1 : above;
	const low = heights[high][0] === width ? high : Math.max(0, high - 1);
	return Math.max(heights[low][1], heights[high][1]);
}

/** The height a frame opens at before the page reports its own. */
export function initialRenderHeight(render: RenderRef, width: number | undefined): number {
	return clampRenderHeight(render.heights && width ? measuredRenderHeight(render.heights, width) : render.height);
}

/**
 * Whether a frame keeps a live WebView, from where it sits in the window. A
 * page mounts once it is within half a screen of view and unmounts only past
 * a screen and a half, so a frame at the edge does not reload as it wobbles.
 * A page this device has never measured mounts two screens out and stays until
 * it has: it is sized before it reaches the screen, so it never shifts there.
 */
export function renderFrameLive(top: number, height: number, viewportHeight: number, live: boolean, measured = true): boolean {
	const screens = measured ? (live ? 1.5 : 0.5) : (live ? 2.5 : 2);
	const margin = viewportHeight * screens;
	return top + height > -margin && top < viewportHeight + margin;
}

/**
 * The caption lines a loading skeleton draws under its content block, as
 * fractions of the frame width. A short frame is all content block: lines
 * squeezed into it would read as the page itself.
 */
export function renderSkeletonLines(height: number): number[] {
	return height >= 160 ? [0.7, 0.45] : [];
}

const MAX_REMEMBERED_HEIGHTS = 200;
type RememberedHeight = { width: number; height: number };
const rememberedHeights = new Map<string, RememberedHeight>();
const rememberedListeners = new Set<() => void>();

/**
 * The height a page last reported on this device, so a frame opens at it
 * instead of the daemon's estimate. A stored page never changes, so the height
 * stays right; only another frame width (rotation, a tablet) can change it.
 * Before the frame has a width, the last height is the best guess there is.
 */
export function rememberedRenderHeight(path: string, width?: number): number | undefined {
	const remembered = rememberedHeights.get(path);
	if (!remembered || (width !== undefined && remembered.width !== width)) return undefined;
	return remembered.height;
}

export function rememberRenderHeight(path: string, width: number, height: number): void {
	const current = rememberedHeights.get(path);
	if (current?.width === width && current.height === height) return;
	setRemembered(path, { width, height });
	for (const listener of rememberedListeners) listener();
}

function setRemembered(path: string, value: RememberedHeight): void {
	rememberedHeights.delete(path);
	rememberedHeights.set(path, value);
	// Oldest first: a Map iterates in insertion order.
	if (rememberedHeights.size > MAX_REMEMBERED_HEIGHTS) rememberedHeights.delete(rememberedHeights.keys().next().value!);
}

/** Called after each change, so the heights can be saved for the next launch. */
export function subscribeRememberedHeights(listener: () => void): () => void {
	rememberedListeners.add(listener);
	return () => rememberedListeners.delete(listener);
}

/** The remembered heights, oldest first, as saved between launches. */
export function serializeRememberedHeights(): string {
	return JSON.stringify([...rememberedHeights].map(([path, { width, height }]) => [path, width, height]));
}

/**
 * Brings back heights saved by an earlier launch. One reported in this launch
 * is newer and wins, and anything malformed is skipped rather than trusted.
 */
export function restoreRememberedHeights(saved: string | null): void {
	if (!saved) return;
	let entries: unknown;
	try {
		entries = JSON.parse(saved);
	} catch {
		return;
	}
	if (!Array.isArray(entries)) return;
	const current = [...rememberedHeights];
	rememberedHeights.clear();
	for (const entry of entries) {
		if (!Array.isArray(entry) || entry.length !== 3) continue;
		const [path, width, height] = entry as unknown[];
		if (typeof path !== "string" || !RENDER_PATH.test(path) || !isPositiveInteger(width) || typeof height !== "number" || !Number.isFinite(height) || height <= 0) continue;
		setRemembered(path, { width: width as number, height });
	}
	for (const [path, value] of current) setRemembered(path, value);
}

type ViewportListener = () => void;
const viewportListeners = new Set<ViewportListener>();
let viewportCheckScheduled = false;
const nextFrame: (callback: () => void) => unknown =
	typeof requestAnimationFrame === "function" ? requestAnimationFrame : (callback) => setTimeout(callback, 16);

/**
 * Frames re-check whether they are in view; the thread calls it as it scrolls
 * and as its content grows. A streaming reply grows it many times a second and
 * each check is a native measure per frame, so checks coalesce to one a frame.
 */
export function notifyRenderViewport(): void {
	if (viewportCheckScheduled) return;
	viewportCheckScheduled = true;
	nextFrame(() => {
		viewportCheckScheduled = false;
		for (const listener of viewportListeners) listener();
	});
}

export function subscribeRenderViewport(listener: ViewportListener): () => void {
	viewportListeners.add(listener);
	return () => viewportListeners.delete(listener);
}

export type RenderTheme = { appearance: "light" | "dark"; variables: Record<string, string> };

// Fixed categorical series for agent pages only, as desktop hands them.
const CHART_SERIES = {
	dark: ["#2dd4bf", "#fbbf24", "#c084fc", "#fb7185", "#a3e635"],
	light: ["#0d9488", "#d97706", "#9333ea", "#e11d48", "#65a30d"],
} as const;

/** The agent-facing variables (documented in commands/render.md) from the app theme. */
export function renderTheme(t: Theme, appearance: "light" | "dark"): RenderTheme {
	const variables: Record<string, string> = {
		"--background": t.bgBase,
		"--foreground": t.textPrimary,
		"--muted": t.bgSubtle,
		"--muted-foreground": t.textTertiary,
		"--card": t.bgSurface,
		"--card-foreground": t.textPrimary,
		"--popover": t.bgElevated,
		"--border": t.borderSubtle,
		"--border-strong": t.borderStrong,
		"--primary": t.accent,
		"--primary-foreground": t.onAccent,
		"--accent": t.accent,
		"--accent-foreground": t.onAccent,
		"--success": t.green,
		"--warning": t.orange,
		"--destructive": t.red,
		"--code": t.blue,
		"--link": t.highlight,
		"--chart-1": t.blue,
		// The theme replaces the page's defaults wholesale, so these must be
		// present too. The app's Geist is not loaded inside the page.
		"--font-sans": "-apple-system, system-ui, sans-serif",
		"--font-mono": "ui-monospace, Menlo, monospace",
		"--radius": "0.5rem",
	};
	CHART_SERIES[appearance].forEach((color, index) => {
		variables[`--chart-${index + 2}`] = color;
	});
	return { appearance, variables };
}

/** Where the page shows: inline in the thread, or expanded to the whole screen. */
export type RenderDisplayMode = "inline" | "fullscreen";

/**
 * The fragment the page's bootstrap reads its theme from before first paint.
 * Fullscreen centers a width-capped top-level block and shows the scrollbar.
 */
export function renderThemeFragment(theme: RenderTheme, displayMode: RenderDisplayMode = "inline"): string {
	return `#ao-theme=${encodeURIComponent(JSON.stringify({ ...theme, displayMode, wrapContent: false }))}`;
}

/**
 * A live theme change, posted inside the page. The bootstrap accepts messages
 * whose source is its parent; a top-level page is its own parent.
 */
export function renderThemeScript(theme: RenderTheme): string {
	const message = { jsonrpc: "2.0", method: "ui/notifications/host-context-changed", params: { theme: theme.appearance, styles: { variables: theme.variables } } };
	return `window.postMessage(${JSON.stringify(message)}, "*"); true;`;
}

/** How long a page's height must hold still before the frame takes it. */
export const REPORT_SETTLE_MS = 80;
/** The longest a page waits for its images and fonts before it reports anyway. */
export const REPORT_SETTLE_LIMIT_MS = 1500;

/**
 * The bootstrap only reports its height and hands off links when framed; in a
 * WebView the page is top level, so this does both over the native bridge.
 * Only trusted taps open a link, so a page cannot open one on its own.
 */
export const RENDER_BRIDGE_SCRIPT = `(function () {
  var bridge = window.ReactNativeWebView;
  if (!bridge || window.__aoRenderBridge) return;
  window.__aoRenderBridge = true;
  var last, lastView, timer, settled = false;
  function measure() {
    var r = document.documentElement;
    // On Android the viewport rounds a pixel taller than the frame (innerHeight
    // 328 in a 327 frame), so anything that tracks the viewport reads "frame + 1"
    // and would grow the frame without end. scrollHeight never drops below the
    // viewport: it counts only when it beats it, which means real overflow. A
    // page that fills the viewport (min-height:100vh, height:100%) has no height
    // of its own, so it keeps the frame's. Anything else reports its content box.
    var view = Math.max(r.clientHeight, window.innerHeight || 0);
    var content = r.getBoundingClientRect().height;
    var h = r.scrollHeight > view + 1 ? r.scrollHeight : content >= view - 2 ? r.clientHeight : Math.ceil(content);
    if (!h || h === last) return;
    // The frame grew to the last report, and the page grew by just as much: it
    // follows the viewport (100vh plus a margin, say), not its content, and would
    // grow the frame step by step to the cap. Hold the frame where it is.
    if (last !== undefined && view > lastView && Math.abs(h - last - (view - lastView)) <= 2) return;
    last = h;
    lastView = view;
    bridge.postMessage(JSON.stringify({ type: "height", height: h }));
  }
  // A height only counts once it holds still: a layout mid-change would size
  // the frame for a moment and then size it again.
  function report() {
    if (!settled) return;
    clearTimeout(timer);
    timer = setTimeout(measure, ${REPORT_SETTLE_MS});
  }
  // Nothing is reported until the images are decoded and the fonts are in:
  // before that the page measures short (a reloaded page reported 92px of its
  // 309px), and the frame would jump down and back up.
  function settle() {
    if (settled) return;
    settled = true;
    report();
  }
  var waits = Array.prototype.map.call(document.images || [], function (img) {
    return img.decode ? img.decode().catch(function () {}) : null;
  });
  if (document.fonts && document.fonts.ready) waits.push(document.fonts.ready);
  Promise.all(waits).then(settle, settle);
  // A page that never settles (an image that neither loads nor fails) still reports.
  setTimeout(settle, ${REPORT_SETTLE_LIMIT_MS});
  if (window.ResizeObserver) {
    var observer = new window.ResizeObserver(report);
    observer.observe(document.documentElement);
    if (document.body) observer.observe(document.body);
  }
  window.addEventListener("load", report);
  document.addEventListener("click", function (e) {
    if (!e.isTrusted) return;
    var link = e.composedPath().find(function (n) { return n && n.matches && n.matches("a[href]"); });
    if (!link) return;
    var url;
    try { url = new URL(link.getAttribute("href"), document.baseURI); } catch (x) { return; }
    if (url.href.split("#")[0] === location.href.split("#")[0]) return;
    e.preventDefault();
    if (/^https?:$/.test(url.protocol)) bridge.postMessage(JSON.stringify({ type: "link", url: url.href }));
  }, true);
})(); true;`;

/**
 * The expanded view always zooms: a page that wrote its own viewport with
 * user-scalable=no or maximum-scale=1 would otherwise refuse the pinch.
 */
export const RENDER_ZOOM_SCRIPT = `(function () {
  var meta = document.querySelector('meta[name="viewport"]');
  if (!meta) { meta = document.createElement("meta"); meta.name = "viewport"; (document.head || document.documentElement).appendChild(meta); }
  meta.content = "width=device-width, initial-scale=1, minimum-scale=1, maximum-scale=5, user-scalable=yes";
})(); true;`;

/**
 * The expanded view's page styles, set before first paint. The page runs edge
 * to edge, so zoomed content pans to the screen edges, and keeps the thread's
 * gutter inside itself, so at normal zoom it sits where it did inline. Native
 * scroll indicators are off, and this hides the scrollbar the bootstrap shows
 * in its fullscreen mode.
 */
export function renderViewerStyleScript(gutter: number): string {
	const css = `html{padding-left:${gutter}px!important;padding-right:${gutter}px!important;scrollbar-width:none!important}html::-webkit-scrollbar{display:none!important}`;
	return `(function () {
  var style = document.createElement("style");
  style.id = "ao-viewer";
  style.textContent = ${JSON.stringify(css)};
  (document.head || document.documentElement).appendChild(style);
})(); true;`;
}

export type RenderBridgeMessage = { type: "height"; height: number } | { type: "link"; url: string };

/** A message from RENDER_BRIDGE_SCRIPT, or undefined for anything else the page posts. */
export function readRenderBridgeMessage(data: string): RenderBridgeMessage | undefined {
	let message: unknown;
	try {
		message = JSON.parse(data);
	} catch {
		return undefined;
	}
	if (typeof message !== "object" || message === null) return undefined;
	const { type, height, url } = message as Record<string, unknown>;
	if (type === "height" && typeof height === "number" && Number.isFinite(height) && height > 0) return { type, height };
	if (type === "link" && typeof url === "string" && /^https?:\/\//i.test(url)) return { type, url };
	return undefined;
}

/** Whether a navigation is the page itself loading; anything else stays out of the frame. */
export function isRenderPageLoad(url: string, pageUrl: string): boolean {
	return url.split("#")[0] === pageUrl.split("#")[0];
}
