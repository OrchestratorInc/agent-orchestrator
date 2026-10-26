import { describe, expect, it } from "vitest";
import * as sse from "./sse";

const { parseSseFrame, takeSseFrames, createConversationEventRegistry, LISTENER_GRACE_MS } = sse;

describe("mobile conversation SSE", () => {
	it("keeps an incomplete tail while reading multiple LF frames", () => {
		const result = takeSseFrames("id: 1\ndata: {\"seq\":1}\n\nid: 2\ndata: {\"seq\":2}\n\nid: 3\nda");
		expect(result.frames).toHaveLength(2);
		expect(result.remainder).toBe("id: 3\nda");
	});

	it("accepts CRLF boundaries from proxies", () => {
		const result = takeSseFrames("id: 4\r\ndata: {\"seq\":4}\r\n\r\n");
		expect(result.frames).toEqual(["id: 4\r\ndata: {\"seq\":4}"]);
		expect(parseSseFrame(result.frames[0])?.seq).toBe(4);
	});

	it("uses the SSE id when old daemons omit seq and ignores malformed data", () => {
		expect(parseSseFrame('id: 9\ndata: {"projectId":"p","type":"session_updated"}')?.seq).toBe(9);
		expect(parseSseFrame("id: 10\ndata: nope")).toBeUndefined();
	});

	// The cursor only needs the `id:` line. Skipping JSON.parse for frames nobody is
	// subscribed to is what keeps a 200k-event replay off the JS thread's critical path.
	it("reads a frame's sequence without parsing its payload", () => {
		expect(sse.readSseFrameSeq('id: 42\ndata: {"seq":42,"projectId":"p"}')).toBe(42);
		expect(sse.readSseFrameSeq("id: 43\r\ndata: nonsense-that-is-never-parsed")).toBe(43);
		expect(sse.readSseFrameSeq("data: {}")).toBeUndefined();
	});

});

describe("conversation event subscriptions", () => {
	it("does not wake the same session ID on another host", () => {
		const registry = createConversationEventRegistry();
		const received: string[] = [];
		registry.subscribe("host-a", "same-session", () => received.push("a"));
		registry.subscribe("host-b", "same-session", () => received.push("b"));

		registry.publish("host-a", event("same-session", 1));
		expect(received).toEqual(["a"]);
		registry.publish("host-b", event("same-session", 2));
		expect(received).toEqual(["a", "b"]);
	});


	it("publishes an event only to listeners for its session", () => {
		const registry = createConversationEventRegistry();
		const received: string[] = [];
		registry.subscribe("host-a", "session-1", () => received.push("session-1"));
		registry.subscribe("host-a", "session-2", () => received.push("session-2"));

		registry.publish("host-a", event("session-2", 4));

		expect(received).toEqual(["session-2"]);
	});

	it("reports whether anything is listening, so the stream can skip parsing", () => {
		let now = 1_000_000;
		const registry = sse.createConversationEventRegistry(() => now);
		expect(registry.hasListeners("host-a")).toBe(false);

		const unsubscribe = registry.subscribe("host-a", "session-1", () => {});
		expect(registry.hasListeners("host-a")).toBe(true);
		expect(registry.hasListeners("host-b")).toBe(false);

		// Unsubscribing no longer flips this straight to false. A skipped payload
		// is unrecoverable, and a chat screen re-subscribing across a network
		// change would otherwise drop the events that arrive in the gap. It goes
		// false once the grace has passed with nothing listening.
		unsubscribe();
		expect(registry.hasListeners("host-a")).toBe(true);
		expect(registry.hasListeners("host-b")).toBe(false);

		now += sse.LISTENER_GRACE_MS + 1;
		expect(registry.hasListeners("host-a")).toBe(false);
	});

	it("stops publishing after a listener unsubscribes", () => {
		const registry = createConversationEventRegistry();
		const received: number[] = [];
		const unsubscribe = registry.subscribe("host-a", "session-1", (next) => received.push(next.seq));
		unsubscribe();

		registry.publish("host-a", event("session-1", 5));

		expect(received).toEqual([]);
	});
});

function event(sessionId: string, seq: number): sse.ConversationEvent {
	return {
		seq,
		projectId: "project-1",
		sessionId,
		type: "session_updated",
		payload: { conversationId: `conversation-${sessionId}` },
		createdAt: "2026-08-11T00:00:00Z",
	};
}

describe("registry listener grace", () => {
	// A payload skipped because nothing was listening is gone for good: the
	// cursor moves past it and the stream never resends it. That is fine for a
	// cold start with no chat open, but a chat screen re-subscribing — which is
	// exactly what a network change causes, as the config changes and effects
	// re-run — leaves a window where live events are dropped. The symptom is a
	// reply that never arrives until the screen is closed and reopened.
	it("still wants payloads briefly after the last listener goes", () => {
		let now = 1_000_000;
		const registry = createConversationEventRegistry(() => now);
		const off = registry.subscribe("host-a", "s1", () => {});
		off();

		expect(registry.hasListeners("host-a")).toBe(true);
	});

	it("stops wanting payloads once nothing has listened for a while", () => {
		let now = 1_000_000;
		const registry = createConversationEventRegistry(() => now);
		const off = registry.subscribe("host-a", "s1", () => {});
		off();
		now += LISTENER_GRACE_MS + 1;

		expect(registry.hasListeners("host-a")).toBe(false);
	});

	// A cold start where no chat has ever been opened must still skip the
	// backlog, which is what the optimisation is for.
	it("does not want payloads when nothing has ever subscribed", () => {
		const registry = createConversationEventRegistry(() => 1_000_000);
		expect(registry.hasListeners("host-a")).toBe(false);
	});

	it("wants payloads while a listener is active", () => {
		const registry = createConversationEventRegistry(() => 1_000_000);
		registry.subscribe("host-a", "s1", () => {});
		expect(registry.hasListeners("host-a")).toBe(true);
	});
});
