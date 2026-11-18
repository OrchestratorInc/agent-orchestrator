import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import {
	peekSessionSearchReveal,
	requestSessionSearchReveal,
	resetSessionSearchReveals,
	revealSessionSearchMatch,
	subscribeSessionSearchReveal,
	takeSessionSearchReveal,
} from "./session-search-reveal";

describe("revealSessionSearchMatch", () => {
	afterEach(() => {
		resetSessionSearchReveals();
		useUiStore.setState({ chatMessageFocusRequest: null });
	});

	it("hands a session-transcript match to the session with its matched text", () => {
		revealSessionSearchMatch({ sessionId: "s1", messageId: "m1", snippet: "the flaky test", matchKind: "user" });
		expect(peekSessionSearchReveal("s1")).toEqual({ messageId: "m1", text: "the flaky test", find: true });
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});

	// Find in chat searches user and assistant text; a word matched in tool or
	// command output, a path or the name would open it on "No results".
	it("asks for find in chat only for a match in a message's words", () => {
		for (const [matchKind, find] of [["user", true], ["assistant", true], ["tool", false], ["command", false], ["path", false], [undefined, false]] as const) {
			revealSessionSearchMatch({ sessionId: "s1", messageId: "m1", snippet: "flaky", matchKind });
			expect(peekSessionSearchReveal("s1")?.find, String(matchKind)).toBe(find);
		}
	});

	// A match in a tool call, command or file change opens at its own row, so its
	// folds open on it; a match in a message's words opens at the message.
	it("carries the activity's sequence only for a match in an activity row", () => {
		for (const [matchKind, activitySequence] of [["command", 42], ["tool", 42], ["path", 42], ["user", undefined], ["assistant", undefined], ["title", undefined]] as const) {
			revealSessionSearchMatch({ sessionId: "s1", messageId: "m1", snippet: "flaky", matchKind, seq: 42 });
			expect(peekSessionSearchReveal("s1")?.activitySequence, matchKind).toBe(activitySequence);
		}
	});

	it("opens a reviewer-chat match through the reviewer chat's focus request", () => {
		revealSessionSearchMatch({ sessionId: "s1", messageId: "m9", snippet: "…flagged a regression…", reviewId: "r1" });
		// The same request the palette's chat search makes, which is what brings
		// the reviewer chat forward; the session's own reveal stays untouched.
		expect(useUiStore.getState().chatMessageFocusRequest).toMatchObject({
			sessionId: "s1", messageId: "m9", reviewId: "r1", text: "flagged a regression",
		});
		expect(peekSessionSearchReveal("s1")).toBeUndefined();
	});

	it("highlights the matched word rather than the whole snippet", () => {
		// A snippet is cut and whitespace-collapsed, so it rarely matches the
		// rendered message; the matched word does, and the timeline marks it.
		revealSessionSearchMatch({
			sessionId: "s1", messageId: "m1", snippet: "…retry backoff  never resets…",
			highlights: [{ start: 7, end: 14 }],
		});
		expect(peekSessionSearchReveal("s1")).toMatchObject({ messageId: "m1", text: "backoff" });
	});

	it("does nothing for a match with no message to open at", () => {
		revealSessionSearchMatch({ sessionId: "s1", snippet: "title match" });
		expect(peekSessionSearchReveal("s1")).toBeUndefined();
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});
});

describe("session search reveal", () => {
	beforeEach(() => {
		resetSessionSearchReveals();
	});

	it("hands a target to the session it belongs to exactly once", () => {
		requestSessionSearchReveal("sess-1", { messageId: "msg-1", text: "flaky login" });
		expect(peekSessionSearchReveal("sess-1")).toEqual({ messageId: "msg-1", text: "flaky login" });
		expect(takeSessionSearchReveal("sess-1")).toEqual({ messageId: "msg-1", text: "flaky login" });
		// Consumed: returning to the session later must not jump again.
		expect(takeSessionSearchReveal("sess-1")).toBeUndefined();
	});

	it("keeps sessions isolated so a stale target cannot reveal another conversation", () => {
		requestSessionSearchReveal("sess-1", { messageId: "msg-1", text: "one" });
		requestSessionSearchReveal("sess-2", { messageId: "msg-2", text: "two" });
		expect(takeSessionSearchReveal("sess-2")).toEqual({ messageId: "msg-2", text: "two" });
		expect(takeSessionSearchReveal("sess-1")).toEqual({ messageId: "msg-1", text: "one" });
	});

	it("ignores a target with no message id", () => {
		requestSessionSearchReveal("sess-1", { messageId: "", text: "native transcript match" });
		expect(peekSessionSearchReveal("sess-1")).toBeUndefined();
	});

	it("notifies a mounted surface when a new target arrives", () => {
		let notified = 0;
		const unsubscribe = subscribeSessionSearchReveal("sess-1", () => {
			notified += 1;
		});
		requestSessionSearchReveal("sess-1", { messageId: "msg-1", text: "one" });
		expect(notified).toBe(1);
		unsubscribe();
		requestSessionSearchReveal("sess-1", { messageId: "msg-2", text: "two" });
		expect(notified).toBe(1);
	});
});
