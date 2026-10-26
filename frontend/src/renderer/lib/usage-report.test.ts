import { describe, expect, it } from "vitest";
import { niceScale, totalsOf, usageByHarness, usageByModel, usageByPeriod, usageByProject, usageSeries, type UsageReportRow, type UsageReportSession } from "./usage-report";

function row(overrides: Partial<UsageReportRow>): UsageReportRow {
	return {
		period: "2026-10-24", harness: "codex", model: "gpt-6.1-sol", speed: "standard", events: 1, pricedEvents: 1,
		inputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, outputTokens: 0,
		inputCostNanos: 0, cacheReadCostNanos: 0, cacheWriteCostNanos: 0, outputCostNanos: 0, costNanos: 0, premiumNanos: 0, cacheSavingsNanos: 0,
		...overrides,
	};
}

function session(overrides: Partial<UsageReportSession>): UsageReportSession {
	return { sessionId: "s", projectId: "app", harness: "codex", title: "s", events: 1, pricedEvents: 1, tokens: 0, costNanos: 0, lastUsedAt: "2026-10-24T10:00:00Z", ...overrides };
}

const rows = [
	row({ period: "2026-10-24", speed: "priority", inputTokens: 100, cacheReadTokens: 900, outputTokens: 10, inputCostNanos: 400, cacheReadCostNanos: 180, outputCostNanos: 200, costNanos: 780, premiumNanos: 390, cacheSavingsNanos: 1620 }),
	row({ period: "2026-10-25", harness: "claude-code", model: "claude-opus-5-5", inputTokens: 50, cacheReadTokens: 500, cacheWriteTokens: 150, outputTokens: 5, inputCostNanos: 200, cacheReadCostNanos: 100, cacheWriteCostNanos: 1200, outputCostNanos: 100, costNanos: 1600 }),
	row({ period: "2026-10-25", model: "glm-unknown", inputTokens: 300, outputTokens: 20, pricedEvents: 0 }),
];
const report = {
	periods: ["2026-10-23", "2026-10-24", "2026-10-25"],
	rows,
	sessions: [session({ sessionId: "a", costNanos: 780, tokens: 1010 }), session({ sessionId: "b", harness: "claude-code", projectId: "web", costNanos: 1600, tokens: 705 }), session({ sessionId: "c", costNanos: 0, tokens: 320 })],
};

describe("usage report aggregation", () => {
	it("sums disjoint parts and groups priority with fast", () => {
		const totals = totalsOf(rows);
		expect(totals.costNanos).toBe(2380);
		expect(totals.costs).toEqual({ input: 600, cacheRead: 280, cacheWrite: 1200, output: 300 });
		expect(totals.tokens).toEqual({ input: 450, cacheRead: 1400, cacheWrite: 150, output: 35 });
		expect(totals.bySpeed.fast).toEqual({ costNanos: 780, tokens: 1010 });
		expect(totals.bySpeed.standard.costNanos).toBe(1600);
		expect(totals.premiumNanos).toBe(390);
		expect(totals.events - totals.pricedEvents).toBe(1);
	});

	it("orders agents by the unit being read and counts their sessions", () => {
		expect(usageByHarness(report, "cost").map((entry) => [entry.harness, entry.sessions])).toEqual([["claude-code", 1], ["codex", 2]]);
		expect(usageByHarness(report, "tokens").map((entry) => entry.harness)).toEqual(["codex", "claude-code"]);
	});

	it("zero-fills periods with no usage so every bar keeps its slot", () => {
		const series = usageSeries(report, "cost");
		expect(series.harnesses).toEqual(["claude-code", "codex"]);
		expect(series.values).toEqual([[0, 0], [0, 780], [1600, 0]]);
	});

	it("reports cache hit rate and leaves an unpriced model's rate empty", () => {
		const models = usageByModel(rows, "cost");
		expect(models.map((model) => model.model)).toEqual(["claude-opus-5-5", "gpt-6.1-sol", "glm-unknown"]);
		expect(models[1].cacheHitRate).toBeCloseTo(0.9);
		expect(models[1].costPerMillionNanos).toBeCloseTo((780 / 1010) * 1_000_000);
		expect(models[2].costPerMillionNanos).toBeNull();
	});

	it("lists periods with usage newest first and groups sessions by project", () => {
		expect(usageByPeriod(report).map((entry) => entry.period)).toEqual(["2026-10-25", "2026-10-24"]);
		expect(usageByProject(report.sessions, "cost")).toEqual([
			{ projectId: "web", sessions: 1, costNanos: 1600, tokens: 705 },
			{ projectId: "app", sessions: 2, costNanos: 780, tokens: 1330 },
		]);
	});

	it("picks round axis steps", () => {
		expect(niceScale(380)).toEqual({ max: 400, ticks: [0, 100, 200, 300, 400] });
		expect(niceScale(1.4e9).max).toBe(1.5e9);
		expect(niceScale(0)).toEqual({ max: 1, ticks: [0, 1] });
	});
});
