import { describe, expect, it } from "vitest";
import type { components } from "../../../api/schema";
import { sideConversationSnapshot } from "./sideConversation";

type SideSnapshot = components["schemas"]["SideSnapshot"];

const side = {
	id: "side-1",
	sessionId: "session",
	mainConversationId: "main",
	state: "ready",
	contextMode: "native",
	label: "Side chat",
	hasWork: false,
	model: "gpt",
	effort: "high",
	createdAt: "2026-01-01T00:00:00Z",
	updatedAt: "2026-01-01T00:00:00Z",
} as SideSnapshot["side"];

const at = (second: number) => new Date(Date.UTC(2026, 0, 1, 0, 0, second)).toISOString();
const turn = (id: string, state: string, second: number, extra: object = {}) =>
	({ id, sideId: side.id, clientMessageId: id, text: id, state, createdAt: at(second), ...extra }) as SideSnapshot["turns"][number];
const message = (id: string, turnId: string, role: string, text: string, second: number, sequence: number) =>
	({ id, sideId: side.id, turnId, role, text, revision: 1, sequence, streaming: false, createdAt: at(second), updatedAt: at(second) }) as SideSnapshot["messages"][number];

describe("sideConversationSnapshot", () => {
	it("leaves queued turns to the queue dock, like the main chat", () => {
		const conversation = sideConversationSnapshot({
			side,
			hasMore: false,
			turns: [turn("done", "completed", 1), turn("waiting", "queued", 3)],
			messages: [
				message("done-user", "done", "user", "first", 1, 1),
				message("done-answer", "done", "assistant", "answer", 2, 2),
				message("waiting-user", "waiting", "user", "later", 3, 3),
			],
			activities: [],
		});
		expect(conversation.turns.map((entry) => entry.id)).toEqual(["done"]);
		expect(conversation.items.map((item) => item.id)).toEqual(["done-user", "done-answer"]);
	});

	it("merges older pages in time order and keeps the newest copy of a message", () => {
		const older: SideSnapshot = {
			side,
			hasMore: false,
			turns: [turn("t1", "completed", 1)],
			messages: [message("m1", "t1", "assistant", "Answer 1", 1, 1)],
			activities: [],
		};
		const latest: SideSnapshot = {
			side,
			hasMore: true,
			turns: [turn("t2", "completed", 2)],
			messages: [message("m2", "t2", "assistant", "Updated answer 2", 2, 2)],
			activities: [{ id: "a2", sideId: side.id, turnId: "t2", kind: "command", status: "completed", summary: "ls", createdAt: at(2) }],
		};
		const conversation = sideConversationSnapshot(latest, [older]);
		// Same timestamp: the tool call sorts before the answer it led to.
		expect(conversation.items.map((item) => item.id)).toEqual(["m1", "a2", "m2"]);
		// Sequences increase in merged order, and same-millisecond entries stay distinct.
		const sequences = conversation.items.map((item) => item.sequence);
		expect(sequences).toEqual([...sequences].sort((a, b) => a - b));
		expect(new Set(sequences).size).toBe(3);
		expect(conversation.hasMoreBefore).toBe(false);
		expect(conversation.items[2]).toMatchObject({ text: "Updated answer 2" });
	});

	it("keeps an item's sequence when older pages load, so turns do not remount", () => {
		const latest: SideSnapshot = {
			side,
			hasMore: true,
			turns: [turn("t2", "completed", 2)],
			messages: [message("m2", "t2", "assistant", "Answer 2", 2, 2)],
			activities: [],
		};
		const older: SideSnapshot = {
			side,
			hasMore: false,
			turns: [turn("t1", "completed", 1)],
			messages: [message("m1", "t1", "assistant", "Answer 1", 1, 1)],
			activities: [],
		};
		const before = sideConversationSnapshot(latest).items.find((item) => item.id === "m2")?.sequence;
		const after = sideConversationSnapshot(latest, [older]).items.find((item) => item.id === "m2")?.sequence;
		expect(after).toBe(before);
	});

	it("keeps a retried side turn retryable when its retry fails too", () => {
		// The side store re-runs the same turn and records itself as the source.
		const conversation = sideConversationSnapshot({
			side,
			hasMore: false,
			turns: [turn("again", "failed", 1, { retryOfTurnId: "again", errorMessage: "rate limited" })],
			messages: [],
			activities: [],
		});
		expect(conversation.turns[0]?.retryOfTurnId).toBeUndefined();
		expect(conversation.turns[0]?.providerTurnId).toBe("again");
	});

	it("keeps failed side turns retryable and reports a running side as busy", () => {
		const conversation = sideConversationSnapshot({
			side,
			hasMore: false,
			turns: [turn("stopped", "failed", 1, { errorMessage: "Stopped." }), turn("live", "running", 2)],
			messages: [],
			activities: [],
		});
		expect(conversation.turns[0]).toMatchObject({ state: "failed", errorMessage: "Stopped.", providerTurnId: "stopped" });
		expect(conversation.turns[1]?.providerTurnId).toBeUndefined();
		expect(conversation.controller.state).toBe("busy");
		expect(conversation.settings).toEqual({ model: "gpt", reasoningEffort: "high" });
	});

	it("carries a user turn's references as excerpt content", () => {
		const conversation = sideConversationSnapshot({
			side,
			hasMore: false,
			turns: [turn("t1", "completed", 1, { references: [{ conversationId: "main", messageId: "source", revision: 3, selection: "quoted" }] })],
			messages: [message("u1", "t1", "user", "why?", 1, 1)],
			activities: [],
		});
		expect(conversation.items[0]).toMatchObject({
			role: "user",
			content: [{ type: "excerpt", text: "quoted", sourceConversationId: "main", sourceMessageId: "source", sourceRevision: 3 }],
		});
	});
});
