import { EventEmitter } from "node:events";
import { mkdtemp, open, readdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { registerFileDownloadIpc } from "./file-download-ipc";

let dir: string;
beforeEach(async () => {
	dir = await mkdtemp(path.join(tmpdir(), "ao-file-download-"));
});
afterEach(async () => {
	await rm(dir, { recursive: true, force: true });
});

function sender(id = 1, destroyed = false) {
	return Object.assign(new EventEmitter(), { id, isDestroyed: () => destroyed, isCrashed: () => false });
}

async function fileHandleWrite() {
	const probe = await open(path.join(dir, "probe"), "w");
	const proto = Object.getPrototypeOf(probe) as { write: (...args: unknown[]) => Promise<{ bytesWritten: number }> };
	await probe.close();
	await rm(path.join(dir, "probe"));
	return { proto, real: proto.write };
}

function setup(chosen: string | null) {
	const registered = new Map<string, (event: unknown, ...args: unknown[]) => Promise<unknown>>();
	const ipcMain = { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => Promise<unknown>) => registered.set(channel, handler)) };
	const dialog = { showSaveDialog: vi.fn(async () => (chosen ? { canceled: false, filePath: chosen } : { canceled: true })) };
	const markDownloaded = vi.fn(async () => undefined);
	registerFileDownloadIpc({ ipcMain: ipcMain as never, dialog: dialog as never, downloadsDirectory: () => "/Users/me/Downloads", appName: "Orchestrator", markDownloaded });
	const call = (channel: string, from: ReturnType<typeof sender>, ...args: unknown[]) => registered.get(`fileDownload:${channel}`)!({ sender: from }, ...args);
	return { call, dialog, markDownloaded };
}

describe("registerFileDownloadIpc", () => {
	it("asks where to save, then writes the streamed chunks there and marks the file", async () => {
		const target = path.join(dir, "report.pdf");
		const { call, dialog, markDownloaded } = setup(target);
		const window = sender();
		const id = await call("start", window, "../report.pdf");
		expect(dialog.showSaveDialog).toHaveBeenCalledWith({ defaultPath: "/Users/me/Downloads/report.pdf" });
		await call("append", window, id, new Uint8Array([1, 2]));
		await call("append", window, id, new Uint8Array([3]));
		expect(await readdir(dir)).not.toContain("report.pdf");
		await call("finish", window, id);
		expect([...(await readFile(target))]).toEqual([1, 2, 3]);
		expect(await readdir(dir)).toEqual(["report.pdf"]);
		expect(markDownloaded).toHaveBeenCalledExactlyOnceWith(target, "Orchestrator");
		expect(window.listenerCount("destroyed")).toBe(0);
		expect(window.listenerCount("render-process-gone")).toBe(0);
	});

	it("writes the rest of a chunk after a short write and fails a write that makes no progress", async () => {
		const target = path.join(dir, "a.bin");
		const { call } = setup(target);
		const window = sender();
		const { proto, real } = await fileHandleWrite();
		const write = vi.spyOn(proto, "write").mockImplementation(function (this: unknown, buffer: unknown, offset: unknown, length: unknown) {
			return real.call(this, buffer, offset, Math.min(Number(length), 1));
		});
		const id = await call("start", window, "a.bin");
		await call("append", window, id, new Uint8Array([1, 2, 3]));
		expect(write).toHaveBeenCalledTimes(3);
		write.mockResolvedValueOnce({ bytesWritten: 0 });
		await expect(call("append", window, id, new Uint8Array([4]))).rejects.toThrow("Short write");
		write.mockRestore();
		await call("finish", window, id);
		expect([...(await readFile(target))]).toEqual([1, 2, 3]);
	});

	it("writes nothing when the user cancels the dialog", async () => {
		const { call } = setup(null);
		await expect(call("start", sender(), "a.bin")).resolves.toBeNull();
		expect(await readdir(dir)).toEqual([]);
	});

	it("removes the partial file on cancel and when the window goes away", async () => {
		const { call } = setup(path.join(dir, "a.bin"));
		const window = sender();
		const cancelled = await call("start", window, "a.bin");
		await call("append", window, cancelled, new Uint8Array([1]));
		await call("cancel", window, cancelled);
		expect(await readdir(dir)).toEqual([]);
		await call("start", window, "a.bin");
		expect(await readdir(dir)).toHaveLength(1);
		window.emit("destroyed");
		await vi.waitFor(async () => expect(await readdir(dir)).toEqual([]));
		await call("start", window, "a.bin");
		window.emit("render-process-gone");
		await vi.waitFor(async () => expect(await readdir(dir)).toEqual([]));
	});

	it("keeps nothing when the window closed while the dialog was open", async () => {
		const { call } = setup(path.join(dir, "a.bin"));
		await expect(call("start", sender(1, true), "a.bin")).resolves.toBeNull();
		expect(await readdir(dir)).toEqual([]);
	});

	it("refuses another window's download", async () => {
		const { call } = setup(path.join(dir, "a.bin"));
		const id = await call("start", sender(1), "a.bin");
		await expect(call("append", sender(2), id, new Uint8Array([1]))).rejects.toThrow("Unknown download");
		await expect(call("finish", sender(2), id)).rejects.toThrow("Unknown download");
	});
});
