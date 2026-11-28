// The AO Cloud plan an install is on, from a fixed vocabulary, for PostHog
// (cloud_plan, cloud_paid), Sentry (the plan tag) and the daemon. Never a
// price, an org name or a Stripe id. "none" is signed out or no billing;
// "unknown" is signed in with a plan not known yet, or a value outside the list.
export const CLOUD_PLANS = ["free", "starter", "pro", "enterprise", "none", "unknown"] as const;
export type CloudPlan = (typeof CLOUD_PLANS)[number];

const PLANS = new Set<string>(CLOUD_PLANS);
const PAID_PLANS = new Set<string>(["starter", "pro", "enterprise"]);

export function cloudPlan(value: unknown): CloudPlan {
	return typeof value === "string" && PLANS.has(value) ? (value as CloudPlan) : "unknown";
}

/** A plan whose errors Sentry always sends, outside the one-in-ten install sample. */
export function isPaidCloudPlan(plan: unknown): boolean {
	return typeof plan === "string" && PAID_PLANS.has(plan);
}

type BillingSummaryShape = {
	enabled?: boolean;
	entitled?: boolean;
	exempt?: boolean;
	billingMode?: string;
	plan?: string;
	limits?: { plan?: string };
};

/** Whether the org pays through self-serve billing: enabled, entitled, not exempt, not Free. */
export function isPaidBillingSummary(summary: BillingSummaryShape | undefined): boolean {
	if (!summary?.enabled || !summary.entitled || summary.exempt) return false;
	if (summary.billingMode !== undefined && summary.billingMode !== "self_serve") return false;
	const plan = summary.limits?.plan ?? summary.plan;
	return Boolean(plan) && plan !== "free";
}

/**
 * The plan a fresh billing summary shows. A paid org gets its plan; an exempt
 * or contract org gets its plan if it is one of ours, else unknown; a
 * self-serve org that is not entitled is on Free.
 */
export function billingSummaryPlan(summary: BillingSummaryShape): CloudPlan {
	if (!summary.enabled) return "none";
	const plan = cloudPlan(summary.limits?.plan ?? summary.plan);
	if (plan === "none") return "unknown";
	if (isPaidBillingSummary(summary)) return plan;
	if (summary.exempt || (summary.billingMode !== undefined && summary.billingMode !== "self_serve")) return plan;
	return summary.entitled ? plan : "free";
}
