import path from "node:path";
import { writeFile } from "node:fs/promises";
import type { Dialog, IpcMain, NativeImage, SaveDialogOptions, Session } from "electron";
import { markDownloaded } from "./quarantine";
import { MAX_CHAT_IMAGE_BYTES } from "../shared/chat-image";

export { MAX_CHAT_IMAGE_BYTES };

export type ChatImageBytes = { bytes: Uint8Array; mimeType: string };

export type ChatImageIpcDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	clipboard: { writeImage(image: NativeImage): void | Promise<void> };
	createImage: (bytes: Buffer) => NativeImage;
	dialog: Pick<Dialog, "showSaveDialog">;
	downloadsDirectory: () => string;
	write?: (filePath: string, bytes: Uint8Array) => Promise<void>;
	appName: string;
	markDownloaded?: (filePath: string, appName: string) => Promise<void>;
};

export async function fetchChatImage(session: Pick<Session, "fetch">, url: string): Promise<ChatImageBytes> {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		throw new Error("Invalid image URL");
	}
	if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("Unsupported image URL");
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

export function chatImageFileName(name: string): string {
	const base = path.basename(name.replace(/[\\/]/g, "/")).replace(/[\u0000-\u001f<>:"|?*]/g, "").trim();
	return base && base !== "." && base !== ".." ? base : "image.png";
}

export function registerChatImageIpc(deps: ChatImageIpcDeps): void {
	const write = deps.write ?? ((filePath, bytes) => writeFile(filePath, bytes));
	const mark = deps.markDownloaded ?? ((filePath: string, appName: string) => markDownloaded(filePath, appName));

	deps.ipcMain.handle("chatImage:fetch", (event, url: string) => fetchChatImage(event.sender.session, String(url)));

	deps.ipcMain.handle("clipboard:writeImage", async (_event, bytes: Uint8Array) => {
		const image = deps.createImage(Buffer.from(bytes));
		if (image.isEmpty()) throw new Error("Unsupported image format");
		await deps.clipboard.writeImage(image);
	});

	deps.ipcMain.handle("chatImage:save", async (_event, input: { name: string; bytes: Uint8Array }) => {
		const options: SaveDialogOptions = {
			defaultPath: path.join(deps.downloadsDirectory(), chatImageFileName(String(input.name))),
		};
		const result = await deps.dialog.showSaveDialog(options);
		if (result.canceled || !result.filePath) return false;
		await write(result.filePath, input.bytes);
		await mark(result.filePath, deps.appName).catch((error: unknown) => {
			console.warn("AO: could not mark a saved file as downloaded", error);
		});
		return true;
	});
}
