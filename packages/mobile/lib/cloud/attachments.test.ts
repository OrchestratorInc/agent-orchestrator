import { describe, expect, it, vi } from "vitest";
import { cloudConversationSnapshot } from "./conversation";
import { createCloudClient } from "./client";
import { attachmentSizeLabel, cloudAttachmentLimit, cloudAttachmentName, hexDigest, uploadCloudFile } from "./attachments";
import type { CloudClientEvent, CloudSession } from "./types";

const session: CloudSession = {
	id: "s1", orgId: "o", projectId: "p", kind: "worker", harness: "codex", displayName: "Task", branch: "ao/task",
	mode: "trusted", interfaceMode: "chat", activityState: "active", status: "working", runtimeConnected: true,
	isTerminated: false, createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
};
const event = (sequence: number, type: string, payload: Record<string, unknown>): CloudClientEvent => ({ sessionId: "s1", sequence, type, payload, createdAt: "2026-11-05T00:00:00Z" });

describe("cloud attachments", () => {
	it("are offered only where the organization's control plane takes them", () => {
		expect(cloudAttachmentLimit({ offering: { providers: [], defaultProvider: "", billingMode: "off", kind: "b2b", features: ["attachments"], maxAttachmentBytes: 25 << 20 } })).toBe(25 << 20);
		expect(cloudAttachmentLimit({ offering: { providers: [], defaultProvider: "", billingMode: "off", kind: "b2b", features: [] } })).toBeUndefined();
		expect(cloudAttachmentLimit(null)).toBeUndefined();
		expect(attachmentSizeLabel(25 << 20)).toBe("25 MB");
	});

	it("names a file as the control plane keeps it", () => {
		expect(cloudAttachmentName("report.pdf")).toBe("report.pdf");
		expect(cloudAttachmentName("a/b\\c.txt")).toBe("a_b_c.txt");
		expect(cloudAttachmentName("invoice‮fdp.exe")).toBe("invoice_fdp.exe");
		expect(cloudAttachmentName("  ")).toBe("attachment");
		const long = cloudAttachmentName(`${"é".repeat(200)}.png`);
		expect(new TextEncoder().encode(long).byteLength).toBeLessThanOrEqual(238);
		expect(long.endsWith(".png")).toBe(true);
		expect(hexDigest(new Uint8Array([0, 15, 255]).buffer)).toBe("000fff");
	});

	it("uploads a picked file with its hash, and refuses one over the limit before reading it", async () => {
		const uploadAttachment = vi.fn(async () => ({ id: "a1", fileName: "notes.txt", contentType: "text/plain", size: 3 }));
		const readBytes = vi.fn(async () => new Uint8Array([1, 2, 3]));
		const deps = { readBytes, sha256: async () => "ab".repeat(32) };
		const target = { orgId: "o", sessionId: "s1" };
		await expect(uploadCloudFile({ uploadAttachment }, target, { uri: "file:///notes.txt", name: "notes.txt", mimeType: "text/plain", size: 3 }, 10, deps)).resolves.toMatchObject({ id: "a1" });
		expect(uploadAttachment).toHaveBeenCalledWith("o", "s1", { name: "notes.txt", bytes: new Uint8Array([1, 2, 3]), sha256: "ab".repeat(32), contentType: "text/plain", signal: undefined });
		readBytes.mockClear();
		await expect(uploadCloudFile({ uploadAttachment }, target, { uri: "file:///big.bin", name: "big.bin", size: 11 }, 10, deps)).rejects.toThrow(/larger than 10 bytes/);
		expect(readBytes).not.toHaveBeenCalled();
	});

	it("posts the raw bytes with the name and hash the control plane checks", async () => {
		const calls: { url: string; init: RequestInit }[] = [];
		const nativeFetch = (async (url: string, init: RequestInit) => {
			calls.push({ url, init });
			return new Response(JSON.stringify({ attachment: { id: "a1", fileName: "a b.png", contentType: "image/png", size: 3, sha256: "cd".repeat(32) } }), { status: 201 });
		}) as unknown as typeof fetch;
		const client = createCloudClient({ baseUrl: "https://api.example.test", getToken: async () => "access", newKey: () => "k", fetch: nativeFetch, nativeFetch });
		const bytes = new Uint8Array([1, 2, 3]);
		await expect(client.uploadAttachment("o", "s1", { name: "a b.png", bytes, sha256: "cd".repeat(32), contentType: "image/png" })).resolves.toMatchObject({ id: "a1" });
		expect(calls[0].url).toBe("https://api.example.test/api/cloud/v1/orgs/o/sessions/s1/attachments?name=a%20b.png");
		expect(calls[0].init).toMatchObject({ method: "POST", body: bytes, headers: { Authorization: "Bearer access", "Content-Type": "image/png", "X-AO-Artifact-SHA256": "cd".repeat(32) } });
		await expect(client.attachmentRequest("o", "s1", "a1")).resolves.toEqual({ url: "https://api.example.test/api/cloud/v1/orgs/o/sessions/s1/attachments/a1", headers: { Authorization: "Bearer access" } });
	});

	it("shows a sent message's files, a message of only files, and a desktop message as it was typed", () => {
		const file = { id: "a1", fileName: "shot.png", contentType: "image/png", size: 9, sha256: "ab".repeat(32) };
		const snapshot = cloudConversationSnapshot(session, [
			event(1, "chat.user_message", { turnId: "t1", text: "Look at this", attachments: [file, { bogus: true }] }),
			event(2, "chat.user_message", { turnId: "t2", text: "", attachments: [file] }),
			event(3, "chat.user_message", { turnId: "t3", text: "> quoted\n\nWhy?", displayText: "Why?", content: [{ type: "excerpt", text: "quoted", sourceMessageId: "m1" }, { type: "file_reference" }] }),
			event(4, "chat.user_message", { turnId: "t4", text: "" }),
		]);
		expect(snapshot.items).toHaveLength(3);
		expect(snapshot.items[0]).toMatchObject({ text: "Look at this", files: [{ id: "a1", name: "shot.png", mimeType: "image/png", size: 9 }] });
		expect(snapshot.items[1]).toMatchObject({ text: "", files: [{ id: "a1" }] });
		expect(snapshot.items[2]).toMatchObject({ text: "Why?", content: [{ type: "excerpt", text: "quoted", sourceMessageId: "m1" }] });
		expect(snapshot.items[2]).not.toHaveProperty("files");
	});
});
