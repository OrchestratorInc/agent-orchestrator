// What the app moving between the front and the background means for UI usage
// and performance. Pure: React Native's AppState values, the stall probe and
// the tracker are injected, so TelemetryManager stays a thin adapter and these
// rules are unit-tested.

import type { UiUsageTracker } from "./uiUsage";
import { noteBackgrounded } from "./uiUsageRegistry";

export type AppStateTelemetryDeps = {
	/** AppState.currentState when the app shell mounted. */
	initial: string | null | undefined;
	tracker: () => UiUsageTracker | null;
	/** Whether a machine is paired: only then is a return timed until fresh data. */
	paired: () => boolean;
	stalls: { start(): void; stop(): void };
	/** The app came to the front. */
	onActive: () => void;
	/** The app went to the background. */
	onBackground: () => void;
};

export type AppStateTelemetry = {
	change(state: string): void;
	dispose(): void;
};

export function createAppStateTelemetry(deps: AppStateTelemetryDeps): AppStateTelemetry {
	// In front unless it started in the background or `inactive`. Android can
	// report no state yet at launch (null or "unknown"), which is in front.
	let inFront = deps.initial !== "background" && deps.initial !== "inactive";
	// Set only when the app really went to the background. iOS also passes
	// through `inactive` for Control Center, Notification Center, the app
	// switcher, Face ID and system alerts: the app never left and polling kept
	// running, so coming back from those is not a return.
	let wasBackground = deps.initial === "background";

	// The stall probe runs only while the app is in front and telemetry is on.
	const syncStalls = () => {
		if (inFront && deps.tracker()?.isEnabled()) deps.stalls.start();
		else deps.stalls.stop();
	};
	syncStalls();
	// A launch in the background (a push woke the app): the tracker starts out
	// in front, and time before the first `active` is not time on screen.
	if (wasBackground) {
		noteBackgrounded();
		deps.tracker()?.setForeground(false);
	}
	const offEnabled = deps.tracker()?.onEnabledChange(syncStalls);

	return {
		change(state) {
			inFront = state === "active";
			if (state === "active") {
				deps.onActive();
				deps.tracker()?.setForeground(true);
				// Times how long a paired machine's data takes to be fresh again.
				if (wasBackground && deps.paired()) deps.tracker()?.resumed();
				wasBackground = false;
			}
			syncStalls();
			if (state === "background") {
				// Only a real background ends the stint, not `inactive`.
				wasBackground = true;
				noteBackgrounded();
				deps.tracker()?.setForeground(false);
				deps.onBackground();
			}
		},
		dispose() {
			deps.stalls.stop();
			offEnabled?.();
		},
	};
}
