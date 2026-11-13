import { AppState } from "react-native";

// Development builds only, and only when Metro was started with
// EXPO_PUBLIC_AO_JANK=1: reports when the JavaScript thread is blocked, the
// thing that makes taps, navigation and live updates feel stuck even while
// native scrolling stays smooth. A timer that should fire every TICK_MS is
// checked for lateness; a late tick means JS was busy for that long.
//
// Logged to Metro (and logcat as ReactNativeJS) with the prefix [jank]. A
// development bundle is several times slower than a release one, so compare
// numbers with each other, not with what a release build would show.

const TICK_MS = 50;
const STALL_MS = 120;
const SUMMARY_MS = 10_000;

// On globalThis so a Fast Refresh, which re-runs this module, keeps one probe.
const STARTED = "__aoJsStallProbe";

export function startJsStallProbe(): void {
	const dev = typeof __DEV__ !== "undefined" && __DEV__ === true;
	const global = globalThis as Record<string, unknown>;
	if (!dev || process.env.EXPO_PUBLIC_AO_JANK !== "1" || global[STARTED]) return;
	global[STARTED] = true;
	let last = Date.now();
	// Timers stop in the background; coming back is not a stall.
	AppState.addEventListener("change", () => { last = Date.now(); });
	let windowStart = last;
	let blocked = 0;
	let worst = 0;
	let stalls = 0;
	setInterval(() => {
		const now = Date.now();
		const late = now - last - TICK_MS;
		last = now;
		if (late >= STALL_MS) {
			stalls++;
			blocked += late;
			worst = Math.max(worst, late);
			console.info(`[jank] JS blocked ${late} ms`);
		}
		if (now - windowStart >= SUMMARY_MS) {
			const span = now - windowStart;
			if (stalls > 0) console.info(`[jank] last ${Math.round(span / 1000)} s: ${stalls} stalls, ${blocked} ms blocked (${Math.round((blocked / span) * 100)}%), worst ${worst} ms`);
			windowStart = now;
			blocked = 0;
			worst = 0;
			stalls = 0;
		}
	}, TICK_MS);
}

