import type { BrowserWindow, Session } from "electron";
import { allowRenderPage, startRenderCheckProxy } from "./render-check-proxy";

export type RenderCheckMessage = { level: "debug" | "log" | "warning" | "error"; text: string };
export type RenderCheckResult = {
	data: string;
	width: number;
	height: number;
	contentHeight: number;
	consoleMessages: RenderCheckMessage[];
};
export type RenderMeasureResult = { heights: Array<[number, number]> };

type RenderCheckDeps = { BrowserWindow: typeof BrowserWindow };

// Only the daemon's own render pages, published or temporary checks; never an arbitrary URL.
const RENDER_URL = /^http:\/\/(?:127\.0\.0\.1|localhost):\d+\/api\/v1\/sessions\/[^/]+\/renders\/[A-Za-z0-9_-]+$/;
// Chromium console levels 0-3: verbose (console.debug), info (console.log), warning, error.
const LEVELS = ["debug", "log", "warning", "error"] as const;
const MAX_MESSAGES = 20;
const MAX_MESSAGE_CHARS = 500;
const MAX_HEIGHT = 2_000;
const MAX_MEASURE_WIDTHS = 16;
// Shorter than nearly any page, so the measure reads the page's scroll height
// and never a viewport the page stretched to fill.
const MEASURE_VIEWPORT_HEIGHT = 80;
// About one frame: a resize reaches the page this long after setContentSize.
const MEASURE_POLL_MS = 16;
// Time for the page's own resize and ResizeObserver handlers to lay it out again.
const MEASURE_RELAYOUT_MS = 3 * MEASURE_POLL_MS;
// How long one width may take to reach the page before the measure gives up.
const MEASURE_RESIZE_LIMIT_MS = 1_000;
// The measure reads from an isolated world: it sees the page's DOM but not the
// page's globals, so a page's own `innerWidth` variable, or its changes to Math
// or Array, cannot change what it reads. The window has no preload, so this
// world holds nothing else.
const MEASURE_WORLD_ID = 999;
// No "persist:" prefix: Electron keeps this partition in memory only, and every
// check shares it, so checks do not each leave a session behind.
const PARTITION = "ao-render-check";
// One deadline for all the work on a page (load, settle, measure, capture): a
// page that blocks its main thread after loading must not keep the hidden window alive.
const CHECK_DEADLINE_MS = 20_000;
// ponytail: fixed settle for CDN scripts and first animation frames; wait on network idle if pages race it.
const SETTLE_MS = 300;

// The same measurement the reader frame reports (render_bootstrap.js report()):
// a page shorter than the viewport reports its own height, not the viewport's.
export const CONTENT_HEIGHT_SCRIPT = `(() => {
	const r = document.documentElement;
	return Math.ceil(r.scrollHeight > r.clientHeight ? r.scrollHeight : r.getBoundingClientRect().height);
})()`;

// Every check shares the partition's session, so it is proxied once.
const proxiedSessions = new WeakMap<Session, Promise<void>>();

/**
 * Sends every connection the check window makes through the public-address
 * proxy. "<-loopback>" matters: without it Chromium connects to loopback
 * directly and skips the proxy.
 */
function proxyPartition(session: Session): Promise<void> {
	let proxied = proxiedSessions.get(session);
	if (!proxied) {
		proxied = startRenderCheckProxy()
			.then((port) => session.setProxy({ proxyRules: `socks5://127.0.0.1:${port}`, proxyBypassRules: "<-loopback>" }))
			.catch((error: unknown) => {
				proxiedSessions.delete(session);
				throw error;
			});
		proxiedSessions.set(session, proxied);
	}
	return proxied;
}

function renderCheckError(code: string, message: string): Error & { code: string } {
	return Object.assign(new Error(message), { code });
}

/**
 * The daemon page `url` names, parsed before any window exists: the pattern
 * alone passes a port past 65535, which `new URL` rejects.
 */
