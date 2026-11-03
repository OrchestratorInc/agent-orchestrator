import { describe, expect, it, vi } from "vitest";
import { appendSpawnAttachments, photoPickerAttachment, readSpawnAttachments, spawnAttachmentPreview } from "./spawn-attachments";

describe("appendSpawnAttachments", () => {
	it("keeps accepted files and reports files that exceed the per-file limit", () => {
		const result = appendSpawnAttachments(
			[{ name: "brief.txt", mimeType: "text/plain", data: "YQ==", bytes: 1 }],
			[
				{ name: "notes.md", mimeType: "text/markdown", data: "Yg==", bytes: 1 },
				{ name: "archive.zip", mimeType: "application/zip", data: "Yw==", bytes: 50 * 1024 * 1024 + 1 },
			],
		);

		expect(result.attachments.map((item) => item.name)).toEqual(["brief.txt", "notes.md"]);
		expect(result.error).toBe("archive.zip must be under 50 MB.");
	});

	it("rejects SVG files before they reach the daemon", () => {
		const result = appendSpawnAttachments([], [
			{ name: "diagram.svg", mimeType: "image/svg+xml", data: "PHN2Zy8+", bytes: 6 },
		]);

		expect(result.attachments).toEqual([]);
		expect(result.error).toBe("diagram.svg is not a supported attachment type.");
	});
});

describe("readSpawnAttachments", () => {
	it("retains encoded data for a file reported at 11 MiB", async () => {
		const readData = vi.fn(async () => "YQ==");
		const result = await readSpawnAttachments([], [
			{ name: "video.mov", mimeType: "video/quicktime", bytes: 11 * 1024 * 1024, readData },
		]);

		expect(readData).toHaveBeenCalledOnce();
		expect(result.error).toBeUndefined();
		expect(result.attachments.map(({ mimeType, data }) => ({ mimeType, data }))).toEqual([
			{ mimeType: "video/quicktime", data: "YQ==" },
		]);
	});

	it("rejects files before reading when size, total, or count is invalid", async () => {
		const readData = vi.fn(async () => "YQ==");
		const existing = [
			{ name: "first.mov", mimeType: "video/quicktime", data: "YQ==", bytes: 50 * 1024 * 1024 },
			{ name: "second.mov", mimeType: "video/quicktime", data: "YQ==", bytes: 50 * 1024 * 1024 },
		];
		const result = await readSpawnAttachments(existing, [
			{ name: "empty.mov", mimeType: "video/quicktime", bytes: 0, readData },
			{ name: "unknown.mov", mimeType: "video/quicktime", readData },
			{ name: "huge.mov", mimeType: "video/quicktime", bytes: 50 * 1024 * 1024 + 1, readData },
			{ name: "extra.mov", mimeType: "video/quicktime", bytes: 1, readData },
		]);

		expect(readData).not.toHaveBeenCalled();
		expect(result.attachments).toEqual(existing);
		expect(result.error).toBe("empty.mov is empty.");

		const full = await readSpawnAttachments(Array(8).fill(existing[0]), [
			{ name: "ninth.mov", mimeType: "video/quicktime", bytes: 1, readData },
		]);
		expect(full.error).toBe("You can attach up to 8 files.");
		expect(readData).not.toHaveBeenCalled();

		const emptyRead = await readSpawnAttachments([], [
			{ name: "missing.mov", mimeType: "video/quicktime", bytes: 1, readData: async () => "" },
		]);
		expect(emptyRead.attachments).toEqual([]);
		expect(emptyRead.error).toBe("missing.mov is empty.");
	});
});

describe("spawnAttachmentPreview", () => {
	it("keeps the picked file for images and previews only those", async () => {
		const readData = vi.fn(async () => "YQ==");
		const result = await readSpawnAttachments([], [
			{ name: "photo.jpg", mimeType: "image/jpeg", bytes: 3, previewUri: "file:///photo.jpg", readData },
			{ name: "notes.pdf", mimeType: "application/pdf", bytes: 3, previewUri: "file:///notes.pdf", readData },
			{ name: "plain.png", mimeType: "image/png", bytes: 3, readData },
		]);

		expect(result.attachments.map(spawnAttachmentPreview)).toEqual(["file:///photo.jpg", undefined, undefined]);
	});
});

// The first bytes of each format, base64-encoded.
const JPEG = "/9j/4AAQSkZJRgAB";
const PNG = "iVBORw0KGgoAAAANSUhEUg==";

describe("photoPickerAttachment", () => {
	it("labels an Android WebP pick exported as JPEG by its bytes and exported size", async () => {
		// Android's picker compresses non-PNG images to JPEG at quality < 1 but
		// reports the source's type and size.
		const asset = { uri: "file:///cache/ImagePicker/a.webp", fileName: "sunset.webp", mimeType: "image/webp", fileSize: 900_000 };
		const picked = photoPickerAttachment(asset, 0, { size: 1234, base64: async () => JPEG });
		expect(picked.bytes).toBe(1234);

		const result = await readSpawnAttachments([], [picked]);
		expect(result.error).toBeUndefined();
		expect(result.attachments.map(({ mimeType, bytes }) => ({ mimeType, bytes }))).toEqual([{ mimeType: "image/jpeg", bytes: 1234 }]);
	});

	it("keeps a PNG a PNG, and keeps the declared type when the bytes are unrecognised", async () => {
		const png = photoPickerAttachment({ uri: "file:///a.png", mimeType: "image/png" }, 0, { size: 10, base64: async () => PNG });
		const odd = photoPickerAttachment({ uri: "file:///b.jxl", mimeType: "image/jxl" }, 1, { size: 10, base64: async () => "aGVsbG8gd29ybGQ=" });
		const result = await readSpawnAttachments([], [png, odd]);
		expect(result.attachments.map((item) => item.mimeType)).toEqual(["image/png", "image/jxl"]);
		expect(odd.name).toBe("photo-2.jxl");
	});
});
