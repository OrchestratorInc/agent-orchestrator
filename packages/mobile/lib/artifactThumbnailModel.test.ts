import { describe, expect, it } from "vitest";
import { THUMBNAIL_PAGE_WIDTH, thumbnailPageHtml, thumbnailZoom } from "./artifactThumbnailModel";

describe("thumbnailZoom", () => {
	it("fits the preview page width into the card", () => {
		expect(thumbnailZoom(THUMBNAIL_PAGE_WIDTH / 2)).toBe(0.5);
		expect(thumbnailZoom(0)).toBe(1);
	});
});

describe("thumbnailPageHtml", () => {
	const rule = "<style>html{zoom:0.4!important;overflow:hidden!important}</style>";

	it("writes the zoom into the head", () => {
		expect(thumbnailPageHtml("<!doctype html><html><head><title>x</title></head><body>b</body></html>", 0.4))
			.toBe(`<!doctype html><html><head>${rule}<title>x</title></head><body>b</body></html>`);
	});

	it("falls back to <html>, then to after the doctype, never before it", () => {
		expect(thumbnailPageHtml('<html lang="en"><body>b</body></html>', 0.4)).toBe(`<html lang="en">${rule}<body>b</body></html>`);
		expect(thumbnailPageHtml("<!DOCTYPE html><p>b</p>", 0.4)).toBe(`<!DOCTYPE html>${rule}<p>b</p>`);
		expect(thumbnailPageHtml("<p>b</p>", 0.4)).toBe(`${rule}<p>b</p>`);
	});
});
