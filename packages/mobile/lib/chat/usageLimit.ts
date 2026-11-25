import type { ConversationSnapshot, ConversationTurn, ConversationUsageLimit } from "./types";

/**
 * The turn whose usage-limit notice offers Resume and auto-resume, as the
 * desktop picks it: the latest settled turn, refused for a usage limit, that
 * answered a person's prompt, reached the provider and has no retry yet.
 */
export function usageLimitedTurn(snapshot: Pick<ConversationSnapshot, "turns">): ConversationTurn | undefined {
	let latest: ConversationTurn | undefined;
	for (let i = snapshot.turns.length - 1; i >= 0; i--) {
		const turn = snapshot.turns[i]!;
		if (turn.rolledBack || turn.state === "queued" || turn.state === "cancelled") continue;
		latest = turn;
		break;
	}
	if (!latest || latest.state !== "failed" || !latest.usageLimit) return undefined;
	if (!latest.providerTurnId || latest.hasRetryAttempt || !latest.hasHumanPrompt) return undefined;
	const id = latest.id;
	return snapshot.turns.some((turn) => turn.retryOfTurnId === id) ? undefined : latest;
}

/** When the limit resets, in ms, or undefined when the provider gave no time. */
export function usageLimitResetAt(limit: ConversationUsageLimit): number | undefined {
	const at = limit.resetsAt ? Date.parse(limit.resetsAt) : NaN;
	return Number.isFinite(at) ? at : undefined;
}

export type UsageLimitNoticeView = {
	/** The reset has passed, or its time is unknown: the prompt can be sent now. */
	ready: boolean;
	scheduled: boolean;
	showResume: boolean;
	/** "Auto-resume at reset" before the reset, "Cancel auto-resume" while armed. */
	showAutoResume: boolean;
	status?: string;
};

/**
 * What the notice offers, as on the desktop: a way to have the daemon resume at
 * the reset, or Resume when the reset time is unknown. Nothing is offered once
 * the limit has reset. The daemon owns the resuming, so the phone need not be
 * open when it happens.
 */
export function usageLimitNoticeView(limit: ConversationUsageLimit, now: number): UsageLimitNoticeView {
	const resetAt = usageLimitResetAt(limit);
	const ready = resetAt === undefined || now >= resetAt;
	const scheduled = limit.autoResume;
	const showResume = resetAt === undefined && !scheduled;
	const showAutoResume = scheduled || (resetAt !== undefined && !ready);
	let status: string | undefined;
	if (scheduled) status = ready ? "Auto-resume is waiting to retry." : `Resumes automatically ${resetTimeCopy(resetAt!, now)}.`;
	else if (!ready) status = `Resets ${resetTimeCopy(resetAt!, now)}.`;
	return { ready, scheduled, showResume, showAutoResume, status };
}

/** "at 10:29 PM" today, "Tue at 10:29 PM" on another day. */
export function resetTimeCopy(at: number, now: number): string {
	const date = new Date(at);
	const time = date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
	if (date.toDateString() === new Date(now).toDateString()) return `at ${time}`;
	const day = date.toLocaleDateString(undefined, at - now < 6 * 86_400_000 ? { weekday: "short" } : { month: "short", day: "numeric" });
	return `${day} at ${time}`;
}

export type AccountHandoffNote = { accountId?: string; retry?: "sent" | "skipped" };

/** The daemon's note that it moved a usage-limited session to another account. */
export function accountHandoffNote(detail: Record<string, unknown> | undefined): AccountHandoffNote | undefined {
	if (detail?.event !== "account.limit_handoff") return undefined;
	const accountId = typeof detail.accountId === "string" && detail.accountId ? detail.accountId : undefined;
	const retry = detail.retry === "sent" || detail.retry === "skipped" ? detail.retry : undefined;
	return { accountId, retry };
}

export function accountHandoffCopy(note: AccountHandoffNote, accountLabel: string | undefined): { title: string; detail?: string } {
	const title = `Usage limit reached. Moved to ${accountLabel ?? "another account"}`;
	if (note.retry === "sent") return { title, detail: "Prompt sent again." };
	if (note.retry === "skipped") return { title, detail: "Prompt not sent again. Resend it if needed." };
	return { title };
}