function renderURL(url: unknown): URL {
	if (typeof url === "string" && RENDER_URL.test(url)) {
		try {
			return new URL(url);
		} catch {
			// Reported below, like any other URL the pattern refuses.
		}
	}
	throw renderCheckError("INVALID_ARGUMENT", "a render check needs a daemon render URL");
}

function isRenderWidth(width: unknown): width is number {
	return typeof width === "number" && Number.isInteger(width) && width >= 240 && width <= 1_600;
}

/** A page height as a frame can show it: 1-2000 whole pixels. */
function clampHeight(height: number): number {
	return Math.min(Math.max(Math.ceil(height) || 1, 1), MAX_HEIGHT);
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * The page's height once its viewport is `width` wide. The resize reaches the
 * page a frame or more after setContentSize, and paints keep arriving at the
 * old size until then, so the page's own viewport is read with its height.
 * The page's resize handlers run after that, so the height is read again once
 * they have had time to lay the page out.
 */
async function heightAt(window: BrowserWindow, width: number): Promise<number> {
	window.setContentSize(width, MEASURE_VIEWPORT_HEIGHT);
	// A script that fails in an isolated world resolves undefined instead of rejecting.
	const read = async (): Promise<unknown[]> =>
		(await window.webContents.executeJavaScriptInIsolatedWorld(MEASURE_WORLD_ID, [{ code: `[innerWidth, ${CONTENT_HEIGHT_SCRIPT}]` }])) ?? [];
	const limit = Date.now() + MEASURE_RESIZE_LIMIT_MS;
	while (Date.now() < limit) {
		const [viewport] = await read();
		if (viewport === width) {
			await sleep(MEASURE_RELAYOUT_MS);
			const [settled, height] = await read();
			if (settled === width) return Number(height);
		}
		await sleep(MEASURE_POLL_MS);
	}
	throw renderCheckError("BROWSER_COMMAND_FAILED", `render measure failed: the viewport did not reach ${width} px`);
}

/** A loaded page in its hidden window; `stage` names the work in progress for the deadline. */
type RenderWindow = { window: BrowserWindow; consoleMessages: RenderCheckMessage[]; stage: string };

/**
 * Loads an agent's page in a throwaway hidden window, the way readers will see
 * it, and runs `use` on it. The window renders offscreen, so it paints without
 * ever being on screen, and never joins the main window or the Browser panel:
 * in-memory partition, sandboxed, no permissions, no popups, no navigation
 * away, and no address on this computer or its network except the page
 * itself. One deadline covers the load and `use`, and the window and the
 * page's allowance go when they end, however they end.
 */
async function withRenderWindow<T>(
	deps: RenderCheckDeps,
	name: string,
	page: URL,
	size: { width: number; height: number },
	signal: AbortSignal | undefined,
	use: (view: RenderWindow) => Promise<T>,
): Promise<T> {
	const window = new deps.BrowserWindow({
		show: false,
		...size,
		webPreferences: {
			offscreen: true,
			sandbox: true,
			contextIsolation: true,
			nodeIntegration: false,
			backgroundThrottling: false,
			partition: PARTITION,
		},
	});
	let release: (() => void) | undefined;
	let deadline: ReturnType<typeof setTimeout> | undefined;
	let onAbort: (() => void) | undefined;
	// Everything after the window exists is inside the try, so a throw while
	// setting it up still destroys it.
	try {
		const contents = window.webContents;
		const view: RenderWindow = { window, consoleMessages: [], stage: "loading the page" };
		contents.session.setPermissionRequestHandler((_contents, _permission, decide) => decide(false));
		contents.session.setPermissionCheckHandler(() => false);
		contents.setWindowOpenHandler(() => ({ action: "deny" }));
		contents.on("will-navigate", (event) => event.preventDefault());
		// No proxy carries WebRTC's UDP.
		contents.setWebRTCIPHandlingPolicy("disable_non_proxied_udp");
		const record = (message: RenderCheckMessage) => {
			if (view.consoleMessages.length < MAX_MESSAGES) view.consoleMessages.push(message);
		};
		contents.on("console-message", (_event, level, message) => {
			record({ level: LEVELS[level] ?? "log", text: message.slice(0, MAX_MESSAGE_CHARS) });
		});
		// The page itself is the one local address the check may load. Each refused
		// destination is reported once, so the agent knows why a resource is missing.
		const refused = new Set<string>();
		release = allowRenderPage(page.hostname, Number(page.port || 80), (destination) => {
			if (refused.has(destination)) return;
			refused.add(destination);
			record({ level: "warning", text: `AO blocked a request to ${destination}. A render check loads only public addresses.` });
		});
		const run = async () => {
			await proxyPartition(contents.session);
			await contents.loadURL(page.href);
			view.stage = "settling";
			await sleep(SETTLE_MS);
			return use(view);
		};
		// Whichever settles first wins; the loser's later rejection stays handled
		// by the race, and the window is destroyed below either way.
		return await Promise.race([
			run(),
			new Promise<never>((_resolve, reject) => {
				deadline = setTimeout(
					() => reject(renderCheckError("BROWSER_COMMAND_FAILED", `${name} timed out after ${CHECK_DEADLINE_MS} ms while ${view.stage}`)),
					CHECK_DEADLINE_MS,
				);
				onAbort = () => reject(renderCheckError("BROWSER_COMMAND_CANCELED", `${name} canceled`));
				if (signal?.aborted) onAbort();
				else signal?.addEventListener("abort", onAbort, { once: true });
			}),
		]);
	} finally {
		clearTimeout(deadline);
		if (onAbort) signal?.removeEventListener("abort", onAbort);
		release?.();
		window.destroy();
	}
}

/** Loads an agent's page as readers will see it, and returns a screenshot, the content height, and console output. */
export async function checkRender(
	deps: RenderCheckDeps,
	args: Record<string, unknown>,
	signal?: AbortSignal,
): Promise<RenderCheckResult> {
	const page = renderURL(args.url);
	const { width } = args;
	if (!isRenderWidth(width)) {
		throw renderCheckError("INVALID_ARGUMENT", "render check width must be an integer from 240 to 1600");
	}
	return withRenderWindow(deps, "render check", page, { width, height: 800 }, signal, async (view) => {
		const contents = view.window.webContents;
		view.stage = "measuring the page";
		const contentHeight = Number(await contents.executeJavaScript(CONTENT_HEIGHT_SCRIPT));
		const height = clampHeight(contentHeight);
		view.stage = "capturing the screenshot";
		// The resize lands asynchronously; capture the first frame painted after
		// it, or the image is cropped to the old size. invalidate() guarantees a
		// frame even when the size did not change.
		const painted = new Promise<void>((resolve) => contents.once("paint", () => resolve()));
		view.window.setContentSize(width, height);
		contents.invalidate();
		await painted;
		const image = await contents.capturePage();
		if (image.isEmpty()) {
			throw renderCheckError("BROWSER_COMMAND_FAILED", "render check captured an empty image");
		}
		return { data: image.toPNG().toString("base64"), width, height, contentHeight, consoleMessages: view.consoleMessages };
	});
}

/**
 * Loads a published page once and returns its height at each width, in the
 * order given, so each reader's frame opens at the height for its own width.
 */
export async function measureRender(
	deps: RenderCheckDeps,
	args: Record<string, unknown>,
	signal?: AbortSignal,
): Promise<RenderMeasureResult> {
	const page = renderURL(args.url);
	const { widths } = args;
	if (!Array.isArray(widths) || widths.length === 0 || widths.length > MAX_MEASURE_WIDTHS || !widths.every(isRenderWidth)) {
		throw renderCheckError("INVALID_ARGUMENT", "render measure needs 1 to 16 widths, each an integer from 240 to 1600");
	}
	const size = { width: widths[0]!, height: MEASURE_VIEWPORT_HEIGHT };
	return withRenderWindow(deps, "render measure", page, size, signal, async (view) => {
		const heights: Array<[number, number]> = [];
		for (const width of widths) {
			view.stage = `measuring the page at ${width} px`;
			heights.push([width, clampHeight(await heightAt(view.window, width))]);
		}
		return { heights };
	});
}
