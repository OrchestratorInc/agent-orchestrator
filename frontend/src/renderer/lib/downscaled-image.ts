import { useEffect, useState } from "react";

/**
 * Small copies of full-size images for preview cards. A card is about 640
 * pixels wide at most, yet a screenshot artifact can be several thousand: as
 * a plain <img> each card would hold the whole picture decoded, and decode it
 * again every time it scrolls back into view. Each image is fetched once,
 * decoded and scaled off the main thread (createImageBitmap), and kept as a
 * small WebP, which a card that comes back shows at once.
 */

/** Wide enough to stay sharp on a gallery card at 2x. */
export const DOWNSCALED_IMAGE_WIDTH = 640;
/** Small copies kept, the least recently used dropped first: a few megabytes. */
const MAX_KEPT = 200;

type Entry = { url: string } | { failed: true };

/**
 * Conversions running at once. Each fetches, decodes and encodes a whole image, so a fast
 * scroll past many new images queues them rather than starting them all.
 */
export const MAX_CONVERTING = 2;

/** One image being made, shared by every card that asks for it. */
type Job = { consumers: number; started: boolean; promise: Promise<Entry>; start: () => void };

const kept = new Map<string, Entry>();
const jobs = new Map<string, Job>();
const queue: Job[] = [];
let converting = 0;

function pump(): void {
	while (converting < MAX_CONVERTING && queue.length > 0) {
		const job = queue.shift() as Job;
		job.started = true;
		converting += 1;
		job.start();
	}
}

function keep(src: string, entry: Entry): Entry {
	kept.delete(src);
	kept.set(src, entry);
	while (kept.size > MAX_KEPT) {
		const [oldest, dropped] = kept.entries().next().value as [string, Entry];
		kept.delete(oldest);
		// An <img> that already shows this copy keeps it; only new loads need the URL.
		if ("url" in dropped) URL.revokeObjectURL(dropped.url);
	}
	return entry;
}

/**
 * The URL a version of a file is fetched from, which is also its key here. A local artifact's
 * URL stays the same when an agent overwrites the file, so its version (the time it changed)
 * names the copy: an overwritten image gets a new one, never the old picture.
 */
function versionedSrc(src: string, version: string): string {
	return `${src}${src.includes("?") ? "&" : "?"}v=${encodeURIComponent(version)}`;
}

async function downscale(src: string): Promise<Entry> {
	try {
		// The version is in the URL; no-store also keeps a stale response of it out.
		const response = await fetch(src, { cache: "no-store" });
		if (!response.ok) return { failed: true };
		const blob = await response.blob();
		// One decode, scaled as it decodes; the height keeps the picture's proportions.
		const bitmap = await createImageBitmap(blob, { resizeWidth: DOWNSCALED_IMAGE_WIDTH, resizeQuality: "medium" });
		const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
		canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
		bitmap.close();
		const small = await canvas.convertToBlob({ type: "image/webp", quality: 0.85 });
		return { url: URL.createObjectURL(small) };
	} catch {
		return { failed: true };
	}
}

/** The kept copy of this version of `src`, or undefined while it is being made. */
export function downscaledImage(src: string, version: string): Entry | undefined {
	const key = versionedSrc(src, version);
	const entry = kept.get(key);
	if (entry) keep(key, entry);
	return entry;
}

/**
 * Makes the copy of this version of `src` once, however many cards ask at the same time. A
 * card that stops waiting aborts `signal`; an image still queued when no card waits for it any
 * more is dropped, and made afresh if asked for again.
 */
export function requestDownscaledImage(src: string, version: string, signal?: AbortSignal): Promise<Entry> {
	const key = versionedSrc(src, version);
	const entry = kept.get(key);
	if (entry) return Promise.resolve(entry);
	if (signal?.aborted) return Promise.resolve({ failed: true });
	let job = jobs.get(key);
	if (!job) {
		let finish!: (entry: Entry) => void;
		const promise = new Promise<Entry>((resolve) => { finish = resolve; });
		job = {
			consumers: 0,
			started: false,
			promise,
			start: () => void downscale(key).then((made) => {
				jobs.delete(key);
				converting -= 1;
				finish(keep(key, made));
				pump();
			}),
		};
		jobs.set(key, job);
		queue.push(job);
	}
	const waiting = job;
	waiting.consumers += 1;
	signal?.addEventListener("abort", () => {
		waiting.consumers -= 1;
		if (waiting.consumers > 0 || waiting.started) return;
		queue.splice(queue.indexOf(waiting), 1);
		jobs.delete(key);
	}, { once: true });
	pump();
	return waiting.promise;
}

/**
 * A small copy of the image at `src`, as of `version` (when the file last
 * changed), for a card: its URL once made, "failed" when it cannot be made
 * (the card then shows the original), undefined while it is being made. Pass
 * undefined to make none.
 */
export function useDownscaledImage(src: string | undefined, version: string): string | "failed" | undefined {
	const read = (value: string | undefined) => {
		const entry = value ? downscaledImage(value, version) : undefined;
		return entry ? ("url" in entry ? entry.url : "failed") : undefined;
	};
	const key = src ? versionedSrc(src, version) : undefined;
	const [state, setState] = useState(() => ({ key, url: read(src) }));
	const current = state.key === key ? state.url : read(src);
	useEffect(() => {
		if (!src || current !== undefined) return;
		const controller = new AbortController();
		void requestDownscaledImage(src, version, controller.signal).then((entry) => {
			if (!controller.signal.aborted) setState({ key: versionedSrc(src, version), url: "url" in entry ? entry.url : "failed" });
		});
		// A card that scrolls away before its image is made stops waiting for it.
		return () => controller.abort();
	}, [src, version, current]);
	return current;
}
