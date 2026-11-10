import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	rememberSentAttachments,
	resetSentAttachmentPreviewsForTest,
	sentAttachmentPreview,
	useSentAttachmentPreview,
} from "./sent-attachment-previews";

const PATH = ".ao/attachments/attachment-shot.png";

describe("sent attachment previews", () => {
	const originalCreate = URL.createObjectURL;
	const originalRevoke = URL.revokeObjectURL;
	let created = 0;
	let revoked: string[] = [];

	beforeEach(() => {
		vi.useFakeTimers();
		created = 0;
		revoked = [];
		URL.createObjectURL = vi.fn(() => `blob:test/${++created}`);
		URL.revokeObjectURL = vi.fn((url: string) => {
			revoked.push(url);
		});
	});

	afterEach(() => {
		resetSentAttachmentPreviewsForTest();
		vi.useRealTimers();
		URL.createObjectURL = originalCreate;
		URL.revokeObjectURL = originalRevoke;
	});

	it("hands out the picked file's URL by staged path", () => {
		rememberSentAttachments([{ path: PATH, mimeType: "image/png", bytes: 3, file: new Blob(["png"], { type: "image/png" }) }]);
		expect(sentAttachmentPreview(PATH)).toBe("blob:test/1");
		expect(sentAttachmentPreview(".ao/attachments/attachment-other.png")).toBeUndefined();
	});

	it("decodes the bytes when the file itself is gone", () => {
		rememberSentAttachments([{ path: PATH, mimeType: "text/plain", bytes: 2, data: btoa("hi") }]);
		expect(sentAttachmentPreview(PATH)).toBe("blob:test/1");
	});

	it("keeps the first copy when the same file is sent again", () => {
		const source = { path: PATH, mimeType: "image/png", bytes: 3, file: new Blob(["png"]) };
		rememberSentAttachments([source]);
		rememberSentAttachments([source]);
		expect(URL.createObjectURL).toHaveBeenCalledOnce();
	});

	it("stops handing out an unused copy after a few minutes and revokes it", () => {
		rememberSentAttachments([{ path: PATH, mimeType: "image/png", bytes: 3, file: new Blob(["png"]) }]);
		vi.advanceTimersByTime(5 * 60_000);
		expect(sentAttachmentPreview(PATH)).toBeUndefined();
		expect(revoked).toEqual(["blob:test/1"]);
	});

	it("keeps a copy a mounted tile shows until the tile goes", () => {
		rememberSentAttachments([{ path: PATH, mimeType: "image/png", bytes: 3, file: new Blob(["png"]) }]);
		const tile = renderHook(() => useSentAttachmentPreview(PATH));
		expect(tile.result.current).toBe("blob:test/1");

		vi.advanceTimersByTime(5 * 60_000);
		expect(sentAttachmentPreview(PATH)).toBeUndefined();
		expect(tile.result.current).toBe("blob:test/1");
		expect(revoked).toEqual([]);

		tile.unmount();
		expect(revoked).toEqual(["blob:test/1"]);
	});
});
