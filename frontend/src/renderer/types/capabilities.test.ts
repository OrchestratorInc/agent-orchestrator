import { describe, expect, it } from "vitest";
import { can, type ConversationSnapshot } from "./conversation";

const base: ConversationSnapshot = {
	conversationId: "conv-1",
	sessionId: "s-1",
	harness: "codex",
	mode: "chat",
	controller: { state: "ready" },
	latestSequence: 0,
	oldestSequence: 1,
	hasMoreBefore: false,
	settings: {},
	turns: [],
	items: [],
};

describe("can", () => {
	it("reports an advertised capability", () => {
		expect(can({ ...base, capabilities: ["steer", "compaction"] }, "steer")).toBe(true);
	});

	it("refuses one the provider did not advertise", () => {
		expect(can({ ...base, capabilities: ["compaction"] }, "steer")).toBe(false);
	});

	it("refuses everything when no capabilities have been reported", () => {
		expect(can(base, "steer")).toBe(false);
	});

	it("ignores capabilities it does not recognize", () => {
		const snapshot = { ...base, capabilities: ["steer", "something_invented_later"] };
		expect(can(snapshot, "steer")).toBe(true);
		expect(can(snapshot, "compaction")).toBe(false);
	});
});
