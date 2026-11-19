import { afterEach, describe, expect, it, vi } from "vitest";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import { PLAN_SHEET_COOLDOWN_MS, presentPlanRefusal, resetPlanRefusalCooldown } from "./planRefusal";
import { clearCloudPlan, loadCloudPlan } from "./planStore";

afterEach(() => {
	resetPlanRefusalCooldown();
	clearCloudPlan();
});

const weekly = new CloudError("You've reached your plan's weekly usage limit.", { status: 429, code: "WEEKLY_LIMIT_REACHED", resetsAt: "2026-11-16T04:30:00Z" });

describe("presentPlanRefusal", () => {
	it("opens the plan sheet with the refusal's code, words and reset time", () => {
		const open = vi.fn();
		expect(presentPlanRefusal(weekly, open, 1_000)).toBe(true);
		expect(open).toHaveBeenCalledExactlyOnceWith({ code: "WEEKLY_LIMIT_REACHED", message: "You've reached your plan's weekly usage limit.", resetsAt: "2026-11-16T04:30:00Z" });
	});

	it("opens it at most once a minute, so a retrying request can't keep reopening it", () => {
		const open = vi.fn();
		presentPlanRefusal(weekly, open, 1_000);
		expect(presentPlanRefusal(weekly, open, 1_000 + PLAN_SHEET_COOLDOWN_MS - 1)).toBe(false);
		expect(presentPlanRefusal(weekly, open, 1_000 + PLAN_SHEET_COOLDOWN_MS)).toBe(true);
		expect(open).toHaveBeenCalledTimes(2);
	});

	it("has the plan read again even when the sheet stays shut", async () => {
		const getBilling = vi.fn(async () => ({ enabled: true, billingMode: "self_serve" as const, plan: "starter", entitled: true }));
		const client = { getBilling } as unknown as CloudClient;
		await loadCloudPlan(client, "o");
		presentPlanRefusal(weekly, vi.fn(), 1_000);
		presentPlanRefusal(weekly, vi.fn(), 2_000);
		await loadCloudPlan(client, "o");
		expect(getBilling).toHaveBeenCalledTimes(2);
	});
});
