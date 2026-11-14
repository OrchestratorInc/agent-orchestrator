import type { ConversationPlan, PlanStep } from "./types";

/**
 * The plan panel's window: about six one-line steps, so a long plan reads as a
 * few steps around the current one rather than a wall of the whole list. The
 * half row at the edge is the cue that there is more to scroll to.
 */
export const PLAN_WINDOW_HEIGHT = 200;

/** How tall the fade is at an edge that has more steps behind it. */
export const PLAN_EDGE_FADE = 28;

export const PLAN_STEP_LABEL: Record<PlanStep["status"], string> = {
	pending: "not started",
	in_progress: "in progress",
	completed: "done",
};

/** The step the window follows: the one in progress, else the next to start, else the last. */
export function activePlanStep(steps: readonly PlanStep[]): number {
	const inProgress = steps.findIndex((step) => step.status === "in_progress");
	if (inProgress >= 0) return inProgress;
	const next = steps.findIndex((step) => step.status === "pending");
	return next >= 0 ? next : steps.length - 1;
}

/**
 * Where to scroll so the followed step sits in the middle of the window, which
 * keeps the finished step before it in view as context. Clamped to the list, so
 * the first and last steps rest against their edge instead of floating mid-window.
 */
export function planFollowOffset(row: { y: number; height: number }, viewportHeight: number, contentHeight: number): number {
	const overflow = contentHeight - viewportHeight;
	if (overflow <= 0) return 0;
	return Math.min(overflow, Math.max(0, row.y - (viewportHeight - row.height) / 2));
}

/** Fade only the edges that have steps hidden behind them. */
export function planEdgeFades(offset: number, viewportHeight: number, contentHeight: number): { top: boolean; bottom: boolean } {
	// A pixel of slack: scroll offsets land on fractional values at the ends.
	return {
		top: offset > 1,
		bottom: contentHeight - viewportHeight - offset > 1,
	};
}

/** Whether the steps are taller than the window, so it has anything to scroll. */
export function planOverflows(viewportHeight: number, contentHeight: number): boolean {
	return viewportHeight > 0 && contentHeight - viewportHeight > 1;
}

/** Whether two renders of the panel would draw the same plan in the same state. */
export function samePlanProps(
	previous: { plan: ConversationPlan; live?: boolean },
	next: { plan: ConversationPlan; live?: boolean },
): boolean {
	if (Boolean(previous.live) !== Boolean(next.live)) return false;
	const a = previous.plan;
	const b = next.plan;
	if (a === b) return true;
	if (a.explanation !== b.explanation || a.steps.length !== b.steps.length) return false;
	return a.steps.every((step, index) => step.text === b.steps[index].text && step.status === b.steps[index].status);
}

/** The plan in a sentence, for the header's accessibility label. */
export function planAccessibilityLabel(steps: readonly PlanStep[]): string {
	const done = steps.filter((step) => step.status === "completed").length;
	if (steps.length > 0 && done === steps.length) return `Plan, all ${steps.length} steps done`;
	const current = steps[activePlanStep(steps)];
	return `Plan, ${done} of ${steps.length} steps done${current ? `. Now: ${current.text}` : ""}`;
}
