import { describe, expect, it } from "vitest";
import type { CloudCpBillingSummary } from "../lib/cloud-cp/types";
import { onCloudPlan } from "./useCloudBilling";

const summary = (fields: Partial<CloudCpBillingSummary>) => ({ enabled: true, entitled: true, exempt: false, plan: "free", ...fields }) as CloudCpBillingSummary;

describe("onCloudPlan", () => {
	it("is on a plan with a contract, whatever the summary says", () => {
		expect(onCloudPlan("contract", undefined)).toBe(true);
	});

	it("is on a plan with a paid self-serve plan, or exempt from paying", () => {
		expect(onCloudPlan("self_serve", summary({ plan: "pro" }))).toBe(true);
		expect(onCloudPlan("self_serve", summary({ plan: "free", limits: { plan: "team" } as CloudCpBillingSummary["limits"] }))).toBe(true);
		expect(onCloudPlan("self_serve", summary({ exempt: true }))).toBe(true);
	});

	it("is not on a plan when Free, not entitled, billing off, or not read yet", () => {
		expect(onCloudPlan("self_serve", summary({ plan: "free" }))).toBe(false);
		expect(onCloudPlan("self_serve", summary({ plan: "pro", entitled: false }))).toBe(false);
		expect(onCloudPlan("self_serve", summary({ plan: "pro", enabled: false }))).toBe(false);
		expect(onCloudPlan("self_serve", undefined)).toBe(false);
		expect(onCloudPlan("off", summary({ plan: "pro" }))).toBe(false);
		expect(onCloudPlan(undefined, summary({ plan: "pro" }))).toBe(false);
	});
});
