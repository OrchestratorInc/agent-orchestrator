// How long a launch took, the mobile counterpart of the desktop's
// ao.launches.timed. Pure: runtime.ts reads React Native's startup marks and the
// frame times and hands them here.
//
// React Native's `performance.rnStartupTiming` gives the app's start, the JS
// runtime's start and the bundle's start in the same clock as
// `performance.now()`. The app start is recorded only when the host app logs
// it, which Expo's does not (checked on a Release build, RN 0.86); React Native
// then reports the runtime's start as `startTime` too. So `startTime` counts as
// the process start only when it is earlier than the runtime's start, and
// `clock_origin` says which origin a launch used.

import type { Surface } from "./surfaces";

export const LAUNCH_EVENT = "mobile.launches.timed";
/** A launch that has not shown live data by then is sent without it. */
export const LAUNCH_TIMEOUT_MS = 60_000;
// A delta outside this range is a clock mix-up, not a launch.
const MAX_DELTA_MS = 10 * 60_000;

export type LaunchKind = "first" | "cold" | "update";
export type LaunchOutcome = "usable" | "timeout" | "backgrounded";

export type StartupMarks = {
	startTime?: number | null;
	initializeRuntimeStart?: number | null;
	executeJavaScriptBundleEntryPointStart?: number | null;
};

/**
 * `first` for the first launch with this telemetry, `update` for the first on a
 * new app version or OTA update, `cold` otherwise. `everActive` is whether the
 * install sent a daily active before, so an install upgrading to the first
 * build with launch timing does not count as new.
 */
export function launchKind(previous: string | null, current: string, everActive: boolean): LaunchKind {
	if (previous === null) return everActive ? "update" : "first";
	return previous === current ? "cold" : "update";
}

export function launchProperties(input: {
	kind: LaunchKind;
	outcome: LaunchOutcome;
	marks: StartupMarks | null | undefined;
	visibleAt?: number;
	usableAt?: number;
	/** On screen when the launch became usable, timed out or went to the background. */
	landingSurface?: Surface | null;
}): Record<string, unknown> {
	const props: Record<string, unknown> = { launch_kind: input.kind, outcome: input.outcome };
	if (input.landingSurface) props.landing_surface = input.landingSurface;
	// Backgrounded before it was usable: the times measure the user, not the app.
	if (input.outcome === "backgrounded") return props;
	const runtimeStart = finite(input.marks?.initializeRuntimeStart);
	const reportedStart = finite(input.marks?.startTime);
	const processStart = reportedStart !== undefined && (runtimeStart === undefined || reportedStart < runtimeStart) ? reportedStart : undefined;
	const origin = processStart ?? runtimeStart;
	if (origin === undefined) return props;
	props.clock_origin = processStart !== undefined ? "process" : "runtime";
	const delta = (at: number | null | undefined) => {
		const value = finite(at);
		if (value === undefined) return undefined;
		const ms = Math.round(value - origin);
		return ms >= 0 && ms <= MAX_DELTA_MS ? ms : undefined;
	};
	const toJs = delta(input.marks?.executeJavaScriptBundleEntryPointStart);
	const toVisible = delta(input.visibleAt);
	const toUsable = input.outcome === "usable" ? delta(input.usableAt) : undefined;
	if (toJs !== undefined) props.to_js_start_ms = toJs;
	if (toVisible !== undefined) props.to_visible_ms = toVisible;
	if (toUsable !== undefined) props.to_usable_ms = toUsable;
	return props;
}

function finite(value: number | null | undefined): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}
