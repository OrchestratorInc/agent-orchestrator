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

const kept = new Map<string, Entry>();
const pending = new Map<string, Promise<Entry>>();

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

async function downscale(src: string): Promise<Entry> {
	try {
		const response = await fetch(src);
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

/** The kept copy of `src`, or undefined while it is being made. */
export function downscaledImage(src: string): Entry | undefined {
	const entry = kept.get(src);
	if (entry) keep(src, entry);
	return entry;
}

/** Makes the copy of `src` once, however many cards ask at the same time. */
export function requestDownscaledImage(src: string): Promise<Entry> {
	const entry = kept.get(src);
	if (entry) return Promise.resolve(entry);
	let request = pending.get(src);
	if (!request) {
		request = downscale(src).then((made) => {
			pending.delete(src);
			return keep(src, made);
		});
		pending.set(src, request);
	}
	return request;
}

/**
 * A small copy of the image at `src` for a card: its URL once made, "failed"
 * when it cannot be made (the card then shows the original), undefined while
 * it is being made. Pass undefined to make none.
 */
export function useDownscaledImage(src: string | undefined): string | "failed" | undefined {
	const read = (value: string | undefined) => {
		const entry = value ? downscaledImage(value) : undefined;
		return entry ? ("url" in entry ? entry.url : "failed") : undefined;
	};
	const [state, setState] = useState(() => ({ src, url: read(src) }));
	const current = state.src === src ? state.url : read(src);
	useEffect(() => {
		if (!src || current !== undefined) return;
		let active = true;
		void requestDownscaledImage(src).then((entry) => {
			if (active) setState({ src, url: "url" in entry ? entry.url : "failed" });
		});
		return () => {
			active = false;
		};
	}, [src, current]);
	return current;
}
