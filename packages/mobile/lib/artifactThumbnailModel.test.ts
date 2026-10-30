import { describe, expect, it, vi } from "vitest";
import { cachedThumbnailText, THUMBNAIL_PAGE_WIDTH, thumbnailPageHtml, thumbnailScale } from "./artifactThumbnailModel";

describe("thumbnailScale", () => {
	it("fits the preview page width into the card", () => {
		expect(thumbnailScale(THUMBNAIL_PAGE_WIDTH / 2)).toBe(0.5);
		expect(thumbnailScale(0)).toBe(1);
	});
});

describe("thumbnailPageHtml", () => {
	const rule = "<style>html{overflow:hidden!important}</style>";

	it("hides the scrollbars from the head", () => {
		expect(thumbnailPageHtml("<!doctype html><html><head><title>x</title></head><body>b</body></html>"))
			.toBe(`<!doctype html><html><head>${rule}<title>x</title></head><body>b</body></html>`);
	});

	it("falls back to <html>, then to after the doctype, never before it", () => {
		expect(thumbnailPageHtml('<html lang="en"><body>b</body></html>')).toBe(`<html lang="en">${rule}<body>b</body></html>`);
		expect(thumbnailPageHtml("<!DOCTYPE html><p>b</p>")).toBe(`<!DOCTYPE html>${rule}<p>b</p>`);
		expect(thumbnailPageHtml("<p>b</p>")).toBe(`${rule}<p>b</p>`);
	});
});

describe("cachedThumbnailText", () => {
	it("reads a file version once, keeping a file with nothing to show", async () => {
		const read = vi.fn(async () => undefined);
		expect(await cachedThumbnailText("s|data.bin|t1", read)).toBeUndefined();
		expect(await cachedThumbnailText("s|data.bin|t1", read)).toBeUndefined();
		expect(read).toHaveBeenCalledTimes(1);
	});

	it("reads again after a failed read", async () => {
		const failing = vi.fn(async (): Promise<string | undefined> => { throw new Error("offline"); });
		expect(await cachedThumbnailText("s|notes.md|t1", failing)).toBeUndefined();
		const read = vi.fn(async () => "# Notes");
		expect(await cachedThumbnailText("s|notes.md|t1", read)).toBe("# Notes");
		expect(read).toHaveBeenCalledTimes(1);
	});
});
