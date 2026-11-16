import { useEffect, useReducer } from "react";
import type { SessionPRSummary } from "./useSessionScmSummary";
import { noChecksSettlesAt } from "../lib/pr-display";

/**
 * Renders again when the first of these PRs' empty rollups settles into "no
 * checks" (see prHasNoChecks), so a view showing "Waiting for checks" turns
 * ready without anything else changing.
 */
export function usePRNoChecksClock(prs: readonly (SessionPRSummary | undefined)[]): void {
	const [, tick] = useReducer((n: number) => n + 1, 0);
	const now = Date.now();
	const next = prs.reduce((soonest, pr) => {
		const at = pr ? noChecksSettlesAt(pr) : undefined;
		return at !== undefined && at > now ? Math.min(soonest, at) : soonest;
	}, Infinity);
	useEffect(() => {
		if (!Number.isFinite(next)) return;
		const timer = setTimeout(tick, Math.max(0, next - Date.now()) + 50);
		return () => clearTimeout(timer);
	}, [next]);
}
