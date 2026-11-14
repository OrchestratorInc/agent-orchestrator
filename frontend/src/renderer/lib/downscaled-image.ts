import { useEffect, useState } from "react";

export const DOWNSCALED_IMAGE_WIDTH = 640;
const MAX_KEPT = 200;

type Entry = { url: string } | { failed: true };

export const MAX_CONVERTING = 2;

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
		if ("url" in dropped) URL.revokeObjectURL(dropped.url);
	}
	return entry;
}

function versionedSrc(src: string, version: string): string {
	return `${src}${src.includes("?") ? "&" : "?"}v=${encodeURIComponent(version)}`;
}

async function downscale(src: string): Promise<Entry> {
	try {
		const response = await fetch(src, { cache: "no-store" });
		if (!response.ok) return { failed: true };
		const blob = await response.blob();
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

export function downscaledImage(src: string, version: string): Entry | undefined {
	const key = versionedSrc(src, version);
	const entry = kept.get(key);
	if (entry) keep(key, entry);
	return entry;
}

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
		return () => controller.abort();
	}, [src, version, current]);
	return current;
}
