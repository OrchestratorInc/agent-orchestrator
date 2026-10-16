/**
 * Copy and save for images shown in chat.
 *
 * The renderer cannot read an image's bytes back out of an `<img>`: daemon,
 * remote-host, and agent-linked images are cross-origin, so a canvas would be
 * tainted and `fetch` is subject to CORS and missing cookies. The main process
 * fetches through the sender's own session instead, the same cookies and cache
 * that loaded the `<img>`, and owns the native clipboard and save dialog.
 */

import path from "node:path";
import { writeFile } from "node:fs/promises";
import type { Clipboard, Dialog, IpcMain, NativeImage, SaveDialogOptions, Session } from "electron";

/** A chat image larger than this is not a screenshot; refuse rather than buffer it. */
export const MAX_CHAT_IMAGE_BYTES = 64 * 1024 * 1024;

export type ChatImageBytes = { bytes: Uint8Array; mimeType: string };

export type ChatImageIpcDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	clipboard: Pick<Clipboard, "writeImage">;
	createImage: (bytes: Buffer) => NativeImage;
	dialog: Pick<Dialog, "showSaveDialog">;
	downloadsDirectory: () => string;
	write?: (filePath: string, bytes: Uint8Array) => Promise<void>;
};

/** Fetch an http(s) image through `session`, refusing anything that is not an image. */
export async function fetchChatImage(session: Pick<Session, "fetch">, url: string): Promise<ChatImageBytes> {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		throw new Error("Invalid image URL");
	}
	if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("Unsupported image URL");
	// The `<img>` was loaded with this session's cookies (daemon auth, remote hosts).
	const response = await session.fetch(parsed.toString(), { credentials: "include" });
	if (!response.ok) throw new Error(`Image request failed (${response.status})`);
	const mimeType = (response.headers.get("content-type") ?? "").split(";")[0]?.trim().toLowerCase() ?? "";
	if (!mimeType.startsWith("image/")) throw new Error("Not an image");
	const declared = Number(response.headers.get("content-length"));
	if (Number.isFinite(declared) && declared > MAX_CHAT_IMAGE_BYTES) throw new Error("Image is too large");
	const bytes = new Uint8Array(await response.arrayBuffer());
	if (bytes.byteLength > MAX_CHAT_IMAGE_BYTES) throw new Error("Image is too large");
	return { bytes, mimeType };
}

/** A file name safe to offer in a save dialog; path separators never survive. */
export function chatImageFileName(name: string): string {
	const base = path.basename(name.replace(/[\\/]/g, "/")).replace(/[\u0000-\u001f<>:"|?*]/g, "").trim();
	return base && base !== "." && base !== ".." ? base : "image.png";
}

export function registerChatImageIpc(deps: ChatImageIpcDeps): void {
	const write = deps.write ?? ((filePath, bytes) => writeFile(filePath, bytes));

	deps.ipcMain.handle("chatImage:fetch", (event, url: string) => fetchChatImage(event.sender.session, String(url)));

	deps.ipcMain.handle("clipboard:writeImage", (_event, bytes: Uint8Array) => {
		const image = deps.createImage(Buffer.from(bytes));
		if (image.isEmpty()) throw new Error("Unsupported image format");
		deps.clipboard.writeImage(image);
	});

	deps.ipcMain.handle("chatImage:save", async (_event, input: { name: string; bytes: Uint8Array }) => {
		const options: SaveDialogOptions = {
			defaultPath: path.join(deps.downloadsDirectory(), chatImageFileName(String(input.name))),
		};
		const result = await deps.dialog.showSaveDialog(options);
		if (result.canceled || !result.filePath) return false;
		await write(result.filePath, input.bytes);
		return true;
	});
}
