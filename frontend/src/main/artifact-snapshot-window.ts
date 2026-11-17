/**
 * Renders one artifact preview in a hidden, offscreen window and returns it as a WebP.
 *
 * The window loads the app's own snapshot page (artifact-snapshot.html), which
 * draws the same preview a card draws, so every kind of artifact looks the same
 * cached as live. Main waits until the page says the preview is complete, takes
 * the frame the window paints, and has the page encode it: Electron's images
 * encode PNG and JPEG only, and a WebP of a card is about half a JPEG's size.
 */

import type { BrowserWindow, NativeImage } from "electron";
import {
	ARTIFACT_SNAPSHOT_HEIGHT,
	ARTIFACT_SNAPSHOT_QUALITY,
	ARTIFACT_SNAPSHOT_WIDTH,
	type ArtifactSnapshotJob,
} from "../shared/artifact-snapshot";
import { SnapshotCancelledError, type SnapshotRenderer, SnapshotUnavailableError } from "./artifact-snapshots";

/** The page reports one of these while it draws the preview. */
type PageState = "loading" | "ready" | "unavailable" | "error";

/** A page that has not finished by now is left uncached; some load slowly from the cloud. */
const SNAPSHOT_DEADLINE_MS = 20_000;
/** The whole render, from the page starting to the encoded bytes, may take this much longer than the preview's own deadline. */
const SNAPSHOT_HARD_DEADLINE_MS = SNAPSHOT_DEADLINE_MS + 15_000;
/** The snapshot page has this long to load before the window is given up on. */
const PAGE_LOAD_DEADLINE_MS = 15_000;
const POLL_MS = 100;
/** A frame painted after the page settles: the compositor lags the DOM by a frame or two. */
const PAINT_ATTEMPTS = 12;

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

type WindowDeps = {
	BrowserWindow: typeof BrowserWindow;
	/** The snapshot page's URL in the running renderer. */
	pageUrl: () => string;
	preload: string;
	/** Whether the app's own window is open. The hidden window is only ever opened while it is: on macOS the app outlives its window, and a hidden window left open then breaks reopening it. */
	shellOpen: () => boolean;
};

function nextPaint(window: BrowserWindow): Promise<NativeImage> {
	const contents = window.webContents;
	const painted = new Promise<NativeImage>((resolve) => contents.once("paint", (_event, _dirty, image) => resolve(image)));
	contents.invalidate();
	return painted;
}

/** The first painted frame that shows the whole preview, at any display scale. */
async function paintedFrame(window: BrowserWindow): Promise<NativeImage> {
	let image = await nextPaint(window);
	for (let attempt = 0; attempt < PAINT_ATTEMPTS; attempt++) {
		const { width, height } = image.getSize();
		const scale = width / ARTIFACT_SNAPSHOT_WIDTH;
		if (scale > 0 && Math.abs(height - ARTIFACT_SNAPSHOT_HEIGHT * scale) <= Math.ceil(scale)) return image;
		await sleep(16);
		image = await nextPaint(window);
	}
	throw new Error("the snapshot window never painted the whole preview");
}

/** A preview that failed to load, as opposed to one with nothing to show: asked again after the retry delay. */
class PreviewLoadError extends Error {}

/** The window stays warm between artifacts, so a screen of cards does not start a renderer process for each. */
const WINDOW_IDLE_MS = 30_000;
/** The window only paints when asked to (invalidate), so it never spends frames on its own. */
const IDLE_FRAME_RATE = 4;

/**
 * The renderer, and `close` for the window it keeps open. The window is a BaseWindow to
 * Electron, so it counts for `getAllWindows()` and `window-all-closed`: main closes it
 * with the shell window and on quit.
 */
