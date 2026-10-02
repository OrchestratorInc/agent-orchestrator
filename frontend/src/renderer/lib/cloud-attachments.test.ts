import { afterEach, describe, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import type { CloudCpClient } from "./cloud-cp";
import type { FileAttachment } from "../hooks/useFileAttachments";
import { uploadCloudAttachments } from "./cloud-attachments";

function selection(): FileAttachment {
	const file = new File(["pixels"], "image.png", { type: "image/png" });
	file.arrayBuffer = async () => new TextEncoder().encode("pixels").buffer;
	return { id: "selection-1", file, name: file.name, bytes: file.size, mimeType: file.type };
}
afterEach(() => vi.unstubAllGlobals());
describe("Cloud image uploads", () => {
	it("uploads a File directly with POST fields and a stable idempotency key", async () => {
		vi.stubGlobal("crypto", webcrypto);
		const fetch = vi.fn().mockResolvedValue({ ok: true });
		vi.stubGlobal("fetch", fetch);
		const prepareAttachment = vi.fn().mockResolvedValue({
			attachment: { id: "image-1", status: "pending" },
			upload: { url: "/storage/grant", fields: { key: "temporary-key", policy: "signed" } },
		});
		const completeAttachment = vi.fn().mockResolvedValue({ attachment: { id: "image-1", status: "ready" } });
		const client = { prepareAttachment, completeAttachment } as unknown as CloudCpClient;
		const file = selection();
		const ready = await uploadCloudAttachments(client, "https://cloud.test", "org", "project", undefined, [
			file,
		]);
		expect(prepareAttachment).toHaveBeenCalledWith(
			"org",
			expect.objectContaining({
				filename: "image.png",
				size: 6,
				sha256: expect.stringMatching(/^[0-9a-f]{64}$/),
			}),
			{ idempotencyKey: "selection-1" },
		);
		expect(prepareAttachment.mock.calls[0]?.[1]).not.toHaveProperty("data");
		expect(fetch).toHaveBeenCalledWith(
			"https://cloud.test/storage/grant",
			expect.objectContaining({ method: "POST", credentials: "omit", redirect: "error" }),
		);
		const form = fetch.mock.calls[0]?.[1].body as FormData;
		expect(form.get("key")).toBe("temporary-key");
		expect(form.get("file")).toBeInstanceOf(File);
		expect(ready[0]).toMatchObject({ attachmentId: "image-1", pendingUpload: false });
		expect(ready[0]?.file).toBeUndefined();
	});
	it("recovers lost completion acknowledgements after reopening without the File", async () => {
		vi.stubGlobal("crypto", webcrypto);
		vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true }));
		const prepareAttachment = vi.fn().mockResolvedValue({
			attachment: { id: "image-1", status: "pending" },
			upload: { url: "/upload", fields: {} },
		});
		const completeAttachment = vi
			.fn()
			.mockRejectedValueOnce(new Error("connection lost"))
			.mockResolvedValue({ attachment: { id: "image-1", status: "ready" } });
		const client = { prepareAttachment, completeAttachment } as unknown as CloudCpClient;
		const file = selection();
		await expect(
			uploadCloudAttachments(client, "https://cloud.test", "org", "project", undefined, [file]),
		).rejects.toThrow("connection lost");
		expect(file).toMatchObject({ attachmentId: "image-1", pendingUpload: true });
		const reopened = { ...file, file: undefined };
		const ready = await uploadCloudAttachments(client, "https://cloud.test", "org", "project", undefined, [
			reopened,
		]);
		expect(prepareAttachment).toHaveBeenCalledTimes(1);
		expect(ready[0]?.pendingUpload).toBe(false);
	});
});

it("persists only image IDs and metadata and clears account drafts", async () => {
	const values = new Map<string, string>();
	vi.stubGlobal("localStorage", {
		get length() {
			return values.size;
		},
		key: (index: number) => [...values.keys()][index] ?? null,
		getItem: (key: string) => values.get(key) ?? null,
		setItem: (key: string, value: string) => values.set(key, value),
		removeItem: (key: string) => values.delete(key),
	});

	const { writeCloudImageDraft, readCloudImageDraft, clearCloudImageDrafts } =
		await import("./cloud-attachments");
	const attachment = {
		id: "selection",
		attachmentId: "12345678-1234-1234-1234-123456789abc",
		name: "image.png",
		mimeType: "image/png",
		bytes: 4,
		pendingUpload: true,
		file: new File(["data"], "image.png"),
		data: "secret-bytes",
	};
	writeCloudImageDraft("cloud:task", [attachment]);
	expect(readCloudImageDraft("cloud:task")).toEqual([
		{
			id: attachment.id,
			attachmentId: attachment.attachmentId,
			name: attachment.name,
			mimeType: attachment.mimeType,
			bytes: 4,
			pendingUpload: true,
		},
	]);
	expect(localStorage.getItem("ao.cloud-image-draft:cloud:task")).not.toContain("secret-bytes");
	expect(localStorage.getItem("ao.cloud-image-draft:cloud:task")).not.toContain('"file"');
	clearCloudImageDrafts();
	expect(readCloudImageDraft("cloud:task")).toEqual([]);
});
