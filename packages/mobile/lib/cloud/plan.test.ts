import { describe, expect, it } from "vitest";
import { cloudAgentsOffered, cloudPlanBlock, cloudPlanView, cloudSessionBilled, formatPlanHours, freePlanSummary, planRefusalTitle } from "./plan";
import type { CloudBillingSummary } from "./types";

const NOW = Date.parse("2026-11-11T10:00:00Z");
const inMinutes = (minutes: number) => new Date(NOW + minutes * 60_000).toISOString();

const limits = { plan: "starter", maxActiveSandboxes: 10, maxRunningSandboxes: 3, orchestratorSlots: 1, windowHours: 5, windowSessionHours: 5, weeklySessionHours: 40, manualResetsPerMonth: 2 };
const usage = {
	windowUsedMinutes: 84, windowLimitMinutes: 300, windowHours: 5, windowResetsAt: inMinutes(130),
	weeklyUsedMinutes: 720, weeklyLimitMinutes: 2400, weeklyResetsAt: "2026-11-16T04:30:00Z",
	manualResetsAllowed: 2, manualResetsUsed: 0,
	activeOrchestrators: 1, activeWorkers: 3, runningOrchestrators: 1, runningWorkers: 1,
};
const paid: CloudBillingSummary = { enabled: true, billingMode: "self_serve", plan: "starter", subscriptionStatus: "active", entitled: true, limits, usage };
const free: CloudBillingSummary = { enabled: true, billingMode: "self_serve", plan: "free", entitled: false };

describe("cloudPlanView", () => {
	it("shows nothing where plans don't apply: no Stripe, or nothing billed", () => {
		expect(cloudPlanView({ ...paid, enabled: false }, NOW)).toEqual({ kind: "none" });
		expect(cloudPlanView({ ...paid, billingMode: "off" }, NOW)).toEqual({ kind: "none" });
		expect(cloudPlanView({ ...paid, billingMode: "contract" }, NOW)).toEqual({ kind: "none" });
	});

	it("puts an organization without an entitled subscription on Free, which runs no cloud agents", () => {
		expect(cloudPlanView(free, NOW)).toEqual({ kind: "free" });
		// A subscription that lapsed keeps its plan name on the organization until Stripe says otherwise.
		expect(cloudPlanView({ ...paid, entitled: false, subscriptionStatus: "canceled" }, NOW)).toEqual({ kind: "free" });
	});

	it("shows a paid plan's name, its two session-hour windows and its session counts", () => {
		const view = cloudPlanView(paid, NOW);
		expect(view).toMatchObject({ kind: "paid", name: "Starter", tone: "default" });
		if (view.kind !== "paid") throw new Error("paid");
		expect(view.meters).toEqual([
			{ key: "window", label: "Last 5 hours", value: "1.4 of 5 h", fraction: 0.28, severity: "normal", detail: "Resets in 2 h 10 m" },
			{ key: "week", label: "This week", value: "12 of 40 h", fraction: 0.3, severity: "normal", detail: expect.stringMatching(/^Resets /) },
		]);
		expect(view.running).toEqual({ value: "2 of 3", full: false });
		expect(view.total).toEqual({ value: "4 of 10", full: false });
	});

	it("warns from 80% and goes red when a limit is reached, as the desktop does", () => {
		const near = cloudPlanView({ ...paid, usage: { ...usage, windowUsedMinutes: 250 } }, NOW);
		const full = cloudPlanView({ ...paid, usage: { ...usage, weeklyUsedMinutes: 2400, runningWorkers: 2, activeWorkers: 9 } }, NOW);
		if (near.kind !== "paid" || full.kind !== "paid") throw new Error("paid");
		expect(near.meters[0]).toMatchObject({ severity: "warn", value: "4.2 of 5 h" });
		expect(full.meters[1]).toMatchObject({ severity: "critical", fraction: 1 });
		expect(full.running).toMatchObject({ value: "3 of 3", full: true });
		expect(full.total).toMatchObject({ value: "10 of 10", full: true });
	});

	it("says beside the name when a payment failed, a plan is set to end, or a trial runs", () => {
		expect(cloudPlanView({ ...paid, subscriptionStatus: "past_due" }, NOW)).toMatchObject({ status: "Payment failed", tone: "bad" });
		expect(cloudPlanView({ ...paid, subscriptionStatus: "trialing" }, NOW)).toMatchObject({ status: "Trial", tone: "default" });
		expect(cloudPlanView({ ...paid, cancelAt: "2026-11-24T00:00:00Z" }, NOW)).toMatchObject({ status: expect.stringMatching(/^Ends \S/) });
		expect(cloudPlanView({ ...paid, subscriptionStatus: "past_due", cancelAt: "2026-11-24T00:00:00Z" }, NOW)).toMatchObject({ status: "Payment failed" });
		expect(cloudPlanView(paid, NOW)).not.toHaveProperty("status");
	});

	it("counts a plan set up before running caps as running all its sessions", () => {
		const view = cloudPlanView({ ...paid, limits: { ...limits, maxRunningSandboxes: 0, maxActiveSandboxes: 4 } }, NOW);
		expect(view.kind === "paid" && view.running?.value).toBe("2 of 4");
	});

	it("leaves out the usage it wasn't sent", () => {
		const view = cloudPlanView({ ...paid, usage: undefined }, NOW);
		expect(view).toMatchObject({ kind: "paid", name: "Starter", meters: [] });
		expect(view.kind === "paid" && view.running).toBeUndefined();
	});
});

