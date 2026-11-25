// The running UI usage tracker, for the screens and helpers that report into it.
// No React Native, Expo or SDK import, so any component (and its tests) can call
// these without loading the telemetry runtime; runtime.ts registers the tracker.

import type { ErrorKind, ErrorPresentation, UiUsageTracker } from "./uiUsage";

let tracker: UiUsageTracker | null = null;

export function setUiUsageTracker(next: UiUsageTracker | null): void {
	tracker = next;
}

/** The tracker, or null before telemetry starts and in dev builds. */
export function uiUsage(): UiUsageTracker | null {
	return tracker;
}

/** A user-visible error, by how it was shown and a fixed kind. Never its text. */
export function reportErrorShown(presentation: ErrorPresentation, kind: ErrorKind): void {
	tracker?.errorShown(presentation, kind);
}
