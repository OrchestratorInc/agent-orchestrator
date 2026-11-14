import { randomUUID } from "node:crypto";
import { open, rename, rm, type FileHandle } from "node:fs/promises";
import path from "node:path";
import type { Dialog, IpcMain, IpcMainInvokeEvent } from "electron";
import { chatImageFileName } from "./chat-image-ipc";
import { markDownloaded } from "./quarantine";

export type FileDownloadIpcDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	dialog: Pick<Dialog, "showSaveDialog">;
	downloadsDirectory: () => string;
	appName: string;
	markDownloaded?: (filePath: string, appName: string) => Promise<void>;
};

type Transfer = {
	owner: number;
	file: FileHandle;
	partPath: string;
	filePath: string;
	release: () => void;
};

type Sender = Pick<IpcMainInvokeEvent["sender"], "id" | "isCrashed" | "isDestroyed" | "once" | "removeListener">;

export function registerFileDownloadIpc(deps: FileDownloadIpcDeps): void {
	const mark = deps.markDownloaded ?? ((filePath: string, appName: string) => markDownloaded(filePath, appName));
	const transfers = new Map<string, Transfer>();

	const owned = (sender: Sender, id: unknown): [string, Transfer] => {
		const key = String(id);
		const transfer = transfers.get(key);
		if (!transfer || transfer.owner !== sender.id) throw new Error("Unknown download");
		return [key, transfer];
	};
	const discard = async (key: string) => {
		const transfer = transfers.get(key);
		if (!transfer) return;
		transfers.delete(key);
		transfer.release();
		await transfer.file.close().catch(() => undefined);
		await rm(transfer.partPath, { force: true });
	};

	deps.ipcMain.handle("fileDownload:start", async (event, name: string) => {
		const result = await deps.dialog.showSaveDialog({
			defaultPath: path.join(deps.downloadsDirectory(), chatImageFileName(String(name))),
		});
		if (result.canceled || !result.filePath) return null;
		const partPath = path.join(path.dirname(result.filePath), `.${path.basename(result.filePath)}.${randomUUID()}.download`);
		const file = await open(partPath, "wx");
		const sender: Sender = event.sender;
		if (sender.isDestroyed() || sender.isCrashed()) {
			await file.close().catch(() => undefined);
			await rm(partPath, { force: true });
			return null;
		}
		const key = randomUUID();
		const onGone = () => void discard(key);
		sender.once("destroyed", onGone);
		sender.once("render-process-gone", onGone);
		transfers.set(key, {
			owner: sender.id,
			file,
			partPath,
			filePath: result.filePath,
			release: () => {
				sender.removeListener("destroyed", onGone);
				sender.removeListener("render-process-gone", onGone);
			},
		});
		return key;
	});

	deps.ipcMain.handle("fileDownload:append", async (event, id: string, bytes: Uint8Array) => {
		const [, transfer] = owned(event.sender, id);
		let offset = 0;
		while (offset < bytes.byteLength) {
			const { bytesWritten } = await transfer.file.write(bytes, offset, bytes.byteLength - offset);
			if (!bytesWritten) throw new Error("Short write");
			offset += bytesWritten;
		}
	});

	deps.ipcMain.handle("fileDownload:finish", async (event, id: string) => {
		const [key, transfer] = owned(event.sender, id);
		transfers.delete(key);
		transfer.release();
		try {
			await transfer.file.close();
			await rename(transfer.partPath, transfer.filePath);
		} catch (error) {
			await rm(transfer.partPath, { force: true });
			throw error;
		}
		await mark(transfer.filePath, deps.appName).catch((error: unknown) => {
			console.warn("AO: could not mark a downloaded file as downloaded", error);
		});
	});

	deps.ipcMain.handle("fileDownload:cancel", async (event, id: string) => {
		const [key] = owned(event.sender, id);
		await discard(key);
	});
}
