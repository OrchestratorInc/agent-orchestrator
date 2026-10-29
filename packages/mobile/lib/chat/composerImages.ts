import { sniffImageMimeType } from "../imageMime";
import { updateComposerAttachments } from "./composerAttachments";
import type { ChatImage } from "./types";

export const MAX_ATTACHMENTS = 8;
export const MAX_IMAGE_BYTES = 10 * 1024 * 1024;
export const MAX_IMAGE_BYTES_TOTAL = 25 * 1024 * 1024;
const SUPPORTED_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/bmp"]);

/** Which batch limit a native paste stopped at before reading the rest. */
export type PasteOverflow = "count" | "size";

const OVERFLOW_NOTICES: Record<PasteOverflow, string> = {
	count: `You can attach up to ${MAX_ATTACHMENTS} items.`,
	size: "Images must total under 25 MB.",
};

/** The picker's asset shape; pasted images are mapped onto it so both share one gate. */
export type ComposerImageAsset = {
	uri: string;
	assetId?: string | null;
	fileName?: string | null;
	mimeType?: string | null;
	fileSize?: number | null;
	base64?: string | null;
};
export type ImageAttachment = { id: string; kind: "image"; name: string; bytes: number; image: ChatImage };

/**
 * Admits picked or pasted images against the composer's limits, in order, and
 * says why any were left out. `current` is what is already attached.
 */
export function acceptImageAssets(current: readonly { kind: string; bytes?: number }[], assets: readonly ComposerImageAsset[], now = Date.now()): { added: ImageAttachment[]; error?: string } {
	const errors = new Set<string>();
	const candidates = assets.flatMap((asset): ImageAttachment[] => {
		const bytes = asset.fileSize ?? Math.floor((asset.base64?.length ?? 0) * 0.75);
		// Oversized pastes arrive without data, so the size check comes first.
		if (bytes > MAX_IMAGE_BYTES) { errors.add("Each image must be under 10 MB."); return []; }
		if (!asset.base64) { errors.add("Some images couldn't be read and were skipped."); return []; }
		// Typed by the bytes, not the label: pickers report the source file's type
		// but export re-encoded JPEG, and agents reject a mismatched image.
		const mimeType = (sniffImageMimeType(asset.base64) ?? (asset.mimeType || "image/jpeg")).toLowerCase();
		if (!SUPPORTED_IMAGE_TYPES.has(mimeType)) { errors.add("Only PNG, JPEG, GIF, WebP, and BMP images are supported."); return []; }
		return [{ id: `${asset.assetId || asset.uri}-${now}`, kind: "image", name: asset.fileName || "Image", bytes, image: { mimeType, data: asset.base64 } }];
	});
	const added: ImageAttachment[] = [];
	let count = current.length;
	let imageBytes = current.reduce((sum, item) => sum + (item.kind === "image" ? item.bytes ?? 0 : 0), 0);
	for (const item of candidates) {
		if (count >= MAX_ATTACHMENTS) { errors.add(`You can attach up to ${MAX_ATTACHMENTS} items.`); break; }
		if (imageBytes + item.bytes > MAX_IMAGE_BYTES_TOTAL) { errors.add("Images must total under 25 MB."); break; }
		added.push(item);
		count += 1;
		imageBytes += item.bytes;
	}
	return { added, error: errors.size ? [...errors].join(" ") : undefined };
}

/**
 * Adds picked or pasted images to a session's stored attachments and records why
 * any were left out, including those a native paste stopped before reading
 * (`overflow`). Reads and writes the store, never a render's copy, so a pick that
 * outlives its composer still lands. Returns whether any image was added.
 */
export function attachComposerImages(key: string, assets: readonly ComposerImageAsset[], overflow?: PasteOverflow): boolean {
	let added = 0;
	updateComposerAttachments(key, (current) => {
		const accepted = acceptImageAssets(current.items, assets);
		added = accepted.added.length;
		const skipped = overflow ? OVERFLOW_NOTICES[overflow] : undefined;
		const notice = skipped && !accepted.error?.includes(skipped) ? [accepted.error, skipped].filter(Boolean).join(" ") : accepted.error;
		return { items: [...current.items, ...accepted.added], notice };
	});
	return added > 0;
}
