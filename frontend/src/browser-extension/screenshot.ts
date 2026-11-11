// Screenshots of the session's tab with chrome.tabs.captureVisibleTab, and
// optional numbered labels for interactive elements (`--annotate`).

import type { Annotation } from "./dom-actions";
import { BridgeError } from "./protocol";

export const MAX_SCREENSHOT_BYTES = 8 << 20;

/** Width and height from a PNG's IHDR chunk. */
export function pngSize(bytes: Uint8Array): { width: number; height: number } {
	const signature = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
	if (bytes.length < 24 || signature.some((byte, i) => bytes[i] !== byte)) {
		throw new BridgeError("SCREENSHOT_UNAVAILABLE", "Chrome returned an invalid PNG");
	}
	const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
	return { width: view.getUint32(16), height: view.getUint32(20) };
}

export function base64ToBytes(base64: string): Uint8Array {
	const binary = atob(base64);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
	return bytes;
}

export function bytesToBase64(bytes: Uint8Array): string {
	let binary = "";
	for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
	return btoa(binary);
}

/** Draw numbered boxes over a PNG (service worker: OffscreenCanvas). */
export async function annotatePng(png: Uint8Array, items: Annotation[], scale: number): Promise<Uint8Array> {
	const bitmap = await createImageBitmap(new Blob([new Uint8Array(png)], { type: "image/png" }));
	const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
	const context = canvas.getContext("2d");
	if (!context) throw new BridgeError("SCREENSHOT_UNAVAILABLE", "Could not draw screenshot annotations");
	context.drawImage(bitmap, 0, 0);
	context.lineWidth = Math.max(2, 2 * scale);
	context.font = `bold ${Math.round(12 * scale)}px sans-serif`;
	items.forEach((item, index) => {
		const x = item.rect.x * scale;
		const y = item.rect.y * scale;
		context.strokeStyle = "#f97316";
		context.strokeRect(x, y, item.rect.width * scale, item.rect.height * scale);
		const label = String(index + 1);
		const width = context.measureText(label).width + 8 * scale;
		const height = 16 * scale;
		context.fillStyle = "#f97316";
		context.fillRect(x, Math.max(0, y - height), width, height);
		context.fillStyle = "#ffffff";
		context.fillText(label, x + 4 * scale, Math.max(height, y) - 4 * scale);
	});
	const blob = await canvas.convertToBlob({ type: "image/png" });
	return new Uint8Array(await blob.arrayBuffer());
}
