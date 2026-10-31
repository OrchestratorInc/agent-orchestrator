import type { components } from "../../api/schema";

export type UsageReport = components["schemas"]["UsageReportResponse"];
export type UsageReportRow = components["schemas"]["UsageReportRowResponse"];
export type UsageReportSession = components["schemas"]["UsageReportSessionResponse"];

/** What the Cost and Tokens views measure. */
export type UsageUnit = "cost" | "tokens";

/** The four disjoint token types. Costs use the same parts, in nano-USD. */
export type UsageParts = { input: number; cacheRead: number; cacheWrite: number; output: number };

/** Priority, fast and ultrafast all bill above standard, so they read as one "Fast". */
export type SpeedGroup = "standard" | "fast" | "flex" | "unknown";

export function speedGroup(speed: string): SpeedGroup {
	if (speed === "standard") return "standard";
	if (speed === "priority" || speed === "fast" || speed === "ultrafast") return "fast";
	if (speed === "flex") return "flex";
	return "unknown";
}

export function rowTokens(row: Pick<UsageReportRow, "inputTokens" | "cacheReadTokens" | "cacheWriteTokens" | "outputTokens">): number {
	return row.inputTokens + row.cacheReadTokens + row.cacheWriteTokens + row.outputTokens;
}

export function rowValue(row: UsageReportRow, unit: UsageUnit): number {
	return unit === "cost" ? row.costNanos : rowTokens(row);
}

export type UsageTotals = {
	costNanos: number;
	tokens: UsageParts;
	costs: UsageParts;
	premiumNanos: number;
	cacheSavingsNanos: number;
	events: number;
	pricedEvents: number;
	bySpeed: Record<SpeedGroup, { costNanos: number; tokens: number }>;
};

function emptyParts(): UsageParts {
	return { input: 0, cacheRead: 0, cacheWrite: 0, output: 0 };
}

export function totalsOf(rows: readonly UsageReportRow[]): UsageTotals {
	const totals: UsageTotals = {
		costNanos: 0, tokens: emptyParts(), costs: emptyParts(), premiumNanos: 0, cacheSavingsNanos: 0, events: 0, pricedEvents: 0,
		bySpeed: { standard: { costNanos: 0, tokens: 0 }, fast: { costNanos: 0, tokens: 0 }, flex: { costNanos: 0, tokens: 0 }, unknown: { costNanos: 0, tokens: 0 } },
	};
	for (const row of rows) {
		totals.costNanos += row.costNanos;
		totals.tokens.input += row.inputTokens;
		totals.tokens.cacheRead += row.cacheReadTokens;
		totals.tokens.cacheWrite += row.cacheWriteTokens;
		totals.tokens.output += row.outputTokens;
		totals.costs.input += row.inputCostNanos;
		totals.costs.cacheRead += row.cacheReadCostNanos;
		totals.costs.cacheWrite += row.cacheWriteCostNanos;
		totals.costs.output += row.outputCostNanos;
		totals.premiumNanos += row.premiumNanos;
		totals.cacheSavingsNanos += row.cacheSavingsNanos;
		totals.events += row.events;
		totals.pricedEvents += row.pricedEvents;
		const speed = totals.bySpeed[speedGroup(row.speed)];
		speed.costNanos += row.costNanos;
		speed.tokens += rowTokens(row);
	}
	return totals;
}

export function partsTotal(parts: UsageParts): number {
	return parts.input + parts.cacheRead + parts.cacheWrite + parts.output;
}

export type HarnessUsage = { harness: string; sessions: number; costNanos: number; tokens: number };

/** One line per agent, the largest first in the unit being read. */
export function usageByHarness(report: Pick<UsageReport, "rows" | "sessions">, unit: UsageUnit): HarnessUsage[] {
	const byHarness = new Map<string, HarnessUsage>();
	const entry = (harness: string) => {
		let found = byHarness.get(harness);
		if (!found) {
			found = { harness, sessions: 0, costNanos: 0, tokens: 0 };
			byHarness.set(harness, found);
		}
		return found;
	};
	for (const row of report.rows) {
		const found = entry(row.harness);
		found.costNanos += row.costNanos;
		found.tokens += rowTokens(row);
	}
	for (const session of report.sessions) entry(session.harness).sessions += 1;
	return [...byHarness.values()].sort((a, b) => {
		const primary = unit === "cost" ? b.costNanos - a.costNanos : b.tokens - a.tokens;
		return primary !== 0 ? primary : a.harness.localeCompare(b.harness);
	});
}

/** values[period][harness], zero-filled for periods with no usage. */
export type UsageSeries = { periods: string[]; harnesses: string[]; values: number[][] };

