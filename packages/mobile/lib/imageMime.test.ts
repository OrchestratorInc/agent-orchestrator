import { describe, expect, it } from "vitest";
import { sniffImageMimeType } from "./imageMime";

// The first bytes of each format, base64-encoded.
const JPEG = "/9j/4AAQSkZJRgAB";
const PNG = "iVBORw0KGgoAAAANSUhEUg==";
const GIF = "R0lGODlhAQABAIAA";
const WEBP = "UklGRiQAAABXRUJQVlA4IA==";
const HEIC = "AAAAGGZ0eXBoZWljAAAAAA==";

describe("sniffImageMimeType", () => {
	it("names the format from the bytes", () => {
		expect([JPEG, PNG, GIF, WEBP, HEIC].map(sniffImageMimeType)).toEqual(["image/jpeg", "image/png", "image/gif", "image/webp", "image/heic"]);
		expect(sniffImageMimeType("aGVsbG8gd29ybGQ=")).toBeUndefined();
	});
});
