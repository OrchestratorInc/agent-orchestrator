import { beforeEach, describe, expect, it, vi } from "vitest";

const { requestBinary, cancelBinary } = vi.hoisted(() => ({ requestBinary: vi.fn(), cancelBinary: vi.fn() }));
vi.mock("./bridge", () => ({ aoBridge: { cloudCp: { requestBinary, cancelBinary } } }));

import { captureFileReference, formatCapturedFileReferences } from "../../shared/chat-file-reference";
import { cloudAttachmentReference, cloudMessageAttachments, fetchCloudAttachment, placeCloudAttachment, splitCloudAttachmentReferences, uploadCloudAttachment, uploadTimeoutMs } from "./cloud-attachments";

const encode = (value: unknown) => new TextEncoder().encode(JSON.stringify(value));

beforeEach(() => {
	requestBinary.mockReset();
	cancelBinary.mockReset();
});

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

describe("cloud message attachments", () => {
	const capture = captureFileReference({ path: "cache.ts", side: "file", line: 3, endLine: 3, lines: [{ kind: "context", oldNo: 3, newNo: 3, text: "const ttl = 60;" }] }, "session-1");
	const requestText = `Is this right?\n\nAttached files (read these files in the workspace):\n- ${cloudAttachmentReference({ id: "id-1", fileName: "notes.txt" })}`;

	it("takes the files off a message whose code captures follow them", () => {
		// The composer appends the captures after its "Attached files" list.
		const message = cloudMessageAttachments(formatCapturedFileReferences(requestText, [capture]), requestText, [capture]);
		expect(message.attachmentIds).toEqual(["id-1"]);
		expect(message.displayText).toBe("Is this right?");
		expect(message.text).toBe(formatCapturedFileReferences("Is this right?", [capture]));
		expect(message.text).not.toContain("ao-cloud-attachment:");
		expect(message.text).not.toContain("Attached files");
	});

	it("inlines captures a caller hands over separately after the files come off", () => {
		const message = cloudMessageAttachments(requestText, undefined, [capture]);
		expect(message).toEqual({ text: formatCapturedFileReferences("Is this right?", [capture]), displayText: "Is this right?", attachmentIds: ["id-1"] });
	});

	it("leaves a message without cloud files as the composer sent it", () => {
		const text = formatCapturedFileReferences("Check", [capture]);
		expect(cloudMessageAttachments(text, "Check", [capture])).toEqual({ text, displayText: "Check", attachmentIds: [] });
		expect(cloudMessageAttachments("plain", undefined)).toEqual({ text: "plain", displayText: "plain", attachmentIds: [] });
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

describe("cloud attachment errors and cancellation", () => {
	// The control plane's refusals, each shown in its own words.
	it.each([
		[409, "ATTACHMENTS_UNSUPPORTED", "This session's worker can't take files yet. Restart the session to update it."],
		[429, "too_many_attachments", "A session can hold at most 200 attachments."],
		[413, "attachment_quota_exceeded", "A session's attachments can hold at most 512 MiB in all."],
		[409, "SESSION_TERMINATED", "This session was deleted. Restore it to continue."],
		[400, "invalid_file_name", "The file name holds a hidden or direction-changing character."],
	])("shows a %i %s with the control plane's message", async (status, code, message) => {
		requestBinary.mockResolvedValue({ status, headers: {}, body: encode({ error: code, code, message }) });
		await expect(uploadCloudAttachment("https://cp", "org", "session", { name: "a.txt", bytes: new Uint8Array(1) })).rejects.toThrow(message);
	});

	it("shows a read-only session's refusal of a terminal drop", async () => {
		requestBinary.mockResolvedValue({ status: 403, headers: {}, body: encode({ code: "WORKSPACE_READ_ONLY", message: "This session does not allow workspace writes." }) });
		await expect(placeCloudAttachment("https://cp", "org", "session", "a1")).rejects.toThrow("This session does not allow workspace writes.");
	});

	it("names each request, gives an upload time for its size, and cancels it in main when the caller does", async () => {
		requestBinary.mockReturnValue(new Promise(() => undefined));
		const controller = new AbortController();
		const upload = uploadCloudAttachment("https://cp", "org", "session", { name: "a.txt", bytes: new Uint8Array(1024) }, { signal: controller.signal });
		await vi.waitFor(() => expect(requestBinary).toHaveBeenCalled());
		const init = requestBinary.mock.calls[0][0];
		expect(init.requestId).toMatch(/^[\w-]{1,64}$/);
		expect(init.timeoutMs).toBe(uploadTimeoutMs(1024));
		controller.abort();
		// The caller stops waiting at once, whatever main does.
		await expect(upload).rejects.toThrow();
		expect(cancelBinary).toHaveBeenCalledWith(init.requestId);
	});

	it("scales an upload's time with its size, within main's bound", () => {
		expect(uploadTimeoutMs(0)).toBe(60_000);
		expect(uploadTimeoutMs(25 * 1024 * 1024)).toBe(260_000);
		expect(uploadTimeoutMs(10 * 1024 * 1024 * 1024)).toBe(15 * 60_000);
	});

	it("reads an IPC failure as a person would", async () => {
		requestBinary.mockRejectedValueOnce(new Error("Error invoking remote method 'cloudCp:requestBinary': TypeError: fetch failed"));
		await expect(fetchCloudAttachment("https://cp", "org", "session", { id: "a1", contentType: "text/plain" })).rejects.toThrow("Couldn’t reach AO Cloud. Check the connection and try again.");
		requestBinary.mockRejectedValueOnce(new Error("Error invoking remote method 'cloudCp:requestBinary': Error: Invalid cloud control-plane request: baseUrl is not the configured AO Cloud control plane."));
		await expect(fetchCloudAttachment("https://cp", "org", "session", { id: "a1", contentType: "text/plain" })).rejects.toThrow(/^Invalid cloud control-plane request: baseUrl is not the configured/);
	});

	it("shows main's own answers: over its 32 MiB, or out of time", async () => {
		requestBinary.mockResolvedValueOnce({ status: 413, headers: {}, body: encode({ code: "attachment_too_large", message: "The file is larger than 32 MiB, the most the app sends to AO Cloud at once." }) });
		await expect(uploadCloudAttachment("https://cp", "org", "session", { name: "big.bin", bytes: new Uint8Array(1) })).rejects.toThrow("larger than 32 MiB");
		requestBinary.mockResolvedValueOnce({ status: 504, headers: {}, body: encode({ code: "request_timeout", message: "AO Cloud did not answer within 120 seconds. Try again." }) });
		await expect(placeCloudAttachment("https://cp", "org", "session", "a1")).rejects.toThrow("did not answer within 120 seconds");
	});
});