export function createArtifactSnapshotRenderer(deps: WindowDeps): { render: SnapshotRenderer; close: () => void } {
	let current: Promise<BrowserWindow> | undefined;
	let idle: ReturnType<typeof setTimeout> | undefined;

	function open(): Promise<BrowserWindow> {
		if (current) return current;
		const opening: Promise<BrowserWindow> = (async () => {
			const window = new deps.BrowserWindow({
				show: false,
				width: ARTIFACT_SNAPSHOT_WIDTH,
				height: ARTIFACT_SNAPSHOT_HEIGHT,
				useContentSize: true,
				webPreferences: {
					offscreen: true,
					sandbox: true,
					contextIsolation: true,
					nodeIntegration: false,
					backgroundThrottling: false,
					preload: deps.preload,
				},
			});
			const contents = window.webContents;
			// The page is the app's own; it may not open a window or navigate away. The window shares the
			// app's session on purpose (the artifacts need its daemon and cloud access), so it sets no
			// session-wide handlers: a permission handler here would apply to the whole app.
			contents.setWindowOpenHandler(() => ({ action: "deny" }));
			contents.on("will-navigate", (event) => event.preventDefault());
			contents.setAudioMuted(true);
			contents.setFrameRate(IDLE_FRAME_RATE);
			// Only the window that is current clears it: a stale one closing later must not unset its successor.
			window.once("closed", () => {
				if (current === opening) current = undefined;
			});
			try {
				let timer: ReturnType<typeof setTimeout> | undefined;
				const stalled = new Promise<never>((_, reject) => {
					timer = setTimeout(() => reject(new Error("the snapshot page did not load")), PAGE_LOAD_DEADLINE_MS);
				});
				await Promise.race([contents.loadURL(deps.pageUrl()), stalled]).finally(() => clearTimeout(timer));
			} catch (error) {
				window.destroy();
				throw error;
			}
			return window;
		})();
		current = opening;
		return opening;
	}

	/** Closes one window, by default the current one. A render passes the window it opened, so one that timed out cannot close its successor's. */
	function close(target: Promise<BrowserWindow> | undefined = current): void {
		if (current === target) current = undefined;
		void target?.then((opened) => {
			if (!opened.isDestroyed()) opened.destroy();
		}, () => undefined);
	}

	const renderOne = async (job: ArtifactSnapshotJob, owned: Promise<BrowserWindow>): Promise<Buffer> => {
		clearTimeout(idle);
		const window = await owned;
		const contents = window.webContents;
		const read = <T>(code: string) => contents.executeJavaScript(code) as Promise<T>;
		const deadline = Date.now() + SNAPSHOT_DEADLINE_MS;
		try {
			contents.startPainting();
			await read(`window.__aoSnapshot.start(${JSON.stringify(job)})`);
			let state: PageState = "loading";
			while (state === "loading" && Date.now() < deadline) {
				await sleep(POLL_MS);
				state = await read<PageState>("window.__aoSnapshot.state()");
			}
			if (state === "unavailable") throw new SnapshotUnavailableError("the artifact has no preview");
			if (state === "error") throw new PreviewLoadError("the preview could not be loaded");
			if (state !== "ready") throw new Error("the preview did not finish loading");
			const frame = await paintedFrame(window);
			const png = frame.getSize().width === ARTIFACT_SNAPSHOT_WIDTH
				? frame.toPNG()
				: frame.resize({ width: ARTIFACT_SNAPSHOT_WIDTH, height: ARTIFACT_SNAPSHOT_HEIGHT, quality: "best" }).toPNG();
			const encoded = await read<string>(`window.__aoSnapshot.encode(${JSON.stringify(png.toString("base64"))}, ${ARTIFACT_SNAPSHOT_QUALITY})`);
			const bytes = Buffer.from(encoded, "base64");
			if (bytes.length === 0) throw new Error("the preview could not be encoded");
			return bytes;
		} catch (error) {
			// A page that failed or hung may be stuck; the next artifact gets a fresh one. A file with
			// nothing to draw left the page healthy.
			if (!(error instanceof SnapshotUnavailableError || error instanceof PreviewLoadError)) close(owned);
			throw error;
		} finally {
			if (!window.isDestroyed()) {
				void read("window.__aoSnapshot.reset()").catch(() => undefined);
				contents.stopPainting();
			}
			// Only the window in use keeps the idle timer: a stale render finishing late must not arm or clear its successor's.
			if (current === owned) {
				clearTimeout(idle);
				idle = setTimeout(() => close(owned), WINDOW_IDLE_MS);
			}
		}
	};

	/** One end-to-end bound on a render: a page that never starts, paints or encodes must not hold up every later one. */
	const render: SnapshotRenderer = async (job) => {
		if (!deps.shellOpen()) throw new SnapshotCancelledError("the app window is closed");
		let timer: ReturnType<typeof setTimeout> | undefined;
		const owned = open();
		const work = renderOne(job, owned);
		const stalled = new Promise<never>((_, reject) => {
			timer = setTimeout(() => {
				close(owned);
				reject(new Error("the snapshot window stalled"));
			}, SNAPSHOT_HARD_DEADLINE_MS);
		});
		try {
			return await Promise.race([work, stalled]);
		} finally {
			clearTimeout(timer);
			// Once the window is closed under it, whatever the render was waiting on may reject later.
			work.catch(() => undefined);
		}
	};

	function closeNow(): void {
		clearTimeout(idle);
		close();
	}

	return { render, close: closeNow };
}
