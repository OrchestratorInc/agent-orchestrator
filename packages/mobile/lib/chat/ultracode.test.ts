import { describe, expect, it } from "vitest";
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

	// A retried send records what was sent; the composer holds what was typed.
	// With Ultracode on those differ, and the delivered draft was kept to be
	// sent a second time.
	it("matches a retried send to the draft whether or not Ultracode prefixed it", () => {
		expect(isSentDraft("fix it", "fix it")).toBe(true);
		expect(isSentDraft(withUltracode("fix it"), "fix it")).toBe(true);
		expect(isSentDraft(JSON.stringify({ text: withUltracode("fix it"), excerpts: [excerpt] }), "fix it", [excerpt])).toBe(true);
		expect(isSentDraft(withUltracode("fix it"), "fix that")).toBe(false);
		expect(isSentDraft("fix it", "fix it", [excerpt])).toBe(false);
	});
});
