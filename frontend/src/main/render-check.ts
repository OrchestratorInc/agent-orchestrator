import type { BrowserWindow, NativeImage, Session } from "electron";
import { isOwnPageRequest } from "../shared/agent-page-url";
import { allowRenderPage, type RenderNetwork, startRenderCheckProxy } from "./render-check-proxy";

export type RenderCheckMessage = { level: "debug" | "log" | "warning" | "error"; text: string };
export type RenderCheckResult = {
	data: string;
	/** The page size the screenshot shows, in CSS pixels. */
	width: number;
	height: number;
	/** The PNG's pixel size, smaller than the page when a large image was scaled down. */
	imageWidth: number;
	imageHeight: number;
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
// No "persist:" prefix: Electron keeps these partitions in memory only, and
// every check on a network shares one, so checks do not each leave a session
// behind. Each network has its own partition, since a partition has one proxy.
const PARTITIONS: Record<RenderNetwork, string> = { public: "ao-render-check", none: "ao-render-check-offline" };
// A screenshot this large or larger is scaled down: base64 in the 8 MiB
// browser-runtime result frame, and under the per-image limits of the models
// an agent hands it to.
const MAX_SCREENSHOT_BYTES = 3.5 * 1024 * 1024;
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

// Every check on a network shares its partition's session, so it is proxied once.
const proxiedSessions = new WeakMap<Session, Promise<void>>();

// The page each open check window shows, by webContents id, and where its refusals go.
const checkPages = new Map<number, { page: URL; onRefused: (destination: string) => void }>();

/**
 * Keeps each check window's daemon requests on its own page. The proxy lets
 * the page's host and port through, and that is the daemon, whose other
 * routes would otherwise be open to the page: one fetches any URL it is
 * given, which reaches the network a "none" check withholds.
 */
function guardDaemonRequests(session: Session): void {
	session.webRequest.onBeforeRequest((details, callback) => {
		const check = details.webContentsId === undefined ? undefined : checkPages.get(details.webContentsId);
		const url = URL.canParse(details.url) ? new URL(details.url) : undefined;
		const refused = !!check && !!url && url.host === check.page.host && !isOwnPageRequest(check.page, url);
		if (refused) check.onRefused(`${url.host}${url.pathname}`);
		callback({ cancel: refused });
	});
}

/**
 * Sends every connection the check window makes through the proxy listener
 * for its network. "<-loopback>" matters: without it Chromium connects to
 * loopback directly and skips the proxy. With a proxy, Chromium resolves no
 * names itself, so a "none" page cannot leak data through DNS either.
 */
function proxyPartition(session: Session, network: RenderNetwork): Promise<void> {
	let proxied = proxiedSessions.get(session);
	if (!proxied) {
		guardDaemonRequests(session);
		proxied = startRenderCheckProxy(network)
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

/** The network the daemon allows this agent's pages: never more than the agent's own sandbox has. */
function renderNetwork(network: unknown): RenderNetwork {
	if (network === undefined || network === "public") return "public";
	if (network === "none") return "none";
	throw renderCheckError("INVALID_ARGUMENT", 'render network must be "public" or "none"');
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
 * Evaluates `code` (an array expression) in the isolated world. A script that
 * fails there resolves undefined instead of rejecting, so that reads as empty.
 */
async function readPage(window: BrowserWindow, code: string): Promise<unknown[]> {
	return (await window.webContents.executeJavaScriptInIsolatedWorld(MEASURE_WORLD_ID, [{ code }])) ?? [];
}

/**
 * Resizes the viewport and waits until the page sees the new size: the resize
 * reaches the page a frame or more after setContentSize, and paints keep
 * arriving at the old size until then.
 */
async function resizeTo(window: BrowserWindow, width: number, height: number): Promise<void> {
	window.setContentSize(width, height);
	const limit = Date.now() + MEASURE_RESIZE_LIMIT_MS;
	while (Date.now() < limit) {
		const [viewWidth, viewHeight] = await readPage(window, "[innerWidth, innerHeight]");
		if (viewWidth === width && viewHeight === height) return;
		await sleep(MEASURE_POLL_MS);
	}
	throw renderCheckError("BROWSER_COMMAND_FAILED", `render check failed: the viewport did not reach ${width}x${height} px`);
}

/** The next frame painted after this call, captured. invalidate() guarantees a frame. */
async function capturePainted(window: BrowserWindow): Promise<NativeImage> {
	const contents = window.webContents;
	const painted = new Promise<void>((resolve) => contents.once("paint", () => resolve()));
	contents.invalidate();
	await painted;
	return contents.capturePage();
}

/** Whether an image shows the whole width x height page, at any device scale. */
function showsWholePage(image: NativeImage, width: number, height: number): boolean {
	const size = image.getSize();
	const scale = size.width / width;
	return scale > 0 && Math.abs(size.height - height * scale) <= Math.ceil(scale);
}

/**
 * The screenshot as a PNG no larger than MAX_SCREENSHOT_BYTES where it can be:
 * each pass scales it by the square root of the overshoot, a little more, so
 * three passes cover any page a check allows.
 */
function boundedPNG(image: NativeImage): { png: Buffer; width: number; height: number } {
	let current = image;
	let png = current.toPNG();
	for (let pass = 0; pass < 3 && png.length > MAX_SCREENSHOT_BYTES; pass++) {
		const factor = Math.sqrt(MAX_SCREENSHOT_BYTES / png.length) * 0.9;
		current = current.resize({ width: Math.max(1, Math.floor(current.getSize().width * factor)), quality: "good" });
		png = current.toPNG();
	}
	const size = current.getSize();
	return { png, width: size.width, height: size.height };
}

/**
 * The page's height once its viewport is `width` wide. The resize reaches the
 * page a frame or more after setContentSize, and paints keep arriving at the
 * old size until then, so the page's own viewport is read with its height.
 * The page's resize handlers run after that, so the height is read again once
 * they have had time to lay the page out.
 */
async function heightAt(window: BrowserWindow, width: number): Promise<number> {
	window.setContentSize(width, MEASURE_VIEWPORT_HEIGHT);
	const read = () => readPage(window, `[innerWidth, ${CONTENT_HEIGHT_SCRIPT}]`);
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
	network: RenderNetwork,
	size: { width: number; height: number },
	signal: AbortSignal | undefined,
	use: (view: RenderWindow) => Promise<T>,
): Promise<T> {
	const window = new deps.BrowserWindow({
		show: false,
		...size,
		// The size is the page's viewport, not the window's outer frame, so the
		// page lays out at the width the agent asked for on every platform.
		useContentSize: true,
		webPreferences: {
			offscreen: true,
			sandbox: true,
			contextIsolation: true,
			nodeIntegration: false,
			backgroundThrottling: false,
			partition: PARTITIONS[network],
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
		const why =
			network === "none"
				? "The agent has no network access, so the check loads no network resources."
				: "A render check loads only public addresses.";
		const onRefused = (destination: string) => {
			if (refused.has(destination)) return;
			refused.add(destination);
			record({ level: "warning", text: `AO blocked a request to ${destination}. ${why}` });
		};
		const releasePage = allowRenderPage(page.hostname, Number(page.port || 80), onRefused);
		checkPages.set(contents.id, { page, onRefused });
		release = () => {
			releasePage();
			checkPages.delete(contents.id);
		};
		const run = async () => {
			await proxyPartition(contents.session, network);
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
	const network = renderNetwork(args.network);
	const { width } = args;
	if (!isRenderWidth(width)) {
		throw renderCheckError("INVALID_ARGUMENT", "render check width must be an integer from 240 to 1600");
	}
	return withRenderWindow(deps, "render check", page, network, { width, height: 800 }, signal, async (view) => {
		view.stage = "measuring the page";
		const [measured] = await readPage(view.window, `[${CONTENT_HEIGHT_SCRIPT}]`);
		const contentHeight = Number(measured);
		const height = clampHeight(contentHeight);
		view.stage = "capturing the screenshot";
		// Captured only once the page sees the new size, and again if the first
		// frame was still the old size, or the image is cropped to it.
		await resizeTo(view.window, width, height);
		let image = await capturePainted(view.window);
		if (!showsWholePage(image, width, height)) image = await capturePainted(view.window);
		if (image.isEmpty()) {
			throw renderCheckError("BROWSER_COMMAND_FAILED", "render check captured an empty image");
		}
		if (!showsWholePage(image, width, height)) {
			const size = image.getSize();
			throw renderCheckError("BROWSER_COMMAND_FAILED", `render check captured ${size.width}x${size.height}, not the ${width}x${height} page`);
		}
		const shot = boundedPNG(image);
		return {
			data: shot.png.toString("base64"),
			width,
			height,
			imageWidth: shot.width,
			imageHeight: shot.height,
			contentHeight,
			consoleMessages: view.consoleMessages,
		};
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
	const network = renderNetwork(args.network);
	const { widths } = args;
	if (!Array.isArray(widths) || widths.length === 0 || widths.length > MAX_MEASURE_WIDTHS || !widths.every(isRenderWidth)) {
		throw renderCheckError("INVALID_ARGUMENT", "render measure needs 1 to 16 widths, each an integer from 240 to 1600");
	}
	const size = { width: widths[0]!, height: MEASURE_VIEWPORT_HEIGHT };
	return withRenderWindow(deps, "render measure", page, network, size, signal, async (view) => {
		const heights: Array<[number, number]> = [];
		for (const width of widths) {
			view.stage = `measuring the page at ${width} px`;
			heights.push([width, clampHeight(await heightAt(view.window, width))]);
		}
		return { heights };
	});
}
