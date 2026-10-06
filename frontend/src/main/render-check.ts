import { randomUUID } from "node:crypto";
import type { BaseWindow, WebContentsView } from "electron";

export type RenderCheckMessage = { level: "debug" | "log" | "warning" | "error"; text: string };
export type RenderCheckResult = {
	data: string;
	width: number;
	height: number;
	contentHeight: number;
	consoleMessages: RenderCheckMessage[];
};

type RenderCheckDeps = {
	WebContentsView: typeof WebContentsView;
	window: Pick<BaseWindow, "contentView">;
};

// Only the daemon's own temporary render-check pages; never an arbitrary URL.
const RENDER_CHECK_URL = /^http:\/\/(?:127\.0\.0\.1|localhost):\d+\/api\/v1\/sessions\/[^/]+\/renders\/check-[A-Za-z0-9_-]+$/;
// Chromium console levels 0-3: verbose (console.debug), info (console.log), warning, error.
const LEVELS = ["debug", "log", "warning", "error"] as const;
const MAX_MESSAGES = 20;
const MAX_MESSAGE_CHARS = 500;
const MAX_CAPTURE_HEIGHT = 2_000;
const LOAD_TIMEOUT_MS = 15_000;
// ponytail: fixed settle for CDN scripts and first animation frames; wait on network idle if pages race it.
const SETTLE_MS = 300;

function renderCheckError(code: string, message: string): Error & { code: string } {
	return Object.assign(new Error(message), { code });
}

function wait(ms: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(resolve, ms);
		signal?.addEventListener("abort", () => {
			clearTimeout(timer);
			reject(renderCheckError("BROWSER_COMMAND_CANCELED", "render check canceled"));
		}, { once: true });
	});
}

/**
 * Loads an agent's page in a throwaway hidden view, the way readers will see
 * it, and returns a screenshot, the content height, and console output. The
 * view never joins the user's Browser panel: in-memory partition, sandboxed,
 * no permissions, no popups, no navigation away. CDP captures it while it is
 * offscreen.
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
	const view = new deps.WebContentsView({
		webPreferences: {
			contextIsolation: true,
			nodeIntegration: false,
			sandbox: true,
			backgroundThrottling: false,
			// No "persist:" prefix: Electron keeps this partition in memory only.
			partition: `ao-render-check-${randomUUID()}`,
		},
	});
	const contents = view.webContents;
	const consoleMessages: RenderCheckMessage[] = [];
	contents.session.setPermissionRequestHandler((_contents, _permission, decide) => decide(false));
	contents.session.setPermissionCheckHandler(() => false);
	contents.setWindowOpenHandler(() => ({ action: "deny" }));
	contents.on("will-navigate", (event) => event.preventDefault());
	contents.on("console-message", (_event, level, message) => {
		if (consoleMessages.length >= MAX_MESSAGES) return;
		consoleMessages.push({ level: LEVELS[level] ?? "log", text: message.slice(0, MAX_MESSAGE_CHARS) });
	});
	view.setBounds({ x: -10_000, y: -10_000, width, height: 800 });
	deps.window.contentView.addChildView(view);
	try {
		await Promise.race([
			contents.loadURL(url),
			wait(LOAD_TIMEOUT_MS, signal).then(() => {
				throw renderCheckError("BROWSER_COMMAND_FAILED", `render check page did not load within ${LOAD_TIMEOUT_MS} ms`);
			}),
		]);
		await wait(SETTLE_MS, signal);
		const contentHeight = Number(
			await contents.executeJavaScript(
				"Math.ceil(Math.max(document.documentElement.scrollHeight, document.documentElement.getBoundingClientRect().height))",
			),
		);
		const height = Math.min(Math.max(contentHeight, 1), MAX_CAPTURE_HEIGHT);
		view.setBounds({ x: -10_000, y: -10_000, width, height });
		contents.debugger.attach("1.3");
		try {
			const shot = (await contents.debugger.sendCommand("Page.captureScreenshot", {
				format: "png",
				clip: { x: 0, y: 0, width, height, scale: 1 },
			})) as { data: string };
			return { data: shot.data, width, height, contentHeight, consoleMessages };
		} finally {
			contents.debugger.detach();
		}
	} finally {
		deps.window.contentView.removeChildView(view);
		contents.close();
	}
}
