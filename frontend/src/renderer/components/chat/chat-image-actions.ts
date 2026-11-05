/**
 * Copy and save for a chat image's resolved URL.
 *
 * http(s) images are read by the main process (see `main/chat-image-ipc.ts`)
 * because they are cross-origin to the renderer; inline `data:` and `blob:`
 * sources are already the renderer's own and are read in place.
 */

import { aoBridge } from "../../lib/bridge";

const EXTENSION_BY_MIME: Record<string, string> = {
	"image/png": "png",
	"image/jpeg": "jpg",
	"image/gif": "gif",
	"image/webp": "webp",
	"image/bmp": "bmp",
	"image/svg+xml": "svg",
	"image/avif": "avif",
};

type ImageBytes = { bytes: Uint8Array; mimeType: string };

async function loadChatImage(url: string): Promise<ImageBytes> {
	const resolved = new URL(url, window.location.href);
	if (resolved.protocol === "http:" || resolved.protocol === "https:") {
		return aoBridge.chatImage.fetch(resolved.toString());
	}
	const response = await fetch(resolved);
	if (!response.ok) throw new Error(`Image request failed (${response.status})`);
	const blob = await response.blob();
	return { bytes: new Uint8Array(await blob.arrayBuffer()), mimeType: blob.type };
}

/** Re-encode as PNG, the one format every clipboard target accepts. */
async function toPng({ bytes, mimeType }: ImageBytes): Promise<Uint8Array> {
	if (mimeType === "image/png") return bytes;
	const objectUrl = URL.createObjectURL(new Blob([new Uint8Array(bytes)], { type: mimeType }));
	try {
		const image = new Image();
		image.src = objectUrl;
		await image.decode();
		const canvas = document.createElement("canvas");
		canvas.width = image.naturalWidth;
		canvas.height = image.naturalHeight;
		const context = canvas.getContext("2d");
		if (!context) throw new Error("Canvas is unavailable");
		context.drawImage(image, 0, 0);
		const png = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
		if (!png) throw new Error("Could not encode image");
		return new Uint8Array(await png.arrayBuffer());
	} finally {
		URL.revokeObjectURL(objectUrl);
	}
}

/**
 * The name to offer when saving: the URL's own file name when it has one,
 * otherwise the image's label, with an extension that matches its bytes.
 */
export function chatImageSaveName(url: string, label: string, mimeType: string): string {
	const extension = EXTENSION_BY_MIME[mimeType] ?? "png";
	let fromUrl = "";
	try {
		const { protocol, pathname } = new URL(url, "http://localhost/");
		if (protocol === "http:" || protocol === "https:") fromUrl = decodeURIComponent(pathname.slice(pathname.lastIndexOf("/") + 1));
	} catch {
		// An unparsable URL falls back to the label.
	}
	if (/\.[A-Za-z0-9]{2,5}$/.test(fromUrl)) return fromUrl;
	const stem = (fromUrl || label).replace(/[\\/:*?"<>|\u0000-\u001f]/g, "").trim() || "image";
	return `${stem}.${extension}`;
}

export async function copyChatImage(url: string): Promise<void> {
	await aoBridge.clipboard.writeImage(await toPng(await loadChatImage(url)));
}

/** Resolves false when the user cancels the save dialog. */
export async function saveChatImage(url: string, label: string): Promise<boolean> {
	const image = await loadChatImage(url);
	return aoBridge.chatImage.save({ name: chatImageSaveName(url, label, image.mimeType), bytes: image.bytes });
}
