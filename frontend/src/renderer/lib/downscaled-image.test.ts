import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

describe("requestDownscaledImage", () => {
	let bitmaps: Array<{ options?: ImageBitmapOptions }>;
	beforeEach(() => {
		vi.resetModules();
		bitmaps = [];
		vi.stubGlobal("fetch", vi.fn(async () => new Response("png", { headers: { "content-type": "image/png" } })));
		vi.stubGlobal("createImageBitmap", vi.fn(async (_blob: Blob, options?: ImageBitmapOptions) => {
			bitmaps.push({ options });
			return { width: 640, height: 400, close: () => undefined };
		}));
		vi.stubGlobal("OffscreenCanvas", class {
			constructor(public width: number, public height: number) {}
			getContext() { return { drawImage: () => undefined }; }
			async convertToBlob() { return new Blob(["webp"], { type: "image/webp" }); }
		});
		let next = 0;
		vi.spyOn(URL, "createObjectURL").mockImplementation(() => `blob:small-${next++}`);
		vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
	});
	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it("decodes each image once, scaled to a card's width, however many cards ask", async () => {
		const { requestDownscaledImage, downscaledImage, DOWNSCALED_IMAGE_WIDTH } = await import("./downscaled-image");
		const [first, second] = await Promise.all([requestDownscaledImage("http://x/shot.png"), requestDownscaledImage("http://x/shot.png")]);
		expect(first).toEqual({ url: "blob:small-0" });
		expect(second).toBe(first);
		expect(fetch).toHaveBeenCalledTimes(1);
		expect(bitmaps).toEqual([{ options: { resizeWidth: DOWNSCALED_IMAGE_WIDTH, resizeQuality: "medium" } }]);
		// A card that comes back reads the kept copy without asking again.
		expect(downscaledImage("http://x/shot.png")).toBe(first);
	});

	it("reports a failure so the card can show the original", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
		const { requestDownscaledImage } = await import("./downscaled-image");
		expect(await requestDownscaledImage("http://x/missing.png")).toEqual({ failed: true });
	});
});
