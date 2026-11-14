import { describe, expect, it } from "vitest";

import { contextMeterModel, quotaChipModel } from "./contextMeter";
import type { ConversationUsage } from "./types";

const usage = (contextUsed: number, contextWindow = 100): ConversationUsage => ({
	contextUsed,
	contextWindow,
	inputTokens: 0,
	outputTokens: 0,
	cachedTokens: 0,
	totalTokens: contextUsed,
});

describe("contextMeterModel", () => {
	it("stays hidden while there is budget to spare", () => {
		expect(contextMeterModel(usage(10))).toBeNull();
		expect(contextMeterModel(usage(69))).toBeNull();
	});

	it("appears at the warn threshold", () => {
		const model = contextMeterModel(usage(70));
		expect(model?.severity).toBe("warn");
		expect(model?.percent).toBe(70);
		expect(model?.label).toBe("70% context");
	});

	it("gives the meter a value screen readers can read", () => {
		expect(contextMeterModel(usage(83))?.accessibilityValue).toEqual({ min: 0, max: 100, now: 83, text: "83% used" });
	});

	it("escalates to critical at 90%", () => {
		expect(contextMeterModel(usage(90))?.severity).toBe("critical");
		expect(contextMeterModel(usage(97))?.severity).toBe("critical");
	});

	it("shows nothing without usage", () => {
		expect(contextMeterModel(undefined)).toBeNull();
	});

	it("shows nothing when the harness reports no context window", () => {
		expect(contextMeterModel(usage(5000, 0))).toBeNull();
	});

	it("keeps the bar visible rather than letting it round to nothing", () => {
		const model = contextMeterModel(usage(7001, 10000));
		expect(model?.fillPercent).toBeGreaterThanOrEqual(2);
	});
});

describe("quotaChipModel", () => {
	it("stays away below 75%", () => {
		expect(quotaChipModel(undefined)).toBeNull();
		expect(quotaChipModel({ primaryUsedPercent: 74, secondaryUsedPercent: -1 })).toBeNull();
	});

	it("reads the fullest window and when it refills", () => {
		expect(quotaChipModel({ primaryUsedPercent: 40, secondaryUsedPercent: 76, secondaryResetsInSeconds: 82_800 })).toEqual({
			severity: "warn",
			label: "76% quota · 23h",
			accessibilityLabel: "Long-term limit, 76% used, resets in 23h",
		});
	});

	it("turns critical at 90% and drops an unknown reset", () => {
		expect(quotaChipModel({ primaryUsedPercent: 92, secondaryUsedPercent: -1, planLabel: "five_hour" })).toEqual({
			severity: "critical",
			label: "92% quota",
			accessibilityLabel: "Five hour limit, 92% used",
		});
	});
});
