import { beforeEach, describe, expect, it } from "vitest";
import { snippetSegments } from "./useSessionSearch";
import {
	peekSessionSearchReveal,
	requestSessionSearchReveal,
	resetSessionSearchReveals,
	subscribeSessionSearchReveal,
	takeSessionSearchReveal,
} from "../lib/session-search-reveal";

describe("snippetSegments", () => {
	it("returns plain text when there are no highlights", () => {
		expect(snippetSegments("nothing marked here", undefined)).toEqual([
			{ text: "nothing marked here", match: false },
		]);
		expect(snippetSegments("", undefined)).toEqual([]);
	});

	it("splits a snippet around each matched span", () => {
		const segments = snippetSegments("before needle after", [{ start: 7, end: 13 }]);
		expect(segments).toEqual([
			{ text: "before ", match: false },
			{ text: "needle", match: true },
			{ text: " after", match: false },
		]);
	});

	it("marks the first and last characters without dropping text", () => {
		const segments = snippetSegments("needle in the middle", [
			{ start: 0, end: 6 },
			{ start: 14, end: 20 },
		]);
		expect(segments.map((segment) => segment.text).join("")).toBe("needle in the middle");
		expect(segments.filter((segment) => segment.match).map((segment) => segment.text)).toEqual([
			"needle",
			"middle",
		]);
	});

	it("ignores overlapping and out-of-range spans instead of duplicating text", () => {
		const segments = snippetSegments("short text", [
			{ start: 0, end: 5 },
			{ start: 3, end: 9 },
			{ start: 20, end: 30 },
		]);
		expect(segments.map((segment) => segment.text).join("")).toBe("short text");
		// Overlap is clipped to what the previous span did not cover, and a span
		// entirely past the end contributes nothing at all.
		expect(segments.filter((segment) => segment.match).map((segment) => segment.text)).toEqual([
			"short",
			" tex",
		]);
	});

	it("sorts unordered spans before slicing", () => {
		const segments = snippetSegments("one two three", [
			{ start: 8, end: 13 },
			{ start: 0, end: 3 },
		]);
		expect(segments.filter((segment) => segment.match).map((segment) => segment.text)).toEqual([
			"one",
			"three",
		]);
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
