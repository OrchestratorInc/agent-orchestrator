import { describe, expect, it, vi } from "vitest";
import { chatImageFileName, fetchChatImage, MAX_CHAT_IMAGE_BYTES, registerChatImageIpc } from "./chat-image-ipc";

function session(response: Response) {
	return { fetch: vi.fn(async () => response) };
}

function handlers() {
	const registered = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
	const ipcMain = { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => registered.set(channel, handler)) };
	return { registered, ipcMain };
}

describe("fetchChatImage", () => {
	it("returns image bytes fetched with the session's cookies", async () => {
		const source = session(new Response(new Uint8Array([1, 2, 3]), { headers: { "content-type": "image/png; charset=binary" } }));
		await expect(fetchChatImage(source, "http://127.0.0.1:4000/a.png")).resolves.toEqual({ bytes: new Uint8Array([1, 2, 3]), mimeType: "image/png" });
		expect(source.fetch).toHaveBeenCalledWith("http://127.0.0.1:4000/a.png", { credentials: "include" });
	});

	it("refuses non-http schemes, non-images, failures, and oversized bodies", async () => {
		const ok = session(new Response("x", { headers: { "content-type": "image/png" } }));
		await expect(fetchChatImage(ok, "file:///etc/passwd")).rejects.toThrow("Unsupported image URL");
		await expect(fetchChatImage(ok, "not a url")).rejects.toThrow("Invalid image URL");
		expect(ok.fetch).not.toHaveBeenCalled();
		await expect(fetchChatImage(session(new Response("<html>", { headers: { "content-type": "text/html" } })), "https://x/a")).rejects.toThrow("Not an image");
		await expect(fetchChatImage(session(new Response("", { status: 404 })), "https://x/a")).rejects.toThrow("404");
		const huge = new Response("x", { headers: { "content-type": "image/png", "content-length": String(MAX_CHAT_IMAGE_BYTES + 1) } });
		await expect(fetchChatImage(session(huge), "https://x/a")).rejects.toThrow("too large");
	});
});

describe("chatImageFileName", () => {
	it("never lets a name escape the chosen directory", () => {
		expect(chatImageFileName("../../etc/passwd")).toBe("passwd");
		expect(chatImageFileName("a\\b\\shot.png")).toBe("shot.png");
		expect(chatImageFileName("..")).toBe("image.png");
		expect(chatImageFileName("")).toBe("image.png");
	});
});

describe("registerChatImageIpc", () => {
	function setup(saveResult: { canceled: boolean; filePath?: string }, empty = false) {
		const { registered, ipcMain } = handlers();
		const clipboard = { writeImage: vi.fn() };
		const image = { isEmpty: () => empty };
		const dialog = { showSaveDialog: vi.fn(async () => saveResult) };
		const write = vi.fn(async () => undefined);
		registerChatImageIpc({
			ipcMain: ipcMain as never,
			clipboard,
			createImage: () => image as never,
			dialog: dialog as never,
			downloadsDirectory: () => "/Users/me/Downloads",
			write,
		});
		return { registered, clipboard, image, dialog, write };
	}

	it("writes decodable images to the clipboard and rejects the rest", async () => {
		const ok = setup({ canceled: true });
		await ok.registered.get("clipboard:writeImage")?.({}, new Uint8Array([1]));
		expect(ok.clipboard.writeImage).toHaveBeenCalledExactlyOnceWith(ok.image);
		const bad = setup({ canceled: true }, true);
		await expect(bad.registered.get("clipboard:writeImage")?.({}, new Uint8Array([1]))).rejects.toThrow("Unsupported image format");
		expect(bad.clipboard.writeImage).not.toHaveBeenCalled();
	});

	it("surfaces a failed async clipboard write", async () => {
		const failing = setup({ canceled: true });
		failing.clipboard.writeImage.mockRejectedValueOnce(new Error("clipboard busy"));
		await expect(failing.registered.get("clipboard:writeImage")?.({}, new Uint8Array([1]))).rejects.toThrow("clipboard busy");
	});

	it("saves where the user chooses, defaulting to Downloads", async () => {
		const bytes = new Uint8Array([9]);
		const saved = setup({ canceled: false, filePath: "/tmp/shot.png" });
		await expect(saved.registered.get("chatImage:save")?.({}, { name: "../shot.png", bytes })).resolves.toBe(true);
		expect(saved.dialog.showSaveDialog).toHaveBeenCalledWith({ defaultPath: "/Users/me/Downloads/shot.png" });
		expect(saved.write).toHaveBeenCalledExactlyOnceWith("/tmp/shot.png", bytes);
		const canceled = setup({ canceled: true });
		await expect(canceled.registered.get("chatImage:save")?.({}, { name: "shot.png", bytes })).resolves.toBe(false);
		expect(canceled.write).not.toHaveBeenCalled();
	});
});
