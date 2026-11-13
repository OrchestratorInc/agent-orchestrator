import { describe, expect, it } from "vitest";
import { composerSnapshotKey } from "./composerSnapshot";
import type { ConversationSnapshot } from "./types";

const snapshot = (text: string, state: "running" | "completed" = "running"): ConversationSnapshot => ({
	conversationId: "c",
	sessionId: "s",
	harness: "claude-code",
	mode: "chat",
	controller: { state: state === "running" ? "busy" : "ready" },
	latestSequence: 2,
	oldestSequence: 1,
	hasMoreBefore: false,
	settings: { model: "m" },
	turns: [{ id: "t1", state, requestedAt: "2026-11-14T00:00:00Z" }],
	items: [{ kind: "message", id: "a", turnId: "t1", sequence: 2, revision: text.length, role: "assistant", origin: "provider", text, streaming: state === "running", createdAt: "2026-11-14T00:00:01Z" }],
});

describe("composerSnapshotKey", () => {
	it("ignores the reply growing, which is what streams several times a second", () => {
		expect(composerSnapshotKey(snapshot("Working on it"))).toBe(composerSnapshotKey(snapshot("Working on it, and more")));
	});

	it("changes when the turn settles, which the composer's stop and send controls follow", () => {
		expect(composerSnapshotKey(snapshot("Done", "completed"))).not.toBe(composerSnapshotKey(snapshot("Done", "running")));
	});

	it("changes with the settings the composer shows", () => {
		const other = { ...snapshot("x"), settings: { model: "other" } };
		expect(composerSnapshotKey(other)).not.toBe(composerSnapshotKey(snapshot("x")));
	});
});
