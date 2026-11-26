// The running UI usage tracker, for the screens and helpers that report into it.
// No React Native, Expo or SDK import, so any component (and its tests) can call
// these without loading the telemetry runtime; runtime.ts registers the tracker.

import { OPENED_BY_MS, type ErrorKind, type ErrorPresentation, type OpenedBySource, type UiUsageTracker } from "./uiUsage";

let tracker: UiUsageTracker | null = null;
// A link can arrive before telemetry starts (expo-router reads the launch URL
// first); it is handed over when the tracker registers.
let early: { source: OpenedBySource; at: number; sessionId?: string } | null = null;

export function setUiUsageTracker(next: UiUsageTracker | null, now: () => number = Date.now): void {
	tracker = next;
	if (next && early) {
		// Still inside the tracker's window only if it arrived just now.
		if (now() - early.at <= OPENED_BY_MS) next.openedBy(early.source, early.sessionId);
		early = null;
	}
}

/** The tracker, or null before telemetry starts and in dev builds. */
export function uiUsage(): UiUsageTracker | null {
	return tracker;
}

/** A user-visible error, by how it was shown and a fixed kind. Never its text. */
export function reportErrorShown(presentation: ErrorPresentation, kind: ErrorKind): void {
	tracker?.errorShown(presentation, kind);
}

/** A push, a link or the launch's restore is about to open a session (`sessionId`, when known). */
export function reportOpenedBy(source: OpenedBySource, sessionId?: string, now: () => number = Date.now): void {
	if (tracker) tracker.openedBy(source, sessionId);
	else early = { source, at: now(), ...(sessionId ? { sessionId } : {}) };
}

/** The session id in a `/session/<id>` path or link, decoded; undefined for anything else. */
export function sessionIdInPath(path: string): string | undefined {
	const match = /^(?:[a-z][\w+.-]*:\/\/\/?|\/)session\/([^/?#]+)/i.exec(path);
	if (!match) return undefined;
	try {
		return decodeURIComponent(match[1]);
	} catch {
		return match[1];
	}
}
