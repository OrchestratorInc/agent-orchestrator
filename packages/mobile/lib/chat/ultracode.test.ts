import { describe, expect, it } from "vitest";
import { pendingDraft, retriedDraft } from "./pendingSend";
import { isSentDraft, offersUltracode, withUltracode } from "./ultracode";

const excerpt = { conversationId: "c", messageId: "m", revision: 0, text: "quoted" };

describe("ultracode", () => {
	it("is offered to Claude Code on a model with Extra High only", () => {
		expect(offersUltracode("claude-code", ["high", "xhigh"])).toBe(true);
		expect(offersUltracode("claude-code", ["high"])).toBe(false);
		expect(offersUltracode("codex", ["xhigh"])).toBe(false);
	});

	it("names itself once, at the message's start", () => {
		expect(withUltracode("fix it")).toBe("Ultracode:\nfix it");
		expect(withUltracode("use ultracode here")).toBe("use ultracode here");
	});

	// A retried send is matched to the draft it was typed as (`retriedDraft`):
	// with Ultracode on, that is without the prefix sent.
	it("matches a retried send to the draft it was typed as, with or without Ultracode", () => {
		const onRecord = pendingDraft(withUltracode("fix it"), "fix it");
		expect(isSentDraft(retriedDraft(onRecord), "fix it")).toBe(true);
		expect(isSentDraft(retriedDraft(onRecord), "fix that")).toBe(false);
		const quoted = pendingDraft(withUltracode("fix it"), "fix it", [excerpt]);
		expect(isSentDraft(retriedDraft(quoted), "fix it", [excerpt])).toBe(true);
		expect(isSentDraft(retriedDraft(quoted), "fix it")).toBe(false);
		expect(isSentDraft(retriedDraft(pendingDraft("fix it", "fix it")), "fix it")).toBe(true);
	});

	// "Ultracode:" typed by hand, with Ultracode off, then edited away while the
	// retry ran: the edited draft is not the one sent, and must not be cleared.
	it("keeps a draft edited away from an Ultracode prefix typed by hand", () => {
		const typed = "Ultracode:\nfix it";
		const record = pendingDraft(typed, typed);
		expect(isSentDraft(retriedDraft(record), "fix it")).toBe(false);
		expect(isSentDraft(retriedDraft(record), typed)).toBe(true);
	});

	// A record from an older build has only what was sent.
	it("matches an older record by what was sent", () => {
		expect(isSentDraft(retriedDraft({ draftText: "fix it" }), "fix it")).toBe(true);
	});
});
