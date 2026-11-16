import type { CloudBillingSummary, CloudOffering, CloudPlanLimits } from "./types";

// What the phone shows of the organization's AO Cloud plan: the same numbers
// the desktop's Plans page shows (frontend BillingSection), in the app's own
// words. Nothing here sells a plan: the phone never links to checkout, prices
// or upgrades, so it stays a companion to the desktop under the App Store's
// rules; plans are chosen on the desktop.

/** How full a meter is: the desktop's thresholds, 80% and the limit. */
export type PlanSeverity = "normal" | "warn" | "critical";

export type PlanMeter = {
	key: "window" | "week";
	label: string;
	/** "1.4 of 5 h" */
	value: string;
	/** 0–1, held at 1 past the limit. */
	fraction: number;
	severity: PlanSeverity;
	/** When the window lifts: "Resets in 2 h 10 m". */
	detail?: string;
};

export type CloudPlanView =
	/** Plans don't apply: no Stripe on the deployment, or nothing the organization runs is billed. */
	| { kind: "none" }
	/** No entitled subscription: Free, which runs no cloud sessions. */
	| { kind: "free" }
	| {
		kind: "paid";
		name: string;
		/** Said beside the name when it changes what the plan does: a failed payment, an end date, a trial. */
		status?: string;
		tone: "default" | "bad";
		meters: PlanMeter[];
		running?: { value: string; full: boolean };
		total?: { value: string; full: boolean };
	};

/** Plans apply only to a self-serve organization on a deployment with Stripe. */
function plansApply(summary: CloudBillingSummary): boolean {
	return summary.enabled && (summary.billingMode === undefined || summary.billingMode === "self_serve") && !summary.exempt;
}

export function cloudPlanView(summary: CloudBillingSummary, now = Date.now()): CloudPlanView {
	if (!plansApply(summary)) return { kind: "none" };
	// Free is the entitlement's answer; an entitled plan whose limits didn't
	// come is still that plan, with nothing to measure.
	if (!summary.entitled) return { kind: "free" };
	const { limits, usage } = summary;
	const status = summary.subscriptionStatus === "past_due"
		? "Payment failed"
		: summary.cancelAt
			? `Ends ${shortDay(summary.cancelAt)}`
			: summary.subscriptionStatus === "trialing" ? "Trial" : undefined;
	const view: CloudPlanView = {
		kind: "paid",
		name: planName(limits?.plan || summary.plan),
		tone: summary.subscriptionStatus === "past_due" ? "bad" : "default",
		meters: [],
		...(status ? { status } : {}),
	};
	if (!usage || !limits) return view;
	view.meters = [
		meter("window", windowLabel(usage.windowHours || limits.windowHours), usage.windowUsedMinutes, usage.windowLimitMinutes, resetIn(usage.windowResetsAt, now)),
		meter("week", "This week", usage.weeklyUsedMinutes, usage.weeklyLimitMinutes, resetOn(usage.weeklyResetsAt, now)),
	];
	const slots = runningSlots(limits);
	const running = usage.runningOrchestrators + usage.runningWorkers;
	view.running = { value: `${running} of ${slots}`, full: running >= slots };
	const active = usage.activeOrchestrators + usage.activeWorkers;
	view.total = { value: `${active} of ${limits.maxActiveSandboxes}`, full: active >= limits.maxActiveSandboxes };
	return view;
}

/**
 * Whether to offer what only matters where the organization can run cloud
 * agents (the coding agents' sign-ins, GitHub access, new cloud projects):
 * never on Free, where a project could never run anything. Undefined until
 * the plan is read, so nothing flashes in on Free or out on a paid plan; a
 * plan that can't be read leaves them offered, as the control plane still
 * decides.
 */
export function cloudAgentsOffered(
	plan: { status: "loading" | "ready" | "failed"; summary?: CloudBillingSummary } | undefined,
	offering?: Pick<CloudOffering, "customCoder">,
): boolean | undefined {
	// Coder that the organization runs free still works on Free.
	if (offering?.customCoder) return true;
	if (!plan || plan.status === "failed" || !plan.summary) return plan?.status === "loading" ? undefined : true;
	return cloudPlanView(plan.summary).kind !== "free";
}

/**
 * Whether a session on `provider` counts against the plan, as the control
 * plane decides (sandboxBilledTx): Freestyle always, Coder unless the
 * organization runs it free (its own Coder, or the coder capability:
 * `customCoder`), and no other provider. A project that names none runs on
 * the organization's default. Unknown, it counts, so Free still says so.
 */
export function cloudSessionBilled(provider: string | undefined, offering: Pick<CloudOffering, "customCoder" | "defaultProvider"> | undefined): boolean {
	if (!offering) return true;
	const effective = provider || offering.defaultProvider;
	if (effective === "freestyle") return true;
	if (effective === "coder") return !offering.customCoder;
	return false;
}

/**
 * Why a new cloud task can't start, known before it is sent: Free, or a
 * session-hour window spent. Full slots are left to the control plane, which
 * counts them at the moment of the request; a spent window whose reset time
 * has passed is let through, since the numbers it was read with are old.
 */
