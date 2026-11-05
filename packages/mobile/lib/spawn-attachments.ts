import { sniffImageMimeType } from "./imageMime";

export type SpawnAttachment = {
	name: string;
	mimeType: string;
	data: string;
	bytes: number;
	/** The picked local file, kept for images so the sheet can show a thumbnail. */
	previewUri?: string;
};

export type PickedSpawnAttachment = Omit<SpawnAttachment, "data" | "bytes"> & {
	bytes?: number;
	readData: () => Promise<string>;
};

export const MAX_SPAWN_ATTACHMENTS = 8;
const MAX_FILE_BYTES = 50 * 1024 * 1024;
const MAX_TOTAL_BYTES = 100 * 1024 * 1024;

export function appendSpawnAttachments(
	existing: readonly SpawnAttachment[],
	incoming: readonly SpawnAttachment[],
): { attachments: SpawnAttachment[]; error?: string } {
	const attachments = [...existing];
	let totalBytes = attachments.reduce((sum, item) => sum + item.bytes, 0);
	let error: string | undefined;

	for (const item of incoming) {
		if (attachments.length >= MAX_SPAWN_ATTACHMENTS) {
			error ??= `You can attach up to ${MAX_SPAWN_ATTACHMENTS} files.`;
			break;
		}
		if (item.mimeType.trim().toLowerCase() === "image/svg+xml") {
			error ??= `${item.name} is not a supported attachment type.`;
			continue;
		}
		if (item.bytes > MAX_FILE_BYTES) {
			error ??= `${item.name} must be under 50 MB.`;
			continue;
		}
		if (totalBytes + item.bytes > MAX_TOTAL_BYTES) {
			error ??= "Attachments must total under 100 MB.";
			continue;
		}
		attachments.push(item);
		totalBytes += item.bytes;
	}

	return { attachments, error };
}

export async function readSpawnAttachments(
	existing: readonly SpawnAttachment[],
	incoming: readonly PickedSpawnAttachment[],
): Promise<{ attachments: SpawnAttachment[]; error?: string }> {
	let attachments = [...existing];
	let error: string | undefined;

	for (const item of incoming) {
		if (item.bytes === 0) {
			error ??= `${item.name} is empty.`;
			continue;
		}
		if (typeof item.bytes !== "number" || !Number.isSafeInteger(item.bytes) || item.bytes < 0) {
			error ??= `Could not determine the size of ${item.name}.`;
			continue;
		}
		const candidate: SpawnAttachment = { name: item.name, mimeType: item.mimeType, bytes: item.bytes, data: "", ...(item.previewUri ? { previewUri: item.previewUri } : {}) };
		const next = appendSpawnAttachments(attachments, [candidate]);
		if (next.error) {
			error ??= next.error;
			continue;
		}
		try {
			candidate.data = await item.readData();
		} catch (cause) {
			error ??= cause instanceof Error ? cause.message : `Could not read ${item.name}.`;
			continue;
		}
		if (!candidate.data.trim()) {
			error ??= `${item.name} is empty.`;
			continue;
		}
		// The daemon names the saved file from this type, so it has to describe
		// the bytes being sent, not the picker's account of the original.
		if (candidate.mimeType.trim().toLowerCase().startsWith("image/")) {
			candidate.mimeType = sniffImageMimeType(candidate.data) ?? candidate.mimeType;
		}
		attachments = next.attachments;
	}

	return { attachments, error };
}

/** The exported photo file the picker hands back. */
export type PickedPhotoFile = { size: number; base64(): Promise<string> };

/**
 * A photo-library pick, described by the file actually exported. Android
 * compresses anything but PNG to JPEG when `quality < 1`, yet reports the
 * source's type and size, so a WebP, GIF or HEIC would otherwise be sent as
 * JPEG bytes under the source's label and size-checked against the original.
 * The type is corrected from the bytes in `readSpawnAttachments`.
 */
export function photoPickerAttachment(
	asset: { uri: string; fileName?: string | null; mimeType?: string },
	index: number,
	file: PickedPhotoFile,
): PickedSpawnAttachment {
	const mimeType = asset.mimeType || "image/jpeg";
	return {
		name: asset.fileName || `photo-${index + 1}.${mimeType.split("/")[1] || "jpg"}`,
		mimeType,
		bytes: file.size,
		previewUri: asset.uri,
		readData: () => file.base64(),
	};
}

/** Images the sheet can draw: a thumbnail in the row, full screen on tap. */
export function spawnAttachmentPreview(item: SpawnAttachment): string | undefined {
	return item.previewUri && item.mimeType.trim().toLowerCase().startsWith("image/") ? item.previewUri : undefined;
}
