import { describe, expect, it } from "vitest";
import type { ConversationTurn } from "./types";
import { accountHandoffCopy, accountHandoffNote, resetTimeCopy, usageLimitedTurn, usageLimitNoticeView } from "./usageLimit";

const limited = (patch: Partial<ConversationTurn> = {}): ConversationTurn => ({
	id: "t2",
	state: "failed",
	requestedAt: "2026-11-17T10:00:00Z",
	providerTurnId: "p2",
	hasHumanPrompt: true,
	usageLimit: { autoResume: false, resetsAt: "2026-11-17T17:00:00Z" },
	...patch,
});
const done: ConversationTurn = { id: "t1", state: "completed", requestedAt: "2026-11-17T09:00:00Z", providerTurnId: "p1" };

describe("usageLimitedTurn", () => {
	it("picks the latest settled turn refused for a usage limit", () => {
		expect(usageLimitedTurn({ turns: [done, limited()] })?.id).toBe("t2");
	});

	it("looks past queued, cancelled and rolled-back turns, as the desktop does", () => {
		const queued: ConversationTurn = { id: "t3", state: "queued", requestedAt: "x" };
		const cancelled: ConversationTurn = { id: "t4", state: "cancelled", requestedAt: "x" };
		const undone: ConversationTurn = { ...done, id: "t5", rolledBack: true };
		expect(usageLimitedTurn({ turns: [limited(), queued, cancelled, undone] })?.id).toBe("t2");
	});

	it("offers nothing once a newer turn ran", () => {
		expect(usageLimitedTurn({ turns: [limited(), { ...done, id: "t3" }] })).toBeUndefined();
		expect(usageLimitedTurn({ turns: [limited(), { id: "t3", state: "running", requestedAt: "x" }] })).toBeUndefined();
	});

	it("offers nothing for a turn that cannot be resumed", () => {
		expect(usageLimitedTurn({ turns: [limited({ usageLimit: undefined })] })).toBeUndefined();
		expect(usageLimitedTurn({ turns: [limited({ providerTurnId: undefined })] })).toBeUndefined();
		expect(usageLimitedTurn({ turns: [limited({ hasHumanPrompt: false })] })).toBeUndefined();
		expect(usageLimitedTurn({ turns: [limited({ hasRetryAttempt: true })] })).toBeUndefined();
	});

	it("offers nothing once the prompt was retried", () => {
		const retry: ConversationTurn = { id: "t3", state: "failed", requestedAt: "x", retryOfTurnId: "t2" };
		// The retry itself failed without a limit, so it is the latest and not limited.
		expect(usageLimitedTurn({ turns: [limited(), retry] })).toBeUndefined();
	});
});

describe("usageLimitNoticeView", () => {
	const reset = Date.parse("2026-11-17T17:00:00Z");

	it("offers auto-resume before the reset, and no Resume", () => {
		const view = usageLimitNoticeView({ autoResume: false, resetsAt: "2026-11-17T17:00:00Z" }, reset - 60_000);
		expect(view).toMatchObject({ ready: false, scheduled: false, showResume: false, showAutoResume: true });
		expect(view.status).toMatch(/^Resets /);
	});

	it("offers cancelling while auto-resume is armed", () => {
		const view = usageLimitNoticeView({ autoResume: true, resetsAt: "2026-11-17T17:00:00Z" }, reset - 60_000);
		expect(view).toMatchObject({ scheduled: true, showResume: false, showAutoResume: true });
		expect(view.status).toMatch(/^Resumes automatically /);
	});

	it("offers nothing after the reset", () => {
		const view = usageLimitNoticeView({ autoResume: false, resetsAt: "2026-11-17T17:00:00Z" }, reset);
		expect(view).toMatchObject({ ready: true, showResume: false, showAutoResume: false, status: undefined });
	});

	it("says it is waiting when the reset passed with auto-resume armed", () => {
		const view = usageLimitNoticeView({ autoResume: true, resetsAt: "2026-11-17T17:00:00Z" }, reset + 1);
		expect(view).toMatchObject({ showResume: false, showAutoResume: true, status: "Auto-resume is waiting to retry." });
	});

	it("offers Resume straight away when the provider gave no reset time", () => {
		for (const resetsAt of [undefined, null, "not a date"]) {
			const view = usageLimitNoticeView({ autoResume: false, resetsAt }, reset);
			expect(view).toMatchObject({ ready: true, showResume: true, showAutoResume: false });
		}
	});
});

describe("resetTimeCopy", () => {
	it("names the day only when the reset is not today", () => {
		const now = new Date(2026, 10, 17, 9, 0).getTime();
		expect(resetTimeCopy(new Date(2026, 10, 17, 17, 0).getTime(), now)).toMatch(/^at /);
		expect(resetTimeCopy(new Date(2026, 10, 18, 17, 0).getTime(), now)).toMatch(/^\S+ at /);
	});
});

describe("account handoff", () => {
	it("reads only the daemon's handoff note", () => {
		expect(accountHandoffNote({ event: "account.limit_handoff", accountId: "a1", retry: "sent" })).toEqual({ accountId: "a1", retry: "sent" });
		expect(accountHandoffNote({ event: "account.limit_handoff", retry: "later" })).toEqual({ accountId: undefined, retry: undefined });
		expect(accountHandoffNote({ event: "compaction" })).toBeUndefined();
		expect(accountHandoffNote(undefined)).toBeUndefined();
	});

	it("says where the session went and whether the prompt was sent again", () => {
		expect(accountHandoffCopy({ accountId: "a1", retry: "sent" }, "me@example.com")).toEqual({
			title: "Usage limit reached. Moved to me@example.com",
			detail: "Prompt sent again.",
		});
		expect(accountHandoffCopy({ retry: "skipped" }, undefined)).toEqual({
			title: "Usage limit reached. Moved to another account",
			detail: "Prompt not sent again. Resend it if needed.",
		});
		expect(accountHandoffCopy({}, "me@example.com").detail).toBeUndefined();
	});
});
