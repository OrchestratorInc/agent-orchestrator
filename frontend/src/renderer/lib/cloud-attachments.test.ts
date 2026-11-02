import { beforeEach, describe, expect, it, vi } from "vitest";

const { requestBinary } = vi.hoisted(() => ({ requestBinary: vi.fn() }));
vi.mock("./bridge", () => ({ aoBridge: { cloudCp: { requestBinary } } }));

import { cloudAttachmentReference, placeCloudAttachment, splitCloudAttachmentReferences, uploadCloudAttachment } from "./cloud-attachments";

const encode = (value: unknown) => new TextEncoder().encode(JSON.stringify(value));

beforeEach(() => requestBinary.mockReset());

describe("cloud attachment references", () => {
	it("round-trips the composer's attached files into attachment ids and the written text", () => {
		const message = `What is wrong here?\n\nAttached files (read these files in the workspace):\n- ${cloudAttachmentReference({ id: "id-1", fileName: "screen shot.png" })}\n- ${cloudAttachmentReference({ id: "id-2", fileName: "spec.pdf" })}`;
		expect(splitCloudAttachmentReferences(message)).toEqual({ text: "What is wrong here?", attachmentIds: ["id-1", "id-2"] });
	});

	it("leaves a message alone that lists other files or none", () => {
		const local = "See\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-1.png";
		expect(splitCloudAttachmentReferences(local)).toEqual({ text: local, attachmentIds: [] });
		expect(splitCloudAttachmentReferences("plain")).toEqual({ text: "plain", attachmentIds: [] });
	});

	it("keeps a message of only files", () => {
		const message = `Attached files (read these files in the workspace):\n- ${cloudAttachmentReference({ id: "id-3", fileName: "a.txt" })}`;
		expect(splitCloudAttachmentReferences(message)).toEqual({ text: "", attachmentIds: ["id-3"] });
	});
});

describe("cloud attachment transport", () => {
	it("uploads the bytes with their SHA-256 and returns the stored attachment", async () => {
		requestBinary.mockResolvedValue({ status: 201, headers: {}, body: encode({ attachment: { id: "a1", fileName: "a.txt", contentType: "text/plain; charset=utf-8", size: 2 } }) });
		const attachment = await uploadCloudAttachment("https://cp", "org", "session", { name: "a.txt", data: btoa("hi") });
		expect(attachment.id).toBe("a1");
		const init = requestBinary.mock.calls[0][0];
		expect(init.path).toBe("/api/cloud/v1/orgs/org/sessions/session/attachments?name=a.txt");
		expect(Array.from(init.body as Uint8Array)).toEqual([104, 105]);
		expect(init.headers["X-AO-Artifact-SHA256"]).toBe("8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4");
	});

	it("surfaces the control plane's message when a request fails", async () => {
		requestBinary.mockResolvedValue({ status: 413, headers: {}, body: encode({ message: "An attachment can be at most 25 MiB." }) });
		await expect(uploadCloudAttachment("https://cp", "org", "session", { name: "big.bin", bytes: new Uint8Array(1) })).rejects.toThrow("at most 25 MiB");
	});

	it("returns the sandbox path the worker placed a dropped file at", async () => {
		requestBinary.mockResolvedValue({ status: 200, headers: {}, body: encode({ path: "/workspace/.ao/attachments/a1-a.txt" }) });
		await expect(placeCloudAttachment("https://cp", "org", "session", "a1")).resolves.toBe("/workspace/.ao/attachments/a1-a.txt");
		expect(requestBinary.mock.calls[0][0].path).toBe("/api/cloud/v1/orgs/org/sessions/session/attachments/a1/place");
	});
});
