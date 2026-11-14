import type { BrowserWindow, NativeImage, Session } from "electron";
import { isOwnPageRequest } from "../shared/agent-page-url";
import { allowRenderPage, type RenderNetwork, startRenderCheckProxy } from "./render-check-proxy";

export type RenderCheckMessage = { level: "debug" | "log" | "warning" | "error"; text: string };
export type RenderCheckResult = {
	data: string;
	width: number;
	height: number;
	imageWidth: number;
	imageHeight: number;
	contentHeight: number;
	consoleMessages: RenderCheckMessage[];
};
export type RenderMeasureResult = { heights: Array<[number, number]> };

type RenderCheckDeps = { BrowserWindow: typeof BrowserWindow };

const RENDER_URL = /^http:\/\/(?:127\.0\.0\.1|localhost):\d+\/api\/v1\/sessions\/[^/]+\/renders\/[A-Za-z0-9_-]+$/;
const LEVELS = ["debug", "log", "warning", "error"] as const;
const MAX_MESSAGES = 20;
const MAX_MESSAGE_CHARS = 500;
const MAX_HEIGHT = 2_000;
const MAX_MEASURE_WIDTHS = 16;
const MEASURE_VIEWPORT_HEIGHT = 80;
const MEASURE_POLL_MS = 16;
const MEASURE_RELAYOUT_MS = 3 * MEASURE_POLL_MS;
const MEASURE_RESIZE_LIMIT_MS = 1_000;
const MEASURE_WORLD_ID = 999;
const PARTITIONS: Record<RenderNetwork, string> = { public: "ao-render-check", none: "ao-render-check-offline" };
const MAX_SCREENSHOT_BYTES = 3.5 * 1024 * 1024;
const CHECK_DEADLINE_MS = 20_000;
const SETTLE_MS = 300;

export const CONTENT_HEIGHT_SCRIPT = `(() => {
	const r = document.documentElement;
	return Math.ceil(r.scrollHeight > r.clientHeight ? r.scrollHeight : r.getBoundingClientRect().height);
})()`;

const proxiedSessions = new WeakMap<Session, Promise<void>>();

const checkPages = new Map<number, { page: URL; onRefused: (destination: string) => void }>();

function guardDaemonRequests(session: Session): void {
	session.webRequest.onBeforeRequest((details, callback) => {
		const check = details.webContentsId === undefined ? undefined : checkPages.get(details.webContentsId);
		const url = URL.canParse(details.url) ? new URL(details.url) : undefined;
		const refused = !!check && !!url && url.host === check.page.host && !isOwnPageRequest(check.page, url);
		if (refused) check.onRefused(`${url.host}${url.pathname}`);
		callback({ cancel: refused });
	});
}

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

function renderURL(url: unknown): URL {
	if (typeof url === "string" && RENDER_URL.test(url)) {
		try {
			return new URL(url);
		} catch {
		}
	}
	throw renderCheckError("INVALID_ARGUMENT", "a render check needs a daemon render URL");
}

function renderNetwork(network: unknown): RenderNetwork {
	if (network === undefined || network === "public") return "public";
	if (network === "none") return "none";
	throw renderCheckError("INVALID_ARGUMENT", 'render network must be "public" or "none"');
}

function isRenderWidth(width: unknown): width is number {
	return typeof width === "number" && Number.isInteger(width) && width >= 240 && width <= 1_600;
}

function clampHeight(height: number): number {
	return Math.min(Math.max(Math.ceil(height) || 1, 1), MAX_HEIGHT);
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

async function readPage(window: BrowserWindow, code: string): Promise<unknown[]> {
	return (await window.webContents.executeJavaScriptInIsolatedWorld(MEASURE_WORLD_ID, [{ code }])) ?? [];
}

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

function nextPaint(window: BrowserWindow): Promise<NativeImage> {
	const contents = window.webContents;
	const painted = new Promise<NativeImage>((resolve) => contents.once("paint", (_event, _dirty, image) => resolve(image)));
	contents.invalidate();
	return painted;
}

async function capturePainted(window: BrowserWindow, width: number, height: number): Promise<NativeImage> {
	const limit = Date.now() + MEASURE_RESIZE_LIMIT_MS;
	let image = await nextPaint(window);
	while (!showsWholePage(image, width, height) && Date.now() < limit) {
		await sleep(MEASURE_POLL_MS);
		image = await nextPaint(window);
	}
	return image;
}

function showsWholePage(image: NativeImage, width: number, height: number): boolean {
	const size = image.getSize();
	const scale = size.width / width;
	return scale > 0 && Math.abs(size.height - height * scale) <= Math.ceil(scale);
}

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

type RenderWindow = { window: BrowserWindow; consoleMessages: RenderCheckMessage[]; stage: string };

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
	try {
		const contents = window.webContents;
		const view: RenderWindow = { window, consoleMessages: [], stage: "loading the page" };
		contents.session.setPermissionRequestHandler((_contents, _permission, decide) => decide(false));
		contents.session.setPermissionCheckHandler(() => false);
		contents.setWindowOpenHandler(() => ({ action: "deny" }));
		contents.on("will-navigate", (event) => event.preventDefault());
		contents.setWebRTCIPHandlingPolicy("disable_non_proxied_udp");
		const record = (message: RenderCheckMessage) => {
			if (view.consoleMessages.length < MAX_MESSAGES) view.consoleMessages.push(message);
		};
		contents.on("console-message", (_event, level, message) => {
			record({ level: LEVELS[level] ?? "log", text: message.slice(0, MAX_MESSAGE_CHARS) });
		});
		const refused = new Set<string>();
		const why =
			network === "none"
				? "The agent has no network access, so the check loads no network resources."
				: "A render check loads only public addresses.";
		const onRefused = (destination: string) => {
			if (refused.has(destination)) return;
			refused.add(destination);
			record({ level: "warning", text: `Orchestrator blocked a request to ${destination}. ${why}` });
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
		await resizeTo(view.window, width, height);
		const image = await capturePainted(view.window, width, height);
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
