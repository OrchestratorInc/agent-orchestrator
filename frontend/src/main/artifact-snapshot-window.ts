import type { BrowserWindow, NativeImage } from "electron";
import {
	ARTIFACT_SNAPSHOT_HEIGHT,
	ARTIFACT_SNAPSHOT_QUALITY,
	ARTIFACT_SNAPSHOT_WIDTH,
	type ArtifactSnapshotJob,
} from "../shared/artifact-snapshot";
import { SnapshotCancelledError, type SnapshotRenderer, SnapshotUnavailableError } from "./artifact-snapshots";

type PageState = "loading" | "ready" | "unavailable" | "error";

const SNAPSHOT_DEADLINE_MS = 20_000;
const SNAPSHOT_HARD_DEADLINE_MS = SNAPSHOT_DEADLINE_MS + 15_000;
const PAGE_LOAD_DEADLINE_MS = 15_000;
const POLL_MS = 100;
const PAINT_ATTEMPTS = 12;

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

type WindowDeps = {
	BrowserWindow: typeof BrowserWindow;
	pageUrl: () => string;
	preload: string;
	shellOpen: () => boolean;
};

function nextPaint(window: BrowserWindow): Promise<NativeImage> {
	const contents = window.webContents;
	const painted = new Promise<NativeImage>((resolve) => contents.once("paint", (_event, _dirty, image) => resolve(image)));
	contents.invalidate();
	return painted;
}

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

class PreviewLoadError extends Error {}

const WINDOW_IDLE_MS = 30_000;
const IDLE_FRAME_RATE = 4;

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
			contents.setWindowOpenHandler(() => ({ action: "deny" }));
			contents.on("will-navigate", (event) => event.preventDefault());
			contents.setAudioMuted(true);
			contents.setFrameRate(IDLE_FRAME_RATE);
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
			if (!(error instanceof SnapshotUnavailableError || error instanceof PreviewLoadError)) close(owned);
			throw error;
		} finally {
			if (!window.isDestroyed()) {
				void read("window.__aoSnapshot.reset()").catch(() => undefined);
				contents.stopPainting();
			}
			if (current === owned) {
				clearTimeout(idle);
				idle = setTimeout(() => close(owned), WINDOW_IDLE_MS);
			}
		}
	};

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
			work.catch(() => undefined);
		}
	};

	function closeNow(): void {
		clearTimeout(idle);
		close();
	}

	return { render, close: closeNow };
}
