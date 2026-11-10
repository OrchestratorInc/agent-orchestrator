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
import type { SnapshotRenderer } from "./artifact-snapshots";

/** The page reports one of these while it draws the preview. */
type PageState = "loading" | "ready" | "unavailable";

/** A page that has not finished by now is left uncached; some load slowly from the cloud. */
const SNAPSHOT_DEADLINE_MS = 20_000;
const POLL_MS = 100;
/** A frame painted after the page settles: the compositor lags the DOM by a frame or two. */
const PAINT_ATTEMPTS = 12;

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

type WindowDeps = {
	BrowserWindow: typeof BrowserWindow;
	/** The snapshot page's URL in the running renderer. */
	pageUrl: () => string;
	preload: string;
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

export function createArtifactSnapshotRenderer(deps: WindowDeps): SnapshotRenderer {
	return async (job: ArtifactSnapshotJob) => {
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
		const read = <T>(code: string) => contents.executeJavaScript(code) as Promise<T>;
		const deadline = Date.now() + SNAPSHOT_DEADLINE_MS;
		try {
			// The page is the app's own; nothing in it may leave for another address or open a window.
			contents.setWindowOpenHandler(() => ({ action: "deny" }));
			contents.on("will-navigate", (event) => event.preventDefault());
			contents.session.setPermissionRequestHandler((_contents, _permission, decide) => decide(false));
			contents.setAudioMuted(true);
			await contents.loadURL(deps.pageUrl());
			await read(`window.__aoSnapshot.start(${JSON.stringify(job)})`);
			let state: PageState = "loading";
			while (state === "loading" && Date.now() < deadline) {
				await sleep(POLL_MS);
				state = await read<PageState>("window.__aoSnapshot.state()");
			}
			if (state !== "ready") throw new Error(state === "unavailable" ? "the artifact has no preview" : "the preview did not finish loading");
			const frame = await paintedFrame(window);
			const png = frame.getSize().width === ARTIFACT_SNAPSHOT_WIDTH
				? frame.toPNG()
				: frame.resize({ width: ARTIFACT_SNAPSHOT_WIDTH, height: ARTIFACT_SNAPSHOT_HEIGHT, quality: "best" }).toPNG();
			const encoded = await read<string>(`window.__aoSnapshot.encode(${JSON.stringify(png.toString("base64"))}, ${ARTIFACT_SNAPSHOT_QUALITY})`);
			const bytes = Buffer.from(encoded, "base64");
			if (bytes.length === 0) throw new Error("the preview could not be encoded");
			return bytes;
		} finally {
			if (!window.isDestroyed()) window.destroy();
		}
	};
}
