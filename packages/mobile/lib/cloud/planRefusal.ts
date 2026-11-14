import type { CloudError } from "./errors";
import { invalidateCloudPlan } from "./planStore";

// Every request the organization's plan refuses (New task, waking a paused
// session, a review, a terminal) opens one sheet that says why, with the
// plan's usage under it, as the desktop opens its Plans page on any refusal.

/** How often a refusal may open the sheet: a retry loop must not keep reopening it. */
export const PLAN_SHEET_COOLDOWN_MS = 60_000;

export type PlanSheetParams = { code?: string; message?: string; resetsAt?: string };

let lastShown = Number.NEGATIVE_INFINITY;

/**
 * The plan refused `error`'s request. The plan is read again either way; the
 * sheet opens unless one opened in the last minute. Reports whether it did.
 */
export function presentPlanRefusal(error: CloudError, open: (params: PlanSheetParams) => void, now = Date.now()): boolean {
	invalidateCloudPlan();
	if (now - lastShown < PLAN_SHEET_COOLDOWN_MS) return false;
	lastShown = now;
	open({
		...(error.code ? { code: error.code } : {}),
		...(error.message ? { message: error.message } : {}),
		...(error.resetsAt ? { resetsAt: error.resetsAt } : {}),
	});
	return true;
}

/** For tests: forget when the sheet last opened. */
export function resetPlanRefusalCooldown(): void {
	lastShown = Number.NEGATIVE_INFINITY;
}
