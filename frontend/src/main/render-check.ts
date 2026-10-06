import type { BrowserWindow } from "electron";

export type RenderCheckMessage = { level: "debug" | "log" | "warning" | "error"; text: string };
export type RenderCheckResult = {
	data: string;
	width: number;
	height: number;
	contentHeight: number;
	consoleMessages: RenderCheckMessage[];
};

type RenderCheckDeps = { BrowserWindow: typeof BrowserWindow };

// Only the daemon's own temporary render-check pages; never an arbitrary URL.
const RENDER_CHECK_URL = /^http:\/\/(?:127\.0\.0\.1|localhost):\d+\/api\/v1\/sessions\/[^/]+\/renders\/check-[A-Za-z0-9_-]+$/;
// Chromium console levels 0-3: verbose (console.debug), info (console.log), warning, error.
const LEVELS = ["debug", "log", "warning", "error"] as const;
const MAX_MESSAGES = 20;
const MAX_MESSAGE_CHARS = 500;
const MAX_CAPTURE_HEIGHT = 2_000;
// No "persist:" prefix: Electron keeps this partition in memory only, and every
// check shares it, so checks do not each leave a session behind.
const PARTITION = "ao-render-check";
// One deadline for the whole check (load, settle, measure, capture): a page that
// blocks its main thread after loading must not keep the hidden window alive.
const CHECK_DEADLINE_MS = 20_000;
// ponytail: fixed settle for CDN scripts and first animation frames; wait on network idle if pages race it.
const SETTLE_MS = 300;

// The same measurement the reader frame reports (render_bootstrap.js report()):
// a page shorter than the viewport reports its own height, not the viewport's.
export const CONTENT_HEIGHT_SCRIPT = `(() => {
	const r = document.documentElement;
	return Math.ceil(r.scrollHeight > r.clientHeight ? r.scrollHeight : r.getBoundingClientRect().height);
})()`;

function renderCheckError(code: string, message: string): Error & { code: string } {
	return Object.assign(new Error(message), { code });
}

/**
 * Loads an agent's page in a throwaway hidden window, the way readers will see
 * it, and returns a screenshot, the content height, and console output. The
 * window renders offscreen, so it paints without ever being on screen, and
 * never joins the main window or the Browser panel: in-memory partition,
 * sandboxed, no permissions, no popups, no navigation away.
 */
export async function checkRender(
	deps: RenderCheckDeps,
	args: Record<string, unknown>,
	signal?: AbortSignal,
): Promise<RenderCheckResult> {
	const { url, width } = args;
	if (typeof url !== "string" || !RENDER_CHECK_URL.test(url)) {
		throw renderCheckError("INVALID_ARGUMENT", "render check needs a daemon render-check URL");
	}
	if (typeof width !== "number" || !Number.isInteger(width) || width < 240 || width > 1_600) {
		throw renderCheckError("INVALID_ARGUMENT", "render check width must be an integer from 240 to 1600");
	}
	const window = new deps.BrowserWindow({
		show: false,
		width,
		height: 800,
		webPreferences: {
			offscreen: true,
			sandbox: true,
			contextIsolation: true,
			nodeIntegration: false,
			backgroundThrottling: false,
			partition: PARTITION,
		},
	});
	const contents = window.webContents;
	const consoleMessages: RenderCheckMessage[] = [];
	contents.session.setPermissionRequestHandler((_contents, _permission, decide) => decide(false));
	contents.session.setPermissionCheckHandler(() => false);
	contents.setWindowOpenHandler(() => ({ action: "deny" }));
	contents.on("will-navigate", (event) => event.preventDefault());
	contents.on("console-message", (_event, level, message) => {
		if (consoleMessages.length >= MAX_MESSAGES) return;
		consoleMessages.push({ level: LEVELS[level] ?? "log", text: message.slice(0, MAX_MESSAGE_CHARS) });
	});
	let stage = "loading the page";
	const capture = async (): Promise<RenderCheckResult> => {
		await contents.loadURL(url);
		stage = "settling";
		await new Promise((resolve) => setTimeout(resolve, SETTLE_MS));
		stage = "measuring the page";
		const contentHeight = Number(await contents.executeJavaScript(CONTENT_HEIGHT_SCRIPT));
		const height = Math.min(Math.max(contentHeight, 1), MAX_CAPTURE_HEIGHT);
		stage = "capturing the screenshot";
		// The resize lands asynchronously; capture the first frame painted after
		// it, or the image is cropped to the old size. invalidate() guarantees a
		// frame even when the size did not change.
		const painted = new Promise<void>((resolve) => contents.once("paint", () => resolve()));
		window.setContentSize(width, height);
		contents.invalidate();
		await painted;
		const image = await contents.capturePage();
		if (image.isEmpty()) {
			throw renderCheckError("BROWSER_COMMAND_FAILED", "render check captured an empty image");
		}
		return { data: image.toPNG().toString("base64"), width, height, contentHeight, consoleMessages };
	};
	let deadline: ReturnType<typeof setTimeout> | undefined;
	let onAbort: (() => void) | undefined;
	try {
		// Whichever settles first wins; the loser's later rejection stays handled
		// by the race, and the window is destroyed below either way.
		return await Promise.race([
			capture(),
			new Promise<never>((_resolve, reject) => {
				deadline = setTimeout(
					() => reject(renderCheckError("BROWSER_COMMAND_FAILED", `render check timed out after ${CHECK_DEADLINE_MS} ms while ${stage}`)),
					CHECK_DEADLINE_MS,
				);
				onAbort = () => reject(renderCheckError("BROWSER_COMMAND_CANCELED", "render check canceled"));
				if (signal?.aborted) onAbort();
				else signal?.addEventListener("abort", onAbort, { once: true });
			}),
		]);
	} finally {
		clearTimeout(deadline);
		if (onAbort) signal?.removeEventListener("abort", onAbort);
		window.destroy();
	}
}
