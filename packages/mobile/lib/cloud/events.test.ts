import { describe, expect, it, vi } from "vitest";
import type { ClientEvent } from "@aoagents/cloud-client";
import { cloudConversationCapabilities, fetchConversationReplay, pollCloudEvents, toConversationItems, toConversationTurns } from "./events";

const userEvent: ClientEvent = {
	sessionId: "s1", sequence: 1, type: "chat.user_message",
	payload: { text: "hello" }, createdAt: "2026-09-01T00:00:00Z",
} as ClientEvent;

const deltaOne: ClientEvent = {
	sessionId: "s1", sequence: 2, type: "chat.assistant_delta",
	payload: { text: "Hi ", turnId: "t1", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:01Z",
} as ClientEvent;

const deltaTwo: ClientEvent = {
	sessionId: "s1", sequence: 3, type: "chat.assistant_delta",
	payload: { text: "there", turnId: "t1", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:02Z",
} as ClientEvent;

describe("toConversationItems", () => {
	it("keeps provider item identities separate by turn and attempt, but folds revisions of the same item", () => {
		const delta = (sequence: number, turnId: string, attempt: number, itemId: string, text: string): ClientEvent => ({ ...deltaOne, type: "chat.assistant_delta", sequence, payload: { turnId, attempt, itemId, text, stream: "stdout" } });
		expect(toConversationItems([delta(1, "t1", 1, "a", "First"), delta(2, "t1", 1, "b", "Second"), delta(3, "t1", 1, "a", " reply"), delta(4, "t1", 2, "a", "Retry"), delta(5, "t2", 1, "a", "Other")]).filter((item) => item.kind === "message").map((item) => item.text)).toEqual(["First reply", "Second", "Retry", "Other"]);
	});
	it("folds activity deltas and revisions, preserves new-attempt identity, and safely handles unknown kinds", () => {
		const activity = (sequence: number, attempt: number, kind: string, status: string, detail: object): ClientEvent => ({ ...userEvent, sequence, type: "chat.activity", payload: { turnId: "t1", attempt, activity: { id: kind === "unknown-kind" ? "unknown" : "cmd", kind, status, summary: "Run tests", detail } } } as ClientEvent);
		const items = toConversationItems([activity(1, 1, "command", "running", { command: "npm test", outputDelta: "One " }), activity(2, 1, "command", "completed", { outputDelta: "Two" }), activity(3, 2, "command", "recovered", {}), activity(4, 2, "unknown-kind", "unknown-status", { command: { malformed: true } })]);
		expect(items).toMatchObject([{ activityKind: "command", status: "completed", revision: 2, detail: { output: "One Two" } }, { activityKind: "command", status: "recovered" }, { activityKind: "system", status: "completed" }]);
		expect(items[2].kind === "activity" && items[2].detail?.command).toBeUndefined();
	});
	it("keeps automation provenance and the chosen approval decision", () => {
		const items = toConversationItems([{ ...userEvent, type: "chat.user_message", payload: { text: "raw", displayText: "Review completed", origin: "automation", senderLabel: "Reviewer", senderSessionId: "reviewer-1" } } as unknown as ClientEvent,
			{ ...userEvent, sequence: 2, type: "chat.approval_requested", payload: { turnId: "t1", attempt: 1, requestId: "r1", summary: "Write file", toolKind: "edit", decisions: [{ id: "allow", label: "Allow", kind: "allow_once" }] } } as unknown as ClientEvent,
			{ ...userEvent, sequence: 3, type: "chat.approval_decided", payload: { turnId: "t1", requestId: "r1", decision: "allow" } } as ClientEvent]);
		expect(items).toMatchObject([{ origin: "automation", text: "Review completed", senderLabel: "Reviewer", senderSessionId: "reviewer-1" }, { status: "completed", detail: { toolKind: "edit", decision: "allow" }, decisions: [{ id: "allow", kind: "allow_once" }] }]);
	});
	it("does not settle an active reply when a later message is queued", () => {
		const items = toConversationItems([deltaOne, { ...userEvent, type: "chat.user_message", sequence: 3, payload: { text: "Next", turnId: "t2" } }, deltaTwo]);
		expect(items[0]).toMatchObject({ text: "Hi there", streaming: true });
	});
	it("maps a user message", () => {
		const [item] = toConversationItems([userEvent]);
		expect(item).toMatchObject({
			kind: "message", role: "user", origin: "human", text: "hello", sequence: 1, streaming: false,
		});
	});

	// Deltas are fragments of one reply; rendering each as its own bubble is
	// the classic streaming-chat bug.
	it("coalesces consecutive assistant deltas into one message", () => {
		const items = toConversationItems([userEvent, deltaOne, deltaTwo]);
		expect(items).toHaveLength(2);
		expect(items[1]).toMatchObject({ role: "assistant", text: "Hi there", streaming: true });
	});

	it("keeps assistant replies from different turns separate when events interleave", () => {
		const otherTurn = {
			...deltaTwo,
			payload: { ...deltaTwo.payload, turnId: "t2" },
		} as ClientEvent;
		const items = toConversationItems([deltaOne, otherTurn]);
		expect(items).toMatchObject([
			{ role: "assistant", text: "Hi ", turnId: "t1" },
			{ role: "assistant", text: "there", turnId: "t2" },
		]);
	});

	it("marks the assistant message settled once its turn completes", () => {
		const completed = {
			sessionId: "s1", sequence: 4, type: "chat.turn_completed",
			payload: { turnId: "t1", attempt: 1 }, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		const items = toConversationItems([deltaOne, completed]);
		expect(items[0]).toMatchObject({ role: "assistant", streaming: false });
	});

	it("keeps a failed turn's error even when no assistant text was emitted", () => {
		const aborted = {
			sessionId: "s1", sequence: 3, type: "chat.turn_aborted",
			payload: { turnId: "t1", error: "Harness did not start" }, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		expect(toConversationTurns([userEvent, aborted])).toMatchObject([{ id: "t1", state: "failed", errorMessage: "Harness did not start" }]);
		expect(toConversationItems([userEvent, aborted])).toContainEqual(expect.objectContaining({
			kind: "activity", activityKind: "error", status: "failed", summary: "Harness did not start",
		}));
	});

	it("keeps an accepted Cloud message queued until the worker actually starts", () => {
		const queuedMessage = {
			...userEvent,
			payload: { text: "hello", turnId: "t1" },
		} as ClientEvent;
		expect(toConversationTurns([queuedMessage])).toMatchObject([{ id: "t1", state: "queued" }]);
	});

	it("settles a Cloud approval after its decision event", () => {
		const requested = {
			sessionId: "s1", sequence: 3, type: "chat.approval_requested",
			payload: { turnId: "t1", requestId: "r1", summary: "Edit file", decisions: [{ id: "allow_once", label: "Allow once" }] },
			createdAt: "2026-09-01T00:00:03Z",
		} as unknown as ClientEvent;
		const decided = {
			sessionId: "s1", sequence: 4, type: "chat.approval_decided",
			payload: { turnId: "t1", requestId: "r1", decision: "allow_once" }, createdAt: "2026-09-01T00:00:04Z",
		} as unknown as ClientEvent;
		expect(toConversationItems([requested, decided])).toMatchObject([{ activityKind: "approval", status: "completed" }]);
	});

	it("ignores event types it has no rendering for", () => {
		const interrupt = {
			sessionId: "s1", sequence: 5, type: "chat.interrupt_requested",
			payload: { turnId: "t1" }, createdAt: "2026-09-01T00:00:04Z",
		} as ClientEvent;
		expect(toConversationItems([interrupt])).toEqual([]);
	});

	it("starts a new assistant message after an interrupted turn, rather than reopening the old one", () => {
		const interrupted = {
			sessionId: "s1", sequence: 4, type: "chat.turn_interrupted",
			payload: { turnId: "t1", attempt: 1 }, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		const deltaThree = {
			sessionId: "s1", sequence: 5, type: "chat.assistant_delta",
			payload: { text: "again", turnId: "t2", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:04Z",
		} as ClientEvent;
		const items = toConversationItems([deltaOne, interrupted, deltaThree]);
		expect(items).toHaveLength(2);
		expect(items[0]).toMatchObject({ text: "Hi ", streaming: false });
		expect(items[1]).toMatchObject({ text: "again", streaming: true });
	});

	// The only reference to an open assistant bubble must not be dropped
	// while it is still streaming, even if a user message interleaves with no
	// intervening turn-ending event -- otherwise that bubble spins forever.
	it("settles an open assistant message when a user message interleaves before any turn end", () => {
		const interleavedUser = {
			sessionId: "s1", sequence: 4, type: "chat.user_message",
			payload: { text: "meanwhile" }, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		const items = toConversationItems([deltaOne, interleavedUser]);
		expect(items).toHaveLength(2);
		expect(items[0]).toMatchObject({ role: "assistant", text: "Hi ", streaming: false });
		expect(items[1]).toMatchObject({ role: "user", text: "meanwhile" });
	});
});

describe("Cloud capabilities and rich turn summaries", () => {
	it("steers only the current running attempt with an explicit capability", () => {
		const session = { interfaceMode: "chat", runtimeConnected: true, isTerminated: false, activeTurn: { id: "t1", state: "running", attemptCount: 2 } } as import("@aoagents/cloud-client").Session;
		const capability = (attempt: number, steering: boolean): ClientEvent => ({ ...userEvent, type: "chat.turn_capabilities", payload: { turnId: "t1", attempt, steering } });
		expect(cloudConversationCapabilities(session, [capability(1, true)])).toEqual([]);
		expect(cloudConversationCapabilities(session, [capability(2, true)])).toEqual(["steer"]);
		expect(cloudConversationCapabilities(session, [capability(2, true), capability(2, false)])).toEqual([]);
		expect(cloudConversationCapabilities({ ...session, activeTurn: { ...session.activeTurn!, state: "queued" } }, [capability(2, true)])).toEqual([]);
		expect(cloudConversationCapabilities({ ...session, interfaceMode: "tui" }, [capability(2, true)])).toEqual([]);
	});
	it("attaches turn diff patches and plans to the existing mobile summary types", () => {
		const activity = (sequence: number, id: string, kind: string, detail: Record<string, unknown>): ClientEvent => ({ ...userEvent, sequence, type: "chat.activity", payload: { turnId: "t1", attempt: 1, activity: { id, kind, status: "completed", summary: "Changes", detail } } });
		const patch = "diff --git a/main.ts b/main.ts\n--- a/main.ts\n+++ b/main.ts\n@@ -1 +1 @@\n-old\n+new\n";
		const events = [activity(1, "files", "file_change", { files: [{ path: "main.ts", status: "modified" }] }), activity(2, "diff", "turn_diff", { diff: patch }), activity(3, "plan", "plan", { steps: [{ text: "Test", status: "completed" }] })];
		expect(toConversationTurns(events)[0]).toMatchObject({ diff: { files: [{ path: "main.ts", additions: 1, deletions: 1 }] }, plan: { steps: [{ text: "Test", status: "completed" }] } });
		expect(toConversationItems(events)[0]).toMatchObject({ detail: { files: [{ path: "main.ts", patch }] } });
	});
	it("settles all open items and ignores an old attempt's late completion", () => {
		const event = (sequence: number, attempt: number, type: string, itemId?: string): ClientEvent => ({ ...deltaOne, sequence, type, payload: { turnId: "t1", attempt, itemId, text: "Reply", stream: "stdout" } } as ClientEvent);
		const events = [event(1, 1, "chat.assistant_delta", "a"), event(2, 2, "chat.turn_started"), event(3, 2, "chat.assistant_delta", "b"), event(4, 1, "chat.turn_completed")];
		expect(toConversationItems(events)).toMatchObject([{ streaming: false }, { streaming: true }]);
		expect(toConversationTurns(events)[0].state).toBe("running");
		expect(toConversationItems([...events, event(5, 2, "chat.turn_completed")])).toMatchObject([{ streaming: false }, { streaming: false }]);
	});
	it("does not attach a retry's diff to the previous attempt's file activity", () => {
		const activity = (sequence: number, attempt: number, kind: string, detail: Record<string, unknown>): ClientEvent => ({ ...userEvent, sequence, type: "chat.activity", payload: { turnId: "t1", attempt, activity: { id: "same", kind, status: "completed", summary: "Changes", detail } } });
		const patch = (text: string) => "diff --git a/main.ts b/main.ts\n@@ -0,0 +1 @@\n+" + text + "\n";
		const items = toConversationItems([activity(1, 1, "file_change", { files: [{ path: "main.ts" }] }), activity(2, 1, "turn_diff", { diff: patch("first") }), activity(3, 2, "file_change", { files: [{ path: "main.ts" }] }), activity(4, 2, "turn_diff", { diff: patch("retry") })]);
		expect(items).toMatchObject([{ detail: { files: [{ patch: patch("first") }] } }, { detail: { files: [{ patch: patch("retry") }] } }]);
	});
});

describe("fetchConversationReplay", () => {
	it("pages to the end, accumulating events across pages", async () => {
		const pages = [
			{ events: [userEvent], hasMore: true, nextAfter: 1 },
			{ events: [deltaOne], hasMore: true, nextAfter: 2 },
			{ events: [deltaTwo], hasMore: false, nextAfter: 3 },
		];
		const client = makeReplayClient(pages);
		const result = await fetchConversationReplay(client, "o1", "s1");
		expect(client.replayEvents).toHaveBeenCalledTimes(3);
		expect(client.replayEvents).toHaveBeenNthCalledWith(1, "o1", "s1", { after: 0 });
		expect(client.replayEvents).toHaveBeenNthCalledWith(2, "o1", "s1", { after: 1 });
		expect(client.replayEvents).toHaveBeenNthCalledWith(3, "o1", "s1", { after: 2 });
		expect(result.events).toEqual([userEvent, deltaOne, deltaTwo]);
		expect(result.latestSequence).toBe(3);
	});

	it("terminates rather than looping forever when a page claims more without advancing the cursor", async () => {
		const replayEvents = vi.fn(async () => ({ events: [userEvent], hasMore: true, nextAfter: 0 }));
		const client = { replayEvents } as unknown as import("@aoagents/cloud-client").CloudClient;
		const result = await fetchConversationReplay(client, "o1", "s1");
		expect(replayEvents).toHaveBeenCalledTimes(1);
		expect(result.events).toEqual([userEvent]);
		expect(result.latestSequence).toBe(0);
	});
});

function makeReplayClient(pages: Array<{ events: ClientEvent[]; hasMore: boolean; nextAfter: number }>) {
	let call = 0;
	const replayEvents = vi.fn(async () => {
		const page = pages[Math.min(call, pages.length - 1)];
		call += 1;
		return page;
	});
	return { replayEvents } as unknown as import("@aoagents/cloud-client").CloudClient;
}

describe("pollCloudEvents", () => {
	it("advances the cursor monotonically and delivers new events each tick", async () => {
		vi.useFakeTimers();
		const client = makeReplayClient([
			{ events: [userEvent], hasMore: false, nextAfter: 1 },
			{ events: [deltaOne], hasMore: false, nextAfter: 2 },
			{ events: [], hasMore: false, nextAfter: 2 },
		]);
		const controller = new AbortController();
		const seen: number[] = [];
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10,
			onEvents: (events, cursor) => { seen.push(cursor); void events; },
		});

		await vi.advanceTimersByTimeAsync(0);
		expect(client.replayEvents).toHaveBeenNthCalledWith(1, "o1", "s1", expect.objectContaining({ after: 0 }));
		await vi.advanceTimersByTimeAsync(10);
		expect(client.replayEvents).toHaveBeenNthCalledWith(2, "o1", "s1", expect.objectContaining({ after: 1 }));
		await vi.advanceTimersByTimeAsync(10);
		expect(client.replayEvents).toHaveBeenNthCalledWith(3, "o1", "s1", expect.objectContaining({ after: 2 }));
		expect(seen).toEqual([1, 2]);

		controller.abort();
		await vi.advanceTimersByTimeAsync(10);
		await run;
		vi.useRealTimers();
	});

	it("survives a transient failure and retries on the next tick", async () => {
		vi.useFakeTimers();
		let call = 0;
		const replayEvents = vi.fn(async () => {
			call += 1;
			if (call === 1) throw new Error("network down");
			return { events: [userEvent], hasMore: false, nextAfter: 1 };
		});
		const client = { replayEvents } as unknown as import("@aoagents/cloud-client").CloudClient;
		const controller = new AbortController();
		const onEvents = vi.fn();
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10, onEvents,
		});

		await vi.advanceTimersByTimeAsync(0);
		expect(replayEvents).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(10);
		expect(replayEvents).toHaveBeenCalledTimes(2);
		expect(onEvents).toHaveBeenCalledWith([userEvent], 1);

		controller.abort();
		await vi.advanceTimersByTimeAsync(10);
		await run;
		vi.useRealTimers();
	});

	it("stops promptly on abort without leaking a pending timer", async () => {
		vi.useFakeTimers();
		const clearSpy = vi.spyOn(global, "clearTimeout");
		const client = makeReplayClient([{ events: [], hasMore: false, nextAfter: 0 }]);
		const controller = new AbortController();
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10_000,
			onEvents: () => {},
		});

		await vi.advanceTimersByTimeAsync(0);
		controller.abort();
		await run;
		expect(clearSpy).toHaveBeenCalled();
		clearSpy.mockRestore();
		vi.useRealTimers();
	});

	// A backlog page is capped at the server's page limit and reports
	// hasMore; catching up must not idle a full interval between pages, or a
	// large backlog trickles in for minutes before reaching the live tail.
	it("polls again immediately when a page reports more is waiting, without sleeping the interval", async () => {
		vi.useFakeTimers();
		const client = makeReplayClient([
			{ events: [userEvent], hasMore: true, nextAfter: 1 },
			{ events: [deltaOne], hasMore: true, nextAfter: 2 },
			{ events: [deltaTwo], hasMore: false, nextAfter: 3 },
		]);
		const controller = new AbortController();
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10_000,
			onEvents: () => {},
		});

		// All three catch-up pages resolve without any fake-timer advance,
		// because none of the hasMore ticks sleep.
		await vi.advanceTimersByTimeAsync(0);
		expect(client.replayEvents).toHaveBeenCalledTimes(3);

		controller.abort();
		await vi.advanceTimersByTimeAsync(0);
		await run;
		vi.useRealTimers();
	});

	// hasMore alone is not enough to justify skipping the sleep: if the
	// server ever reports hasMore: true without the cursor actually
	// advancing, treating that as "more to fetch" spins the loop against a
	// live server at round-trip rate forever, ignoring intervalMs entirely.
	it("falls through to the interval sleep when hasMore is true but the cursor does not advance", async () => {
		vi.useFakeTimers();
		const replayEvents = vi.fn(async () => ({ events: [], hasMore: true, nextAfter: 0 }));
		const client = { replayEvents } as unknown as import("@aoagents/cloud-client").CloudClient;
		const controller = new AbortController();
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 1_000,
			onEvents: () => {},
		});

		await vi.advanceTimersByTimeAsync(0);
		expect(replayEvents).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(999);
		expect(replayEvents).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(replayEvents).toHaveBeenCalledTimes(2);

		controller.abort();
		await vi.advanceTimersByTimeAsync(0);
		await run;
		vi.useRealTimers();
	});
});
