import { aoBridge } from "./bridge";
import type { LaunchSteps, LaunchTimingStart } from "../../shared/performance-telemetry";

/**
 * Launch timings for ao.launches.timed. Main hands each shell page its launch
 * start once (process creation for a cold launch, the window request for a warm
 * one), so a reload sends no timings.
 *
 * to_visible_ms ends at the first contentful paint. to_usable_ms ends at the
 * frame that shows the sidebar with live sessions, the same frame main's
 * "launch timing" log line ends at (the shell's reportSessionsShown). A cold
 * launch also carries main's steps on the way there, so a slow launch shows
 * whether the window, the daemon or the first workspace load took the time.
 */

const LAUNCH_VERSION_KEY = "ao.telemetry.launchVersion";
/** How long ao.launches.timed waits for a usable frame before it is sent without one. */
const USABLE_WAIT_MS = 60_000;

let resolveUsable: ((epochMs: number) => void) | undefined;
let usableAtMs: number | undefined;
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
	usableAtMs = performance.timeOrigin + performance.now();
	resolveUsable?.(usableAtMs);
}

function readLaunchKind(start: LaunchTimingStart, appVersion: string): "first" | "cold" | "warm" | "update" {
	let previous: string | null = null;
	try {
		previous = window.localStorage.getItem(LAUNCH_VERSION_KEY);
		window.localStorage.setItem(LAUNCH_VERSION_KEY, appVersion);
	} catch {
		// Without storage an update launch reads as cold.
	}
	if (start.kind === "cold" && start.firstRun) return "first";
	return start.kind === "cold" && previous !== null && previous !== appVersion ? "update" : start.kind;
}

/** Main's steps of a cold launch, as ao.launches.timed properties. */
async function readLaunchSteps(): Promise<Record<string, unknown>> {
	let steps: LaunchSteps | null = null;
	try {
		steps = (await aoBridge.telemetry.launchSteps?.()) ?? null;
	} catch {
		return {};
	}
	if (!steps) return {};
	return {
		to_app_ready_ms: steps.appReadyMs,
		to_window_shown_ms: steps.windowShownMs,
		to_server_spawn_ms: steps.serverSpawnMs,
		to_server_ready_ms: steps.serverReadyMs,
		server_reused: steps.serverReused,
	};
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

/**
 * The properties of ao.launches.timed. Resolves within USABLE_WAIT_MS. A window
 * closed first (a launch the daemon failed, then quit) hands the event to
 * sendOnClose inside its pagehide, since an event queued after that is lost
 * with the page, and the promise resolves undefined.
 */
export async function launchProperties(
	start: LaunchTimingStart,
	appVersion: string,
	sendOnClose?: (properties: Record<string, unknown>) => void,
): Promise<Record<string, unknown> | undefined> {
	const launchKind = readLaunchKind(start, appVersion);
	const properties = (usableAt: number | undefined, steps: Record<string, unknown> = {}): Record<string, unknown> => {
		if (hiddenDuringLaunch) return { launch_kind: launchKind };
		const visibleAt = firstContentfulPaintAt();
		return {
			launch_kind: launchKind,
			...(visibleAt === undefined ? {} : { to_visible_ms: visibleAt - start.startedAtMs }),
			...(usableAt === undefined ? {} : { to_usable_ms: usableAt - start.startedAtMs }),
			...steps,
		};
	};
	let timer = 0;
	let sentOnClose = false;
	let onPageHide = () => {};
	const usableAt = await Promise.race([
		usable,
		new Promise<undefined>((resolve) => {
			timer = window.setTimeout(() => resolve(undefined), USABLE_WAIT_MS);
			onPageHide = () => {
				if (sendOnClose) {
					sentOnClose = true;
					sendOnClose(properties(usableAtMs));
				}
				resolve(undefined);
			};
			window.addEventListener("pagehide", onPageHide, { once: true });
		}),
	]);
	window.clearTimeout(timer);
	// Main's steps are measured from process start, so only a cold launch has
	// them. The close handler stays on while they are read, so a window closed
	// meanwhile still reports the launch, once.
	const steps = !sentOnClose && start.kind === "cold" ? await readLaunchSteps() : {};
	window.removeEventListener("pagehide", onPageHide);
	if (sentOnClose) return undefined;
	return properties(usableAt, steps);
}
