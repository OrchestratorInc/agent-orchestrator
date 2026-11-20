import { aoBridge } from "./bridge";
import type { LaunchTimingStart } from "../../shared/performance-telemetry";

/**
 * Launch timings for ao.launches.timed. Main hands each shell page its launch
 * start once (process creation for a cold launch, the window request for a warm
 * one), so a reload sends no timings.
 *
 * to_visible_ms ends at the first contentful paint. to_usable_ms ends at the
 * frame that shows the sidebar with live sessions, the same frame main's
 * "launch timing" log line ends at (the shell's reportSessionsShown).
 */

const LAUNCH_VERSION_KEY = "ao.telemetry.launchVersion";
/** How long ao.launches.timed waits for a usable frame before it is sent without one. */
const USABLE_WAIT_MS = 60_000;

let resolveUsable: ((epochMs: number) => void) | undefined;
const usable = new Promise<number>((resolve) => {
	resolveUsable = resolve;
});
// A launch the user hid partway (minimized the window or switched away)
// measures the wait for the window, not the app, so it reports no timings.
// Main creates the window hidden and shows it once this page has loaded, so a
// page that starts hidden is every launch and does not count; only becoming
// hidden later does.
let hiddenDuringLaunch = false;
let usableMarked = false;
if (typeof document !== "undefined") {
	const onVisibility = () => {
		if (usableMarked) return;
		if (document.visibilityState === "hidden") hiddenDuringLaunch = true;
	};
	document.addEventListener("visibilitychange", onVisibility);
}

/** Called on the frame that first shows the workspace with live sessions. */
export function markLaunchUsable(): void {
	if (usableMarked) return;
	usableMarked = true;
	resolveUsable?.(performance.timeOrigin + performance.now());
}

function readLaunchKind(kind: "cold" | "warm", appVersion: string): "cold" | "warm" | "update" {
	let previous: string | null = null;
	try {
		previous = window.localStorage.getItem(LAUNCH_VERSION_KEY);
		window.localStorage.setItem(LAUNCH_VERSION_KEY, appVersion);
	} catch {
		// Without storage an update launch reads as cold.
	}
	return kind === "cold" && previous !== null && previous !== appVersion ? "update" : kind;
}

function firstContentfulPaintAt(): number | undefined {
	const entry = performance.getEntriesByName?.("first-contentful-paint")?.[0];
	return entry ? performance.timeOrigin + entry.startTime : undefined;
}

/** This shell page's launch start, once; null for a reload or without the desktop bridge. */
export async function takeLaunchStart(): Promise<LaunchTimingStart | null> {
	try {
		return (await aoBridge.telemetry.takeLaunchStart?.()) ?? null;
	} catch {
		return null;
	}
}

/** The properties of ao.launches.timed. Resolves within USABLE_WAIT_MS. */
export async function launchProperties(start: LaunchTimingStart, appVersion: string): Promise<Record<string, unknown>> {
	const launchKind = readLaunchKind(start.kind, appVersion);
	let timer = 0;
	let onPageHide = () => {};
	// Closing the window first (a launch the daemon failed, then quit) still
	// sends the event, without the usable time.
	const usableAt = await Promise.race([
		usable,
		new Promise<undefined>((resolve) => {
			timer = window.setTimeout(() => resolve(undefined), USABLE_WAIT_MS);
			onPageHide = () => resolve(undefined);
			window.addEventListener("pagehide", onPageHide, { once: true });
		}),
	]);
	window.clearTimeout(timer);
	window.removeEventListener("pagehide", onPageHide);
	if (hiddenDuringLaunch) return { launch_kind: launchKind };
	const visibleAt = firstContentfulPaintAt();
	return {
		launch_kind: launchKind,
		...(visibleAt === undefined ? {} : { to_visible_ms: visibleAt - start.startedAtMs }),
		...(usableAt === undefined ? {} : { to_usable_ms: usableAt - start.startedAtMs }),
	};
}
