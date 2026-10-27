import { expect, it } from "vitest";
import { readComposerAttachments } from "./composerAttachments";
import { acceptImageAssets, attachComposerImages, MAX_IMAGE_BYTES } from "./composerImages";

const png = (id: string, fileSize = 5) => ({ uri: id, fileName: id, mimeType: "image/png", fileSize, base64: "aGVsbG8=" });

it("admits supported images and explains each kind of rejection once", () => {
	const result = acceptImageAssets([], [
		png("ok"),
		{ uri: "big", mimeType: "image/png", fileSize: MAX_IMAGE_BYTES + 1 },
		{ uri: "unreadable", mimeType: "image/png" },
		{ ...png("heic"), mimeType: "image/heic" },
		{ ...png("heic-2"), mimeType: "image/heic" },
	], 1);
	expect(result.added).toEqual([{ id: "ok-1", kind: "image", name: "ok", bytes: 5, image: { mimeType: "image/png", data: "aGVsbG8=" } }]);
	expect(result.error).toBe("Each image must be under 10 MB. Some images couldn't be read and were skipped. Only PNG, JPEG, GIF, WebP, and BMP images are supported.");
});

it("stops at the attachment count and the total image size, counting what is already attached", () => {
	const seven = Array.from({ length: 7 }, () => ({ kind: "resource" }));
	expect(acceptImageAssets(seven, [png("a"), png("b")])).toMatchObject({ added: [{ name: "a" }], error: "You can attach up to 8 items." });
	const twenty = [{ kind: "image", bytes: 20 * 1024 * 1024 }];
	expect(acceptImageAssets(twenty, [png("a", 4 * 1024 * 1024), png("b", 2 * 1024 * 1024)])).toMatchObject({ added: [{ name: "a" }], error: "Images must total under 25 MB." });
});

it("adds to the stored attachments, keeping what was there, and records why any were skipped", () => {
	expect(attachComposerImages("images-test", [png("a")])).toBe(true);
	expect(attachComposerImages("images-test", [png("b"), { uri: "big", mimeType: "image/png", fileSize: MAX_IMAGE_BYTES + 1 }])).toBe(true);
	expect(readComposerAttachments("images-test")).toMatchObject({ items: [{ name: "a" }, { name: "b" }], notice: "Each image must be under 10 MB." });
	expect(attachComposerImages("images-test", [{ uri: "unreadable", mimeType: "image/png" }])).toBe(false);
	expect(readComposerAttachments("images-test")).toMatchObject({ items: [{ name: "a" }, { name: "b" }], notice: "Some images couldn't be read and were skipped." });
});
