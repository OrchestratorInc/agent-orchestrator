import { describe, expect, it } from "vitest";
import { pendingDraft, retriedDraft } from "./pendingSend";
import { DEFAULT_EFFORT_STOP, isSentDraft, offersUltracode, ULTRACODE, ultracodeKey, ultracodeLevel, withUltracode, withUltracodeStop } from "./ultracode";

const excerpt = { conversationId: "c", messageId: "m", revision: 0, text: "quoted" };

describe("ultracode", () => {
	// Every model Claude Code runs on a machine of the user's own, whatever
	// levels it takes.
	it("is offered to Claude Code on a machine's own chats, and to nothing else", () => {
		expect(offersUltracode("claude-code")).toBe(true);
		expect(offersUltracode("claude-code", true)).toBe(true);
		expect(offersUltracode("claude-code", false)).toBe(false);
		expect(offersUltracode("codex")).toBe(false);
		expect(offersUltracode(undefined)).toBe(false);
	});

	// The slider's top stop, above the model's own levels.
	it("is the stop after the model's levels, checked in place of the level while on", () => {
		const levels = [
			{ value: "high", label: "High", selected: false },
			{ value: "xhigh", label: "Extra high", selected: true },
		];
		expect(withUltracodeStop(levels, false)).toEqual([...levels, { value: ULTRACODE, label: "Ultracode", selected: false }]);
		expect(withUltracodeStop(levels, true)).toEqual([
			{ value: "high", label: "High", selected: false },
			{ value: "xhigh", label: "Extra high", selected: false },
			{ value: ULTRACODE, label: "Ultracode", selected: true },
		]);
	});

	// Claude Haiku takes no effort: "Default" is the stop that turns it off.
	it("follows a Default stop on a model with no levels", () => {
		expect(withUltracodeStop([], false)).toEqual([
			{ value: DEFAULT_EFFORT_STOP, label: "Default", selected: true },
			{ value: ULTRACODE, label: "Ultracode", selected: false },
		]);
		expect(withUltracodeStop([], true).map((choice) => choice.selected)).toEqual([false, true]);
	});

	// Claude Code's Ultracode runs at Extra high.
	it("saves Extra high where the model has it, and no level elsewhere", () => {
		expect(ultracodeLevel(["low", "high", "xhigh", "max"])).toBe("xhigh");
		expect(ultracodeLevel(["low", "medium", "high"])).toBeUndefined();
		expect(ultracodeLevel([])).toBeUndefined();
	});

	it("is kept per machine and conversation", () => {
		expect(ultracodeKey("host.m", "s1")).toBe("ao.chat.ultracode.host.m.s1");
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
