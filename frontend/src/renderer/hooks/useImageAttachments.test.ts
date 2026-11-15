import { renderHook } from "@testing-library/react";
import { act } from "react";
import { describe, expect, it } from "vitest";

import { MAX_ATTACHMENTS, useImageAttachments } from "./useImageAttachments";

const pngFile = (name: string, bytes = 8, type = "image/png") =>
	new File([new Uint8Array(bytes).fill(1)], name, { type });

describe("useImageAttachments", () => {
	it("retains images from two overlapping addFiles calls", async () => {
		const { result } = renderHook(() => useImageAttachments());

		await act(async () => {
			const first = result.current.addFiles([pngFile("a.png")]);
			const second = result.current.addFiles([pngFile("b.png")]);
			await Promise.all([first, second]);
		});

		expect(result.current.attachments).toHaveLength(2);
		expect(result.current.error).toBeNull();
	});

	it("stages a supported image", async () => {
		const { result } = renderHook(() => useImageAttachments());
		await act(async () => {
			await result.current.addFiles([pngFile("a.png")]);
		});
		expect(result.current.attachments).toHaveLength(1);
		expect(result.current.attachments[0]?.mimeType).toBe("image/png");
		expect(result.current.error).toBeNull();
	});

	it("rejects unsupported image types (e.g. SVG) with inline feedback", async () => {
		const { result } = renderHook(() => useImageAttachments());
		await act(async () => {
			await result.current.addFiles([pngFile("vector.svg", 8, "image/svg+xml")]);
		});
		expect(result.current.attachments).toHaveLength(0);
		expect(result.current.error).toMatch(/supported/i);
	});

	it("enforces the count cap against current state", async () => {
		const { result } = renderHook(() => useImageAttachments());
		await act(async () => {
			await result.current.addFiles(Array.from({ length: MAX_ATTACHMENTS + 2 }, (_, i) => pngFile(`img-${i}.png`)));
		});
		expect(result.current.attachments).toHaveLength(MAX_ATTACHMENTS);
		expect(result.current.error).toMatch(/up to/i);
	});
});
