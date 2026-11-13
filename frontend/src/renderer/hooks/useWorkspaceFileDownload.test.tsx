import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { downloadWorkspaceFile, useWorkspaceFileDownload } from "./useWorkspaceFileDownload";

const { connection } = vi.hoisted(() => ({ connection: vi.fn() }));
vi.mock("./useHostConnection", () => ({ useHostConnection: connection }));

const base = "http://127.0.0.1:4100/token";

function streamOf(chunks: number, chunk: Uint8Array) {
	let sent = 0;
	const body = new ReadableStream<Uint8Array>({
		pull(controller) {
			if (sent === chunks) return controller.close();
			sent += 1;
			controller.enqueue(chunk);
		},
	});
	return { response: new Response(body), sent: () => sent };
}

function transfer(id: string | null = "t1") {
	const received: number[] = [];
	return {
		received,
		start: vi.spyOn(aoBridge.fileDownload, "start").mockResolvedValue(id),
		append: vi.spyOn(aoBridge.fileDownload, "append").mockImplementation(async (_id, bytes) => {
			received.push(...bytes);
		}),
		finish: vi.spyOn(aoBridge.fileDownload, "finish").mockResolvedValue(undefined),
		cancel: vi.spyOn(aoBridge.fileDownload, "cancel").mockResolvedValue(undefined),
	};
}

afterEach(() => vi.restoreAllMocks());

describe("downloadWorkspaceFile", () => {
	it("asks where to save, then streams the raw file there in order", async () => {
		const fetchFile = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(new Uint8Array([1, 2, 3, 4])));
		const save = transfer();
		const onSaving = vi.fn();
		await expect(downloadWorkspaceFile(`${base}/`, "s 1", "out/report v2.pdf", onSaving)).resolves.toBe(true);
		expect(String(fetchFile.mock.calls[0]?.[0])).toBe(`${base}/api/v1/sessions/s%201/preview/files/out/report%20v2.pdf?raw=true`);
		expect(save.start).toHaveBeenCalledExactlyOnceWith("report v2.pdf");
		expect(save.received).toEqual([1, 2, 3, 4]);
		expect(save.finish).toHaveBeenCalledExactlyOnceWith("t1");
		expect(save.cancel).not.toHaveBeenCalled();
		expect(onSaving).toHaveBeenCalledOnce();
	});

	it("has no size limit: a body without Content-Length is streamed to the end", async () => {
		const chunk = new Uint8Array(1024 * 1024);
		const { response } = streamOf(100, chunk);
		vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
		const save = transfer();
		let written = 0;
		save.append.mockImplementation(async (_id, bytes) => {
			written += bytes.byteLength;
		});
		await expect(downloadWorkspaceFile(base, "s1", "data.json")).resolves.toBe(true);
		expect(written).toBe(100 * chunk.byteLength);
		expect(save.finish).toHaveBeenCalledExactlyOnceWith("t1");
	});

	it("stops reading when the user cancels the save dialog", async () => {
		const { response, sent } = streamOf(1000, new Uint8Array(1024));
		vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
		const save = transfer(null);
		await expect(downloadWorkspaceFile(base, "s1", "a.bin")).resolves.toBe(false);
		expect(save.append).not.toHaveBeenCalled();
		expect(sent()).toBeLessThan(10);
	});

	it("reports a failed request before asking where to save", async () => {
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("gone", { status: 404 }));
		const save = transfer();
		await expect(downloadWorkspaceFile(base, "s1", "a.txt")).rejects.toThrow("Download failed (404)");
		expect(save.start).not.toHaveBeenCalled();
	});

	it("discards the partial file when writing fails", async () => {
		const { response } = streamOf(5, new Uint8Array(16));
		vi.spyOn(globalThis, "fetch").mockResolvedValue(response);
		const save = transfer();
		save.append.mockRejectedValueOnce(new Error("disk full"));
		await expect(downloadWorkspaceFile(base, "s1", "a.bin")).rejects.toThrow("disk full");
		expect(save.cancel).toHaveBeenCalledExactlyOnceWith("t1");
		expect(save.finish).not.toHaveBeenCalled();
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

	it("tells the user when the download starts and when it is saved", async () => {
		connection.mockReturnValue({ isRemote: true, baseUrl: base });
		vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(new Uint8Array([1])));
		transfer();
		const toast = vi.fn();
		useUiStore.setState({ showGlobalToast: toast });
		renderHook(() => useWorkspaceFileDownload("s1", "h1")).result.current?.("out/report.pdf");
		await waitFor(() => expect(toast).toHaveBeenLastCalledWith("Downloaded report.pdf", undefined, "success"));
		expect(toast).toHaveBeenNthCalledWith(1, "Downloading report.pdf…", undefined, "info");
	});
});