export function cloudPlanBlock(summary: CloudBillingSummary | undefined, now = Date.now(), billed = true): { reason: "plan" | "window" | "week"; message: string } | undefined {
	// A session the plan doesn't pay for (free Coder) isn't the plan's to stop.
	if (!summary || !billed || !plansApply(summary)) return undefined;
	if (!summary.entitled) return { reason: "plan", message: "Your organization's Free plan doesn't include cloud agents." };
	const usage = summary.usage;
	if (!usage) return undefined;
	if (spent(usage.windowUsedMinutes, usage.windowLimitMinutes, usage.windowResetsAt, now)) {
		return { reason: "window", message: withReset(`You've used your plan's ${formatPlanHours(usage.windowLimitMinutes)} session hours for now.`, resetIn(usage.windowResetsAt, now)) };
	}
	if (spent(usage.weeklyUsedMinutes, usage.weeklyLimitMinutes, usage.weeklyResetsAt, now)) {
		return { reason: "week", message: withReset(`You've used your plan's ${formatPlanHours(usage.weeklyLimitMinutes)} session hours this week.`, resetOn(usage.weeklyResetsAt, now)) };
	}
	return undefined;
}

/**
 * What Free includes, beside its name: no cloud agents, or only Coder where
 * the organization runs that free (its own Coder, or the coder capability).
 */
export function freePlanSummary(offering: Pick<CloudOffering, "customCoder"> | undefined): string {
	return offering?.customCoder ? "Coder only" : "No cloud agents";
}

/** A refusal's headline, by the code the control plane refused with. */
export function planRefusalTitle(code: string | undefined): string {
	switch (code) {
		case "PLAN_REQUIRED": return "Cloud agents aren't on your plan";
		case "WINDOW_LIMIT_REACHED": return "Usage limit reached";
		case "WEEKLY_LIMIT_REACHED": return "Weekly limit reached";
		case "WORKER_SLOTS_FULL": return "All running sessions in use";
		case "ORCHESTRATOR_SLOTS_FULL": return "Orchestrator already running";
		case "SESSION_LIMIT_REACHED": return "Session limit reached";
		default: return "Your plan";
	}
}

/** Session-minutes as hours, one decimal and none when whole: 252 → "4.2", 360 → "6". */
export function formatPlanHours(minutes: number): string {
	const hours = Math.round((minutes / 60) * 10) / 10;
	return Number.isInteger(hours) ? String(hours) : hours.toFixed(1);
}

export function planName(plan: string): string {
	const trimmed = plan.trim();
	return trimmed ? trimmed.charAt(0).toUpperCase() + trimmed.slice(1) : "Plan";
}

/** Every running session the plan allows; a plan from before running caps runs them all. */
function runningSlots(limits: CloudPlanLimits): number {
	return limits.maxRunningSandboxes && limits.maxRunningSandboxes > 0 ? limits.maxRunningSandboxes : limits.maxActiveSandboxes;
}

function meter(key: PlanMeter["key"], label: string, used: number, limit: number, detail: string | undefined): PlanMeter {
	const fraction = limit > 0 ? Math.min(1, used / limit) : 0;
	return {
		key,
		label,
		value: `${formatPlanHours(used)} of ${formatPlanHours(limit)} h`,
		fraction,
		severity: fraction >= 1 ? "critical" : fraction >= 0.8 ? "warn" : "normal",
		...(detail ? { detail } : {}),
	};
}

function windowLabel(hours: number): string {
	return hours === 1 ? "Last hour" : `Last ${hours} hours`;
}

function spent(used: number, limit: number, resetsAt: string | undefined, now: number): boolean {
	if (limit <= 0 || used < limit) return false;
	const at = resetsAt ? Date.parse(resetsAt) : NaN;
	return !Number.isFinite(at) || at > now;
}

function withReset(message: string, reset: string | undefined): string {
	return reset ? `${message} ${reset}.` : message;
}

/** A short window's reset, counted down: "Resets in 2 h 10 m". */
function resetIn(iso: string | undefined, now: number): string | undefined {
	const at = iso ? Date.parse(iso) : NaN;
	if (!Number.isFinite(at)) return undefined;
	const ms = at - now;
	if (ms <= 0) return "Resets any moment now";
	const total = Math.ceil(ms / 60_000);
	const hours = Math.floor(total / 60);
	const minutes = total % 60;
	return hours === 0 ? `Resets in ${minutes} m` : `Resets in ${hours} h ${minutes} m`;
}

/** The week's reset, as a day and time: "Resets Mon, 16 Nov, 10:00". */
function resetOn(iso: string | undefined, now: number): string | undefined {
	const at = iso ? Date.parse(iso) : NaN;
	if (!Number.isFinite(at)) return undefined;
	if (at <= now) return "Resets any moment now";
	return `Resets ${new Date(at).toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}`;
}

function shortDay(iso: string): string {
	const at = Date.parse(iso);
	return Number.isFinite(at) ? new Date(at).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" }) : iso;
}
