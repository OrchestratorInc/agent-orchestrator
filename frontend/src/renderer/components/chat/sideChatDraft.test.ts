import { describe, expect, it } from "vitest";
import { emptySideChatDraft, parseSideChatDraft, sideDeliveryHoldsPath, type SideDeliveryReceipt } from "./sideChatDraft";

describe("launch-scoped side draft", () => {
	it("restores text, files, and multiple references", () => {
		const draft = {
			...emptySideChatDraft(),
			text: "What are these?",
			attachments: [
				{
					id: "file",
					path: "notes.txt",
					name: "notes.txt",
					mimeType: "text/plain",
					bytes: 3,
				},
			],
			references: [
				{
					id: "one",
					conversationId: "main",
					messageId: "message-1",
					revision: 2,
					text: "sun",
					role: "assistant" as const,
				},
				{
					id: "two",
					conversationId: "side",
					messageId: "message-2",
					revision: 1,
					text: "moon",
					role: "user" as const,
				},
			],
		};
		expect(parseSideChatDraft(JSON.stringify(draft))).toEqual(draft);
	});

	it("migrates a plain-text draft from the previous side UI", () => {
		expect(parseSideChatDraft("unfinished question")).toEqual({
			...emptySideChatDraft(),
			text: "unfinished question",
		});
	});
});

describe("sideDeliveryHoldsPath", () => {
	const sent = { id: "sent", path: ".ao/attachments/attachment-sent01.txt", name: "sent.txt", mimeType: "text/plain", bytes: 3 };
	const receipt = (): SideDeliveryReceipt => ({
		clientMessageId: "m1",
		request: { clientMessageId: "m1", text: "see file" },
		submitted: { text: "see file", attachments: [sent], references: [] },
	});

	it("keeps a file named by any delivery the draft still owns", () => {
		const base = { ...emptySideChatDraft(), attachments: [sent] };
		expect(sideDeliveryHoldsPath(base, sent.path)).toBe(false);
		expect(sideDeliveryHoldsPath({ ...base, pendingDelivery: receipt() }, sent.path)).toBe(true);
		expect(sideDeliveryHoldsPath({ ...base, openingDelivery: receipt() }, sent.path)).toBe(true);
		expect(sideDeliveryHoldsPath({ ...base, lastHandoffDelivery: receipt() }, sent.path)).toBe(true);
		expect(sideDeliveryHoldsPath({ ...base, pendingCommand: { creationKey: "k", receipt: receipt() } }, sent.path)).toBe(true);
	});

	it("does not hold other files or a missing draft", () => {
		const draft = { ...emptySideChatDraft(), pendingDelivery: receipt() };
		expect(sideDeliveryHoldsPath(draft, ".ao/attachments/attachment-other01.txt")).toBe(false);
		expect(sideDeliveryHoldsPath(undefined, sent.path)).toBe(false);
	});
});
