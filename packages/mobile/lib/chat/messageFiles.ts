import { createContext } from "react";
import type { MessageFile } from "./types";

/**
 * Where a message's uploaded files come from: a local file:// copy of one. A
 * cloud session's screen provides it; without one a file shows as its name.
 */
export const MessageFileSourceContext = createContext<((file: MessageFile) => Promise<string>) | null>(null);

/**
 * What a source rejects with when the file is gone for good, so the message
 * says so instead of offering a retry that can only fail: a cloud session's
 * attachments are deleted with it when it is archived, and come back without
 * them when it is restored.
 */
export class MessageFileGoneError extends Error {
	constructor(name: string) {
		super(`${name} is no longer available.`);
		this.name = "MessageFileGoneError";
	}
}

/** Image types React Native draws on both platforms (not SVG). */
const DRAWN_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/heic", "image/heif", "image/bmp"]);

export function isDrawnImageFile(file: MessageFile): boolean {
	return DRAWN_IMAGE_TYPES.has(file.mimeType.toLowerCase());
}
