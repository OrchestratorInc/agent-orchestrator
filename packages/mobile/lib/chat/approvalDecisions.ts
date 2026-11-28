import type { DecisionOption } from "./types";

/**
 * The approval decision drawn as the prominent action.
 *
 * As on desktop, that is Allow Once, never a rejection: agents list Deny first
 * (Claude Code does), so "the first one" made Deny the button that stands out.
 * Without Allow Once it is the first decision that isn't a rejection. A host
 * that sends no kinds gives nothing to judge by, so its first decision stays
 * primary, as before.
 */
export function primaryDecisionId(decisions: readonly DecisionOption[] | undefined): string | undefined {
	if (!decisions?.length) return undefined;
	if (!decisions.some((decision) => decision.kind)) return decisions[0].id;
	const allowOnce = decisions.find((decision) => decision.kind === "allow_once");
	if (allowOnce) return allowOnce.id;
	return (decisions.find((decision) => !decision.kind?.startsWith("reject")) ?? decisions[0]).id;
}
