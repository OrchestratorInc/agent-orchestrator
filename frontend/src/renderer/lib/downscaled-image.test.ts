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
		const version = "2026-11-13T10:00:00Z";
		const [first, second] = await Promise.all([requestDownscaledImage("http://x/shot.png", version), requestDownscaledImage("http://x/shot.png", version)]);
		expect(first).toEqual({ url: "blob:small-0" });
		expect(second).toBe(first);
		expect(fetch).toHaveBeenCalledTimes(1);
		expect(fetch).toHaveBeenCalledWith(`http://x/shot.png?v=${encodeURIComponent(version)}`, { cache: "no-store" });
		expect(bitmaps).toEqual([{ options: { resizeWidth: DOWNSCALED_IMAGE_WIDTH, resizeQuality: "medium" } }]);
		// A card that comes back reads the kept copy without asking again.
		expect(downscaledImage("http://x/shot.png", version)).toBe(first);
	});

	it("reports a failure so the card can show the original", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 404 })));
		const { requestDownscaledImage } = await import("./downscaled-image");
		expect(await requestDownscaledImage("http://x/missing.png", "v1")).toEqual({ failed: true });
	});

	it("makes a new copy when the file is overwritten at the same URL", async () => {
		const { requestDownscaledImage, downscaledImage } = await import("./downscaled-image");
		// A local artifact keeps its URL when an agent writes the file again; only its time changes.
		const src = "http://127.0.0.1:3001/api/v1/sessions/s1/preview/files/shot.png?source=artifact&raw=true";
		const before = await requestDownscaledImage(src, "2026-11-13T10:00:00Z");
		const after = await requestDownscaledImage(src, "2026-11-13T10:05:00Z");
		expect(after).not.toEqual(before);
		expect(fetch).toHaveBeenCalledTimes(2);
		// Each version is fetched by its own URL, so no cached response of the old file is reused.
		expect(vi.mocked(fetch).mock.calls.map(([url]) => url)).toEqual([
			`${src}&v=${encodeURIComponent("2026-11-13T10:00:00Z")}`,
			`${src}&v=${encodeURIComponent("2026-11-13T10:05:00Z")}`,
		]);
		expect(downscaledImage(src, "2026-11-13T10:05:00Z")).toBe(after);
	});

	it("converts a few images at a time and drops a queued one no card waits for", async () => {
		const answers: Array<() => void> = [];
		vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => {
			answers.push(() => resolve(new Response("png", { headers: { "content-type": "image/png" } })));
		})));
		const { requestDownscaledImage, MAX_CONVERTING } = await import("./downscaled-image");
		const scrolledPast = new AbortController();
		const first = requestDownscaledImage("http://x/1.png", "v1");
		void requestDownscaledImage("http://x/2.png", "v1");
		void requestDownscaledImage("http://x/3.png", "v1", scrolledPast.signal);
		const fourth = requestDownscaledImage("http://x/4.png", "v1");
		expect(fetch).toHaveBeenCalledTimes(MAX_CONVERTING);
		// The third card scrolled away before its turn: its image is never fetched.
		scrolledPast.abort();
		answers[0]?.();
		await first;
		expect(vi.mocked(fetch).mock.calls.map(([url]) => url)).toEqual(["http://x/1.png?v=v1", "http://x/2.png?v=v1", "http://x/4.png?v=v1"]);
		answers[2]?.();
		expect(await fourth).toMatchObject({ url: expect.stringMatching(/^blob:/) });
	});
});
