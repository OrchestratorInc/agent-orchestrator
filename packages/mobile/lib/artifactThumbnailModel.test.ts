import { describe, expect, it } from "vitest";
import { THUMBNAIL_PAGE_WIDTH, thumbnailPageHtml, thumbnailScale } from "./artifactThumbnailModel";

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
