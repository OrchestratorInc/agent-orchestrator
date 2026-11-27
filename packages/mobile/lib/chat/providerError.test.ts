import { describe, expect, it } from "vitest";

import { errorActivityDuplicatesTurn, providerErrorCopy, turnErrorCopy, usageLimitSentence } from "./providerError";
import type { ConversationActivity } from "./types";

const activity = (over: Partial<ConversationActivity>): ConversationActivity =>
	({ kind: "activity", activityKind: "error", status: "failed", sequence: 1, summary: "", ...over }) as ConversationActivity;

describe("providerErrorCopy", () => {
	// The reason this exists: Codex sends the usage-limit sentence as both the
	// summary and detail.error, and rendering each in turn printed one failure
	// twice inside a single card.
	it("says an identical message once", () => {
		const text = "You've hit your usage limit. Upgrade to Pro or try again at 10:29 PM.";
		const copy = providerErrorCopy(activity({ summary: text, detail: { error: text } as ConversationActivity["detail"] }));
		expect(copy.headline).toBe(text);
		expect(copy.detail).toBeUndefined();
	});

	it("keeps a detail that adds something", () => {
		const copy = providerErrorCopy(activity({
			summary: "The provider rejected the request",
			detail: { error: "quota_exceeded" } as ConversationActivity["detail"],
		}));
		expect(copy).toEqual({ headline: "The provider rejected the request", detail: "quota_exceeded" });
	});

	it("prefers detail.message as the headline", () => {
		const copy = providerErrorCopy(activity({
			summary: "Turn failed",
			detail: { message: "Rate limited" } as ConversationActivity["detail"],
		}));
		expect(copy.headline).toBe("Rate limited");
	});

	it("falls back to detail.error when there is no headline", () => {
		expect(providerErrorCopy(activity({ detail: { error: "boom" } as ConversationActivity["detail"] })).headline).toBe("boom");
	});

	it("never returns an empty headline", () => {
		expect(providerErrorCopy(activity({})).headline).toBe("Provider error");
	});

	describe("providers that wrap the real message in JSON", () => {
		it("unwraps message and additionalDetails", () => {
			const raw = 'stream error: {"error":{"message":"Usage limit reached","additionalDetails":"Resets at 10:29 PM"}}';
			expect(providerErrorCopy(activity({ summary: raw }))).toEqual({
				headline: "Usage limit reached",
				detail: "Resets at 10:29 PM",
			});
		});

		it("says it once when the wrapped fields agree", () => {
			const raw = '{"error":{"message":"Usage limit reached","additionalDetails":"Usage limit reached"}}';
			expect(providerErrorCopy(activity({ summary: raw }))).toEqual({ headline: "Usage limit reached" });
		});

		it("reads the fields even when the JSON does not parse", () => {
			const raw = 'error {"message":"Usage limit reached","additionalDetails":"Buy credits"} trailing junk';
			expect(providerErrorCopy(activity({ summary: raw })).headline).toBe("Usage limit reached");
		});
	});
});

describe("errorActivityDuplicatesTurn", () => {
	const text = "You've hit your usage limit. Upgrade to Pro or try again at 10:29 PM.";

	it("spots the activity restating its turn's failure", () => {
		const a = activity({ summary: text, detail: { error: text } as ConversationActivity["detail"] });
		expect(errorActivityDuplicatesTurn(a, text)).toBe(true);
	});

	it("keeps an activity that says something else", () => {
		const a = activity({ summary: "A tool crashed" });
		expect(errorActivityDuplicatesTurn(a, text)).toBe(false);
	});

	it("keeps everything when the turn reports no error", () => {
		const a = activity({ summary: text });
		expect(errorActivityDuplicatesTurn(a, undefined)).toBe(false);
		expect(errorActivityDuplicatesTurn(a, "   ")).toBe(false);
	});

	it("only ever drops error activities", () => {
		const a = activity({ activityKind: "command", summary: text });
		expect(errorActivityDuplicatesTurn(a, text)).toBe(false);
	});
});

describe("turnErrorCopy", () => {
	// The failure in the bug report: Claude over ACP, its usage limit wrapped in
	// a JSON-RPC error and followed by the adapter's stderr.
	const acpUsageLimit = 'ACP prompt: {"code":-32603,"message":"Internal error: You\'ve hit your 5-hour limit · resets 11am (UTC)","data":{"errorKind":"rate_limit"}}: [session/query] sessionId=c8cf34f2 resume=none apiType=native baseUrl=native (node:9482) [CLAUDE_SDK_CAN_USE_TOOL_SHADOWED] Warning: canUseTool will not be invoked';

	it("leads with the provider's sentence and folds the transport dump", () => {
		expect(turnErrorCopy(acpUsageLimit)).toEqual({
			headline: "You've hit your 5-hour limit · resets 11am (UTC)",
			raw: acpUsageLimit,
			folded: true,
		});
	});

	it("drops the sentence when the reply already said it", () => {
		const copy = turnErrorCopy(acpUsageLimit, "You’ve hit your 5-hour limit · resets 11am (UTC)");
		expect(copy.headline).toBeUndefined();
		expect(copy).toMatchObject({ raw: acpUsageLimit, folded: true });
	});

	// Failures stored on a local daemon.
	it("reads the reason out of a local ACP prompt failure", () => {
		const raw = 'ACP session/prompt: {"code":-32603,"message":"Internal error: Insufficient balance or no resource package. Please recharge.","data":{"providerData":{"errorName":"APIError"}}}';
		expect(turnErrorCopy(raw)).toEqual({ headline: "Insufficient balance or no resource package. Please recharge.", raw, folded: true });
		const cancelled = 'ACP session/prompt: {"code":-32800,"message":"Request cancelled","data":{"error":"context canceled"}}';
		expect(turnErrorCopy(cancelled).headline).toBe("Request cancelled");
	});

	it("uses data.details when the message is a bare Internal error", () => {
		const raw = 'set ACP session mode "auto": {"code":-32603,"message":"Internal error","data":{"details":"Mode auto is not available in this session"}}';
		expect(turnErrorCopy(raw)).toEqual({ headline: "Mode auto is not available in this session", raw, folded: true });
	});

	it("shows a plain error as it is", () => {
		expect(turnErrorCopy("  The agent process exited  ")).toEqual({ raw: "The agent process exited", folded: false });
	});
});

describe("usageLimitSentence", () => {
	it("reads a quota refusal out of an ACP transport failure", () => {
		const raw = 'ACP prompt: {"code":-32603,"message":"Internal error: You’ve hit your weekly limit · resets Nov 14, 5am (Asia/Calcutta)"}: stderr';
		expect(usageLimitSentence(raw)).toBe("You’ve hit your weekly limit · resets Nov 14, 5am (Asia/Calcutta)");
	});

	it("matches a refusal that already arrives clean", () => {
		expect(usageLimitSentence("Usage limit reached for this account")).toBe("Usage limit reached for this account");
	});

	it("ignores failures that are not quota refusals", () => {
		expect(usageLimitSentence('ACP session/prompt: {"code":-32603,"message":"Internal error: Insufficient balance or no resource package. Please recharge."}')).toBeUndefined();
		expect(usageLimitSentence("The agent said the usage limit was fine")).toBeUndefined();
	});
});
