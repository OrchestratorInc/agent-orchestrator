import { describe, expect, it } from "vitest";
import { compactContextUsageLabel, compactTokenCount, contextReadout, contextUsageLabel, elapsedLabel, mcpServerFailureLabel, quotaWarning, quotaWindows, resetLabel, workingElapsedLabel } from "./conversationChrome";

describe("mobile Chat conversation chrome", () => {
	it("uses desktop context thresholds and keeps a minimum bar fill visible", () => {
		expect(contextReadout({ contextUsed: 1, contextWindow: 1000, inputTokens: 0, outputTokens: 0, cachedTokens: 0, totalTokens: 1 }))
			.toMatchObject({ percent: 0, fillPercent: 2, severity: "normal" });
		expect(contextReadout({ contextUsed: 70, contextWindow: 100, inputTokens: 0, outputTokens: 0, cachedTokens: 0, totalTokens: 70 })?.severity).toBe("warn");
		expect(contextReadout({ contextUsed: 900, contextWindow: 1000, inputTokens: 0, outputTokens: 0, cachedTokens: 0, totalTokens: 900 })?.severity).toBe("critical");
	});

	it("shows exact context counts only after the provider reports positive use", () => {
		const usage = { contextUsed: 14_863, contextWindow: 1_000_000, inputTokens: 0, outputTokens: 0, cachedTokens: 0, totalTokens: 0 };
		expect(contextUsageLabel(usage)).toBe("14,863 / 1,000,000 context tokens");
		expect(compactContextUsageLabel(usage)).toBe("14.9K / 1M context tokens");
		expect(contextUsageLabel({ ...usage, contextUsed: 0 })).toBe("Context unavailable");
		expect(compactContextUsageLabel({ ...usage, contextUsed: 0 })).toBe("Context unavailable");
		expect(contextUsageLabel({ ...usage, contextWindow: 0 })).toBe("Context unavailable");
		expect(contextUsageLabel()).toBe("Context unavailable");
		expect(contextReadout({ ...usage, contextUsed: 0 })).not.toHaveProperty("percent");
	});

	it("uses the next unit when rounding would show 1000K or 1000M", () => {
		expect(compactTokenCount(999_949)).toBe("999.9K");
		expect(compactTokenCount(999_950)).toBe("1M");
		expect(compactTokenCount(999_999_999)).toBe("1B");
	});

	it("warns on the tighter reported quota window and ignores absent ones", () => {
		expect(quotaWarning({ primaryUsedPercent: -1, secondaryUsedPercent: 82, secondaryResetsInSeconds: 7200, planLabel: "weekly" }))
			.toEqual({ percent: 82, severity: "warn", label: "Weekly limit", resetsInSeconds: 7200 });
		expect(quotaWarning({ primaryUsedPercent: 40, secondaryUsedPercent: -1 })).toBeUndefined();
		expect(quotaWarning({ primaryUsedPercent: 91, secondaryUsedPercent: 80 })?.severity).toBe("critical");
	});

	// Rounding first made 74.6 and 75.1 tie at 75, and the tie kept the window
	// that is still below the threshold, so the warning vanished.
	it("picks the fullest window before rounding", () => {
		expect(quotaWarning({ primaryUsedPercent: 74.6, secondaryUsedPercent: 75.1 })).toMatchObject({ percent: 75, severity: "warn", label: "Long-term limit" });
		expect(quotaWarning({ primaryUsedPercent: 89.6, secondaryUsedPercent: 90.1 })).toMatchObject({ percent: 90, severity: "critical", label: "Long-term limit" });
	});

	it("lists each reported window and never draws an unreported one", () => {
		expect(quotaWindows(undefined)).toEqual([]);
		expect(quotaWindows({ primaryUsedPercent: -1, secondaryUsedPercent: -1 })).toEqual([]);
		const both = quotaWindows({ primaryUsedPercent: 12.4, secondaryUsedPercent: 91, primaryResetsInSeconds: 600, planLabel: "pro" });
		expect(both.map(({ key, label, percent, severity }) => ({ key, label, percent, severity }))).toEqual([
			{ key: "primary", label: "Short-term limit", percent: 12, severity: "normal" },
			{ key: "secondary", label: "Long-term limit", percent: 91, severity: "critical" },
		]);
		expect(both[0]).toMatchObject({ resetsInSeconds: 600, fillPercent: 12.4 });
	});

	it("names a lone window from the provider's label and clamps the reading", () => {
		expect(quotaWindows({ primaryUsedPercent: 104, secondaryUsedPercent: -1, planLabel: "seven_day" })[0]).toMatchObject({ label: "Seven day limit", percent: 100, fillPercent: 100 });
		expect(quotaWindows({ primaryUsedPercent: 0, secondaryUsedPercent: -1 })[0]).toMatchObject({ label: "Usage limit", fillPercent: 2 });
		expect(quotaWindows({ primaryUsedPercent: 5, secondaryUsedPercent: -1, planLabel: "Weekly limit" })[0].label).toBe("Weekly limit");
	});

	it("formats live turn and reset durations without wall-clock assumptions", () => {
		expect(elapsedLabel("2026-08-05T00:00:00Z", Date.parse("2026-08-05T00:02:03Z"))).toBe("2m 3s");
		expect(resetLabel(172_800)).toBe("2d");
	});

	it("starts a running turn at one second and advances from the server timestamp", () => {
		const startedAt = "2026-08-05T00:00:00Z";
		expect(workingElapsedLabel(startedAt, Date.parse(startedAt))).toBe("1s");
		expect(workingElapsedLabel(startedAt, Date.parse("2026-08-05T00:01:02Z"))).toBe("1m 2s");
	});

	it("keeps both the MCP failure class and provider diagnostic", () => {
		expect(mcpServerFailureLabel({ name: "github", status: "failed", failureReason: "auth", error: "token expired" }))
			.toBe("github (auth: token expired)");
	});
});