export function usageSeries(report: Pick<UsageReport, "periods" | "rows" | "sessions">, unit: UsageUnit): UsageSeries {
	const harnesses = usageByHarness(report, unit).map((entry) => entry.harness);
	const harnessIndex = new Map(harnesses.map((harness, index) => [harness, index]));
	const periodIndex = new Map(report.periods.map((period, index) => [period, index]));
	const values = report.periods.map(() => harnesses.map(() => 0));
	for (const row of report.rows) {
		const p = periodIndex.get(row.period);
		const h = harnessIndex.get(row.harness);
		if (p === undefined || h === undefined) continue;
		values[p][h] += rowValue(row, unit);
	}
	return { periods: report.periods, harnesses, values };
}

export type ModelUsage = {
	model: string;
	harness: string;
	costNanos: number;
	tokens: number;
	events: number;
	pricedEvents: number;
	/** Share of input tokens served from the cache, 0–1. */
	cacheHitRate: number;
	/** nano-USD per million processed tokens over the priced events; null when none were priced. */
	costPerMillionNanos: number | null;
};

export function usageByModel(rows: readonly UsageReportRow[], unit: UsageUnit): ModelUsage[] {
	const byModel = new Map<string, ModelUsage & { input: number; cacheRead: number; pricedTokens: number }>();
	for (const row of rows) {
		let found = byModel.get(row.model);
		if (!found) {
			found = { model: row.model, harness: row.harness, costNanos: 0, tokens: 0, events: 0, pricedEvents: 0, cacheHitRate: 0, costPerMillionNanos: null, input: 0, cacheRead: 0, pricedTokens: 0 };
			byModel.set(row.model, found);
		}
		const tokens = rowTokens(row);
		found.costNanos += row.costNanos;
		found.tokens += tokens;
		found.events += row.events;
		found.pricedEvents += row.pricedEvents;
		found.input += row.inputTokens + row.cacheReadTokens + row.cacheWriteTokens;
		found.cacheRead += row.cacheReadTokens;
		if (row.pricedEvents > 0) found.pricedTokens += tokens;
	}
	return [...byModel.values()]
		.map(({ input, cacheRead, pricedTokens, ...model }) => ({
			...model,
			cacheHitRate: input > 0 ? cacheRead / input : 0,
			costPerMillionNanos: model.pricedEvents > 0 && pricedTokens > 0 ? (model.costNanos / pricedTokens) * 1_000_000 : null,
		}))
		.sort((a, b) => (unit === "cost" ? b.costNanos - a.costNanos : b.tokens - a.tokens) || b.tokens - a.tokens || a.model.localeCompare(b.model));
}

export type PeriodUsage = { period: string; costNanos: number; tokens: number; events: number };

/** Periods with usage, newest first. */
export function usageByPeriod(report: Pick<UsageReport, "periods" | "rows">): PeriodUsage[] {
	const byPeriod = new Map<string, PeriodUsage>();
	for (const row of report.rows) {
		let found = byPeriod.get(row.period);
		if (!found) {
			found = { period: row.period, costNanos: 0, tokens: 0, events: 0 };
			byPeriod.set(row.period, found);
		}
		found.costNanos += row.costNanos;
		found.tokens += rowTokens(row);
		found.events += row.events;
	}
	const order = new Map(report.periods.map((period, index) => [period, index]));
	return [...byPeriod.values()].sort((a, b) => (order.get(b.period) ?? 0) - (order.get(a.period) ?? 0));
}

export type ProjectUsage = { projectId: string; sessions: number; costNanos: number; tokens: number };

export function usageByProject(sessions: readonly UsageReportSession[], unit: UsageUnit): ProjectUsage[] {
	const byProject = new Map<string, ProjectUsage>();
	for (const session of sessions) {
		const projectId = session.projectId ?? "";
		let found = byProject.get(projectId);
		if (!found) {
			found = { projectId, sessions: 0, costNanos: 0, tokens: 0 };
			byProject.set(projectId, found);
		}
		found.sessions += 1;
		found.costNanos += session.costNanos;
		found.tokens += session.tokens;
	}
	return [...byProject.values()].sort((a, b) => (unit === "cost" ? b.costNanos - a.costNanos : b.tokens - a.tokens) || a.projectId.localeCompare(b.projectId));
}

/** An axis maximum on a 1/2/2.5/5 × 10ⁿ step, with evenly spaced ticks from 0. */
export function niceScale(peak: number, tickCount = 4): { max: number; ticks: number[] } {
	if (!Number.isFinite(peak) || peak <= 0) return { max: 1, ticks: [0, 1] };
	const raw = peak / tickCount;
	const magnitude = 10 ** Math.floor(Math.log10(raw));
	const step = [1, 2, 2.5, 5, 10].map((factor) => factor * magnitude).find((candidate) => candidate >= raw) ?? 10 * magnitude;
	const max = Math.ceil(peak / step) * step;
	const ticks: number[] = [];
	for (let value = 0; value <= max + step / 2; value += step) ticks.push(Number(value.toPrecision(12)));
	return { max, ticks };
}
