import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { downloadWorkspaceFile, FileTooLargeError, MAX_FILE_DOWNLOAD_BYTES, useWorkspaceFileDownload } from "./useWorkspaceFileDownload";

const { connection } = vi.hoisted(() => ({ connection: vi.fn() }));
vi.mock("./useHostConnection", () => ({ useHostConnection: connection }));

const base = "http://127.0.0.1:4100/token";
const bytes = new Uint8Array([1, 2, 3, 4]);

afterEach(() => vi.restoreAllMocks());

describe("downloadWorkspaceFile", () => {
	it("reads the raw workspace file from the host and saves it under its own name", async () => {
		const fetchFile = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(bytes));
		const save = vi.spyOn(aoBridge.chatImage, "save").mockResolvedValue(true);
		await expect(downloadWorkspaceFile(`${base}/`, "s 1", "out/report v2.pdf")).resolves.toBe(true);
		expect(String(fetchFile.mock.calls[0]?.[0])).toBe(`${base}/api/v1/sessions/s%201/preview/files/out/report%20v2.pdf?raw=true`);
		expect(save).toHaveBeenCalledExactlyOnceWith({ name: "report v2.pdf", bytes });
	});

	it("refuses a file over the limit before reading its body", async () => {
		const huge = new Response(bytes, { headers: { "content-length": String(MAX_FILE_DOWNLOAD_BYTES + 1) } });
		const read = vi.spyOn(huge.body!, "getReader");
		vi.spyOn(globalThis, "fetch").mockResolvedValue(huge);
		const save = vi.spyOn(aoBridge.chatImage, "save");
		await expect(downloadWorkspaceFile(base, "s1", "big.bin")).rejects.toThrow();
		expect(read).not.toHaveBeenCalled();
		expect(save).not.toHaveBeenCalled();
	});

	it("stops reading a body without Content-Length at the limit", async () => {
		const chunk = new Uint8Array(1024 * 1024);
		let pulled = 0;
		const endless = new ReadableStream<Uint8Array>({ pull(controller) { pulled += 1; controller.enqueue(chunk); } });
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(endless));
		const save = vi.spyOn(aoBridge.chatImage, "save");
		await expect(downloadWorkspaceFile(base, "s1", "data.json")).rejects.toBeInstanceOf(FileTooLargeError);
		expect(pulled).toBeLessThan(MAX_FILE_DOWNLOAD_BYTES / chunk.byteLength + 8);
		expect(save).not.toHaveBeenCalled();
	});

	it("reports a failed request", async () => {
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("gone", { status: 404 }));
		await expect(downloadWorkspaceFile(base, "s1", "a.txt")).rejects.toThrow("Download failed (404)");
	});
});

describe("useWorkspaceFileDownload", () => {
	it("offers download only for a connected remote host", () => {
		connection.mockReturnValue({ isRemote: false, baseUrl: undefined });
		expect(renderHook(() => useWorkspaceFileDownload("s1")).result.current).toBeUndefined();
		connection.mockReturnValue({ isRemote: true, baseUrl: undefined });
		expect(renderHook(() => useWorkspaceFileDownload("s1", "h1")).result.current).toBeUndefined();
		connection.mockReturnValue({ isRemote: true, baseUrl: base });
		expect(renderHook(() => useWorkspaceFileDownload("s1", "h1")).result.current).toBeTypeOf("function");
	});

	it("shows the size limit when a file is too large", async () => {
		connection.mockReturnValue({ isRemote: true, baseUrl: base });
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(bytes, { headers: { "content-length": String(MAX_FILE_DOWNLOAD_BYTES + 1) } }));
		const toast = vi.fn();
		useUiStore.setState({ showGlobalToast: toast });
		renderHook(() => useWorkspaceFileDownload("s1", "h1")).result.current?.("big.bin");
		await waitFor(() => expect(toast).toHaveBeenCalledWith("Couldn’t download file", "This file is over the 64 MB download limit.", "error"));
	});
});
