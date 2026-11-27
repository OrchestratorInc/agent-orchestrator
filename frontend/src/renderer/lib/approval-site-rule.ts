import type { PendingApproval } from "./approvals";

// Categories whose effect cannot be read in advance: "script" (page scripts,
// unrecognized commands, targets the runtime could not describe) and AO
// Cloud's "unknown" (a command its gate does not judge). The daemon refuses an
// "always allow on this site" rule for them, so no card offers one.
const CATCH_ALL = new Set(["script", "unknown"]);

/** Whether a card may offer "Always allow on <site>" for this request. */
export function offersSiteRule(request: Pick<PendingApproval, "site" | "category">): boolean {
	return Boolean(request.site) && !CATCH_ALL.has(request.category);
}