// The contract lets an entitled plan come without its limits: that is a paid
// plan whose numbers aren't known, not Free.
describe("an entitled plan without its limits", () => {
	const unknown: CloudBillingSummary = { enabled: true, billingMode: "self_serve", plan: "starter", entitled: true };
	it("is shown as the plan it is, with nothing to measure", () => {
		expect(cloudPlanView(unknown, NOW)).toEqual({ kind: "paid", name: "Starter", tone: "default", meters: [] });
	});
	it("neither blocks a task nor hides Cloud", () => {
		expect(cloudPlanBlock(unknown, NOW)).toBeUndefined();
		expect(cloudAgentsOffered({ status: "ready", summary: unknown })).toBe(true);
	});
});

// Plans sell Freestyle, and Coder for an organization that doesn't run it
// free; one with its own Coder or the coder capability (customCoder) keeps
// its Coder sessions on Free.
describe("what a plan pays for", () => {
	const coderFree = { defaultProvider: "coder", customCoder: true };
	it("counts Freestyle always, Coder unless the organization runs it free, and nothing else", () => {
		expect(cloudSessionBilled("freestyle", coderFree)).toBe(true);
		expect(cloudSessionBilled("coder", coderFree)).toBe(false);
		expect(cloudSessionBilled("coder", { defaultProvider: "freestyle", customCoder: false })).toBe(true);
		expect(cloudSessionBilled("docker", coderFree)).toBe(false);
		// The project names none: the organization's default.
		expect(cloudSessionBilled(undefined, coderFree)).toBe(false);
		// Not known yet: as billed, so Free still says so.
		expect(cloudSessionBilled(undefined, undefined)).toBe(true);
	});
	// The page offers coding agents and projects to a Free organization with
	// free Coder, so Free can't say it has no cloud agents there.
	it("says what Free includes: Coder only where Coder runs free, else no cloud agents", () => {
		expect(freePlanSummary(coderFree)).toBe("Coder only");
		expect(freePlanSummary({ customCoder: false })).toBe("No cloud agents");
		expect(freePlanSummary(undefined)).toBe("No cloud agents");
	});

	it("lets a Free organization start a task on its free Coder, and keeps Cloud offered", () => {
		expect(cloudPlanBlock(free, NOW, cloudSessionBilled("coder", coderFree))).toBeUndefined();
		expect(cloudPlanBlock(free, NOW, cloudSessionBilled("freestyle", coderFree))).toMatchObject({ reason: "plan" });
		expect(cloudAgentsOffered({ status: "ready", summary: free }, coderFree)).toBe(true);
		expect(cloudAgentsOffered({ status: "ready", summary: free }, { customCoder: false })).toBe(false);
	});
});

