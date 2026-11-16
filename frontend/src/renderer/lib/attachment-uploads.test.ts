import { Blob as NodeBlob } from "node:buffer";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
	fetch: vi.fn<(request: Request) => Promise<Response>>(),
	base: "http://127.0.0.1:3001",
	hosts: new Map<string, string>(),
}));

vi.mock("./api-client", () => ({
	getApiBaseUrl: () => h.base,
	reportingFetch: (request: Request) => h.fetch(request),
}));
vi.mock("./host-clients", () => ({
	baseUrlForHost: (hostId: string) => h.hosts.get(hostId),
}));

import { toWireAttachments, uploadAttachmentFile } from "./attachment-uploads";

const uploaded = (uploadId: string) => new Response(JSON.stringify({ uploadId, bytes: 3 }), { status: 201 });

describe("attachment uploads", () => {
	beforeEach(() => {
		h.fetch.mockReset();
		h.hosts.clear();
	});

	it("streams the file itself as the body to the daemon that will use it", async () => {
		h.fetch.mockResolvedValueOnce(uploaded("upload-a"));
		// Node's own Blob: jsdom's File is no body the Node Request can send, while
		// Chromium streams a real File the same way.
		const file = new NodeBlob([new Uint8Array([1, 2, 3])], { type: "video/quicktime" }) as unknown as Blob;
		await expect(uploadAttachmentFile(file)).resolves.toBe("upload-a");
		const request = h.fetch.mock.calls[0]![0];
		expect(request.method).toBe("POST");
		expect(request.url).toBe("http://127.0.0.1:3001/api/v1/attachment-uploads");
		expect(new Uint8Array(await request.arrayBuffer())).toEqual(new Uint8Array([1, 2, 3]));

		h.hosts.set("mac-mini", "http://127.0.0.1:7654/token");
		h.fetch.mockResolvedValueOnce(uploaded("upload-b"));
		await expect(uploadAttachmentFile(file, "mac-mini")).resolves.toBe("upload-b");
		expect(h.fetch.mock.calls[1]![0].url).toBe("http://127.0.0.1:7654/token/api/v1/attachment-uploads");
	});

	it("reuses an upload for a retry, and uploads again after one fails", async () => {
		const file = new File([new Uint8Array([1])], "a.bin");
		h.fetch.mockResolvedValueOnce(new Response(JSON.stringify({ message: "disk full" }), { status: 500 }));
		await expect(uploadAttachmentFile(file)).rejects.toThrow("disk full");
		h.fetch.mockResolvedValueOnce(uploaded("upload-c"));
		await expect(uploadAttachmentFile(file)).resolves.toBe("upload-c");
		await expect(uploadAttachmentFile(file)).resolves.toBe("upload-c");
		expect(h.fetch).toHaveBeenCalledTimes(2);
	});

	it("names an upload only for a file kept on disk", async () => {
		h.fetch.mockResolvedValueOnce(uploaded("upload-d"));
		const big = new File([new Uint8Array([1])], "big.bin", { type: "application/octet-stream" });
		await expect(toWireAttachments([
			{ mimeType: "text/plain", data: "aGk=", name: "notes.txt" },
			{ mimeType: "application/octet-stream", data: "", file: big },
		])).resolves.toEqual([
			{ mimeType: "text/plain", data: "aGk=" },
			{ mimeType: "application/octet-stream", uploadId: "upload-d" },
		]);
	});

	it("refuses a disconnected remote machine", async () => {
		await expect(uploadAttachmentFile(new File(["x"], "x"), "gone")).rejects.toThrow(/not connected/);
		expect(h.fetch).not.toHaveBeenCalled();
	});
});
