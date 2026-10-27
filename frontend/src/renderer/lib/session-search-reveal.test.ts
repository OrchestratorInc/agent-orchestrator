import { afterEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { peekSessionSearchReveal, resetSessionSearchReveals, revealSessionSearchMatch } from "./session-search-reveal";

describe("revealSessionSearchMatch", () => {
	afterEach(() => {
		resetSessionSearchReveals();
		useUiStore.setState({ chatMessageFocusRequest: null });
	});

	it("hands a session-transcript match to the session with its query for stepping", () => {
		revealSessionSearchMatch({ sessionId: "s1", messageId: "m1", snippet: "the flaky test", query: "flaky" });
		expect(peekSessionSearchReveal("s1")).toEqual({ messageId: "m1", text: "the flaky test", query: "flaky" });
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});

	it("opens a reviewer-chat match through the reviewer chat's focus request", () => {
		revealSessionSearchMatch({ sessionId: "s1", messageId: "m9", snippet: "…flagged a regression…", query: "regression", reviewId: "r1" });
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
			highlights: [{ start: 7, end: 14 }], query: "backoff",
		});
		expect(peekSessionSearchReveal("s1")).toMatchObject({ messageId: "m1", text: "backoff", query: "backoff" });
	});

	it("does nothing for a match with no message to open at", () => {
		revealSessionSearchMatch({ sessionId: "s1", snippet: "title match", query: "title" });
		expect(peekSessionSearchReveal("s1")).toBeUndefined();
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});
});