describe("cloudPlanBlock", () => {
	it("blocks a new cloud task on Free, with nothing to wait for", () => {
		expect(cloudPlanBlock(free, NOW)).toEqual({ reason: "plan", message: "Your organization's Free plan doesn't include cloud agents." });
	});

	it("blocks it while a usage window is spent, and says when it lifts", () => {
		expect(cloudPlanBlock({ ...paid, usage: { ...usage, windowUsedMinutes: 300 } }, NOW)).toEqual({
			reason: "window", message: "You've used your plan's 5 session hours for now. Resets in 2 h 10 m.",
		});
		expect(cloudPlanBlock({ ...paid, usage: { ...usage, weeklyUsedMinutes: 2400 } }, NOW)).toMatchObject({ reason: "week", message: expect.stringMatching(/^You've used your plan's 40 session hours this week\. Resets /) });
	});

	it("lets a spent window go once its reset time has passed, since the numbers it was read with are old", () => {
		expect(cloudPlanBlock({ ...paid, usage: { ...usage, windowUsedMinutes: 300, windowResetsAt: inMinutes(-1) } }, NOW)).toBeUndefined();
	});

	it("leaves full slots to the control plane, which counts them at the moment of the request", () => {
		expect(cloudPlanBlock({ ...paid, usage: { ...usage, runningWorkers: 2, activeWorkers: 9 } }, NOW)).toBeUndefined();
	});

	it("blocks nothing where plans don't apply", () => {
		expect(cloudPlanBlock({ ...free, enabled: false }, NOW)).toBeUndefined();
		expect(cloudPlanBlock({ ...free, billingMode: "off" }, NOW)).toBeUndefined();
		expect(cloudPlanBlock(undefined, NOW)).toBeUndefined();
	});
});

describe("cloudAgentsOffered", () => {
	it("offers the coding agents only where the organization can run cloud agents", () => {
		expect(cloudAgentsOffered({ status: "ready", summary: paid })).toBe(true);
		expect(cloudAgentsOffered({ status: "ready", summary: free })).toBe(false);
		// Plans that don't apply never take them away.
		expect(cloudAgentsOffered({ status: "ready", summary: { ...free, enabled: false } })).toBe(true);
	});

	it("waits for the plan rather than showing them and taking them away", () => {
		expect(cloudAgentsOffered({ status: "loading" })).toBeUndefined();
	});

	it("leaves them offered when the plan can't be read, since the control plane still decides", () => {
		expect(cloudAgentsOffered({ status: "failed" })).toBe(true);
		expect(cloudAgentsOffered(undefined)).toBe(true);
	});
});

describe("plan copy", () => {
	it("writes session-hours as the desktop does: one decimal, none when whole", () => {
		expect(formatPlanHours(252)).toBe("4.2");
		expect(formatPlanHours(360)).toBe("6");
		expect(formatPlanHours(0)).toBe("0");
	});

	it("titles a refusal by what stopped it", () => {
		expect(planRefusalTitle("PLAN_REQUIRED")).toBe("Cloud agents aren't on your plan");
		expect(planRefusalTitle("WINDOW_LIMIT_REACHED")).toBe("Usage limit reached");
		expect(planRefusalTitle("WEEKLY_LIMIT_REACHED")).toBe("Weekly limit reached");
		expect(planRefusalTitle("WORKER_SLOTS_FULL")).toBe("All running sessions in use");
		expect(planRefusalTitle("ORCHESTRATOR_SLOTS_FULL")).toBe("Orchestrator already running");
		expect(planRefusalTitle("SESSION_LIMIT_REACHED")).toBe("Session limit reached");
		expect(planRefusalTitle(undefined)).toBe("Your plan");
	});
});
