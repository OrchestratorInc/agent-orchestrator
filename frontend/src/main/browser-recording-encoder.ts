import { AoError } from "../../../packages/shared/telemetry/ao-error";
import { open, rm } from "node:fs/promises";
import type { WebContents } from "electron";
import type { BrowserRecordingEncoder, CreateBrowserRecordingEncoder } from "./browser-recording";

// The recorder is a hidden offscreen page of the app's own: it draws each page
// frame onto a canvas and MediaRecorder encodes the canvas stream to WebM. It
// has no preload, no network and no navigation; main hands it frames and takes
// the encoded bytes back with executeJavaScript.
const RECORDER_HTML = `<!doctype html><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; img-src blob:">
<canvas id="c"></canvas>
<script>
"use strict";
const canvas = document.getElementById("c");
const context = canvas.getContext("2d", { alpha: false });
let recorder, track, chunks = [];
const toBase64 = async (blob) => {
	const bytes = new Uint8Array(await blob.arrayBuffer());
	let text = "";
	for (let i = 0; i < bytes.length; i += 0x8000) text += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
	return btoa(text);
};
const take = () => (chunks.length ? toBase64(new Blob(chunks.splice(0))) : Promise.resolve(""));
window.aoRecorder = {
	async frame(encoded) {
		const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0));
		const image = await createImageBitmap(new Blob([bytes], { type: "image/jpeg" }));
		if (!recorder) {
			// VP8/VP9 want even dimensions. The first frame sets the video's size.
			canvas.width = image.width + (image.width % 2);
			canvas.height = image.height + (image.height % 2);
			const stream = canvas.captureStream(0);
			track = stream.getVideoTracks()[0];
			const mimeType = MediaRecorder.isTypeSupported("video/webm;codecs=vp9") ? "video/webm;codecs=vp9" : "video/webm;codecs=vp8";
			recorder = new MediaRecorder(stream, { mimeType, videoBitsPerSecond: 2500000 });
			recorder.ondataavailable = (event) => { if (event.data.size) chunks.push(event.data); };
			recorder.start(1000);
		}
		// A later frame of another size (a tab switch) is fitted inside the first.
		const scale = Math.min(canvas.width / image.width, canvas.height / image.height);
		const width = Math.round(image.width * scale);
		const height = Math.round(image.height * scale);
		context.fillStyle = "#000";
		context.fillRect(0, 0, canvas.width, canvas.height);
		context.drawImage(image, Math.round((canvas.width - width) / 2), Math.round((canvas.height - height) / 2), width, height);
		image.close();
		track.requestFrame();
		return true;
	},
	take,
	stop() {
		if (!recorder) return Promise.resolve("");
		return new Promise((resolve) => {
			recorder.onstop = () => resolve(take());
			// Hold the last picture until the moment the recording stopped.
			track.requestFrame();
			recorder.stop();
		});
	},
};
</script>`;

// Frames waiting for the recorder beyond this are dropped rather than queued.
const MAX_QUEUED_FRAMES = 6;
const FLUSH_INTERVAL_MS = 1000;

type RecorderView = { webContents: WebContents };

// A view in no window is collected once nothing references it, and its page
// goes with it. Hold each recorder's view until its recording closes.
const liveRecorders = new Set<RecorderView>();

export function createElectronRecordingEncoder(
	WebContentsView: new (options: { webPreferences: Electron.WebPreferences }) => RecorderView,
): CreateBrowserRecordingEncoder {
	return async (file, onWritten) => {
		const handle = await open(file, "wx", 0o600);
		const view = new WebContentsView({
			webPreferences: {
				offscreen: true,
				sandbox: true,
				contextIsolation: true,
				nodeIntegration: false,
				backgroundThrottling: false,
				// Its own in-memory session: nothing it does touches the app's.
				partition: "ao-browser-recorder",
				webgl: false,
				spellcheck: false,
			},
		});
		liveRecorders.add(view);
		const contents = view.webContents;
		contents.setWindowOpenHandler(() => ({ action: "deny" }));
		contents.on("will-navigate", (event) => event.preventDefault());
		contents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
		let written = 0;
		let queued = 0;
		let closed = false;
		let flushTimer: ReturnType<typeof setInterval> | undefined;
		let work: Promise<unknown> = Promise.resolve();
		// The first chunk that failed to reach the file: the video has a gap.
		let writeError: unknown;
		const close = async () => {
			if (closed) return;
			closed = true;
			clearInterval(flushTimer);
			try {
				view.webContents.close();
			} catch {
				// Already gone with the app.
			}
			liveRecorders.delete(view);
			await handle.close().catch(() => undefined);
		};
		const append = async (encoded: unknown) => {
			if (typeof encoded !== "string" || !encoded) return;
			const bytes = Buffer.from(encoded, "base64");
			await handle.write(bytes);
			written += bytes.length;
			onWritten(written);
		};
		const enqueue = <T>(task: () => Promise<T>): Promise<T> => {
			const next = work.then(task, task);
			work = next.catch(() => undefined);
			return next;
		};
		try {
			await contents.loadURL(`data:text/html;base64,${Buffer.from(RECORDER_HTML).toString("base64")}`);
			const supported = await contents.executeJavaScript("typeof MediaRecorder === 'function'");
			if (supported !== true) throw new AoError("BROWSER_RECORDING_UNAVAILABLE", "This app cannot encode video");
		} catch (error) {
			await close();
			await rm(file, { force: true });
			throw new AoError(
				"BROWSER_RECORDING_UNAVAILABLE",
				error instanceof Error ? error.message : "Recorder failed to start",
				{ cause: error },
			);
		}
		flushTimer = setInterval(() => {
			if (writeError) return;
			void enqueue(async () => append(await contents.executeJavaScript("aoRecorder.take()"))).catch((error) => {
				writeError ??= error;
			});
		}, FLUSH_INTERVAL_MS);
		flushTimer.unref?.();

		const encoder: BrowserRecordingEncoder = {
			frame: (jpeg) => {
				if (closed || queued >= MAX_QUEUED_FRAMES) return;
				queued += 1;
				const encoded = jpeg.toString("base64");
				void enqueue(() => contents.executeJavaScript(`aoRecorder.frame(${JSON.stringify(encoded)})`))
					.catch(() => undefined)
					.finally(() => {
						queued -= 1;
					});
			},
			finish: async () => {
				try {
					await enqueue(async () => append(await contents.executeJavaScript("aoRecorder.stop()")));
					if (writeError) {
						throw new AoError(
							"BROWSER_RECORDING_FAILED",
							`The recording could not be written: ${writeError instanceof Error ? writeError.message : String(writeError)}`,
							{ cause: writeError },
						);
					}
				} finally {
					await close();
				}
			},
			abort: async () => {
				await close();
				await rm(file, { force: true });
			},
		};
		return encoder;
	};
}
