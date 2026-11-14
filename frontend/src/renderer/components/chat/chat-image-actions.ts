/**
 * Copy and save for a chat image's resolved URL.
 *
 * Loopback images on the origins connect-src allows (http 127.0.0.1 and
 * *.localhost: the daemon and remote-host proxies) load in CORS mode, so the
 * renderer reads them itself: the remote proxy rejects a main-process fetch
 * because it carries Sec-Fetch-Site without Origin. Other http(s) images are
 * read by the main process (see `main/chat-image-ipc.ts`); inline `data:` and
 * `blob:` sources are read in place.
 */

import { MAX_CHAT_IMAGE_BYTES } from "../../../shared/chat-image";
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
	const loopback = resolved.protocol === "http:" && (resolved.hostname === "127.0.0.1" || resolved.hostname.endsWith(".localhost"));
	if ((resolved.protocol === "http:" || resolved.protocol === "https:") && !loopback) {
		return aoBridge.chatImage.fetch(resolved.toString());
	}
	const response = await fetch(resolved);
	if (!response.ok) throw new Error(`Image request failed (${response.status})`);
	if (loopback && Number(response.headers.get("content-length")) > MAX_CHAT_IMAGE_BYTES) throw new Error("Image is too large");
	const blob = await response.blob();
	const mimeType = blob.type.split(";", 1)[0]?.trim().toLowerCase() ?? "";
	if (loopback && !mimeType.startsWith("image/")) throw new Error("Not an image");
	if (loopback && blob.size > MAX_CHAT_IMAGE_BYTES) throw new Error("Image is too large");
	return { bytes: new Uint8Array(await blob.arrayBuffer()), mimeType };
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
