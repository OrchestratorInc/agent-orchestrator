import { contextReadout, quotaWarning, resetLabel, type Severity } from "./conversationChrome";
import type { ConversationRateLimits, ConversationUsage } from "./types";

/**
 * The context-budget meter shown in the composer's meta row.
 *
 * Deliberately absent below 70%. A permanent token gauge is chrome nobody
 * reads; one that appears as the window fills is information, and it arrives
 * before a compaction surprises you mid-thread.
 */
export type ContextMeterModel = {
	/** Whole percent of the context window used. */
	percent: number;
	/** Bar width as a percent — floored so a thin sliver stays visible. */
	fillPercent: number;
	severity: Exclude<Severity, "normal">;
	/** Short enough to sit beside the turn-settings control. */
	label: string;
	/**
	 * For the meter's `accessibilityValue`, so a screen reader hears how full
	 * the window is rather than a bar it cannot see.
	 */
	accessibilityValue: { min: 0; max: 100; now: number; text: string };
};

/**
 * Shown beside the quota chip rather than yielding to it: the two run out on
 * different clocks, and near a limit is exactly when both are worth reading.
 */
export function contextMeterModel(usage: ConversationUsage | undefined): ContextMeterModel | null {
	const readout = contextReadout(usage);
	// `percent` is absent when the harness reports no context window, and there
	// is no budget to show without one.
	if (!readout || readout.percent === undefined || readout.fillPercent === undefined) return null;
	if (readout.severity === "normal") return null;

	return {
		percent: readout.percent,
		fillPercent: readout.fillPercent,
		severity: readout.severity,
		label: `${readout.percent}% context`,
		accessibilityValue: { min: 0, max: 100, now: readout.percent, text: `${readout.percent}% used` },
	};
}

export type QuotaChipModel = {
	severity: Exclude<Severity, "normal">;
	/** "76% quota · 23h": the reading and when it refills, short enough for a chip. */
	label: string;
	accessibilityLabel: string;
};

/**
 * The account's usage limit as a composer chip. Like the context meter it is
 * absent until it matters (75%), and turns red at 90%. It is the only quota
 * warning: a banner over the conversation repeated what the chip already said.
 */
export function quotaChipModel(limits: ConversationRateLimits | undefined): QuotaChipModel | null {
	const quota = quotaWarning(limits);
	if (!quota) return null;
	const reset = resetLabel(quota.resetsInSeconds);
	return {
		severity: quota.severity,
		label: reset ? `${quota.percent}% quota · ${reset}` : `${quota.percent}% quota`,
		accessibilityLabel: `${quota.label}, ${quota.percent}% used${reset ? `, resets in ${reset}` : ""}`,
	};
}
