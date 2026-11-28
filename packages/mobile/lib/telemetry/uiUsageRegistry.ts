// The running UI usage tracker, for the screens and helpers that report into it.
// No React Native, Expo or SDK import, so any component (and its tests) can call
// these without loading the telemetry runtime; runtime.ts registers the tracker.

import { TRANSPORTS, type Transport } from "./uiPerformance";
import { OPENED_BY_MS, type ErrorKind, type ErrorPresentation, type OpenedBySource, type SessionTarget, type UiUsageTracker } from "./uiUsage";

let tracker: UiUsageTracker | null = null;
let offEnabled: (() => void) | undefined;
// Goes up each time the app goes to the background, and each time telemetry is
// switched off or on. A request in flight across a background measured the time
// away, or failed because the OS suspended it; one across a consent change
// belongs to a window that was discarded.
let epoch = 0;
// A link can arrive before telemetry starts (expo-router reads the launch URL
// first); it is handed over when the tracker registers.
let early: { source: OpenedBySource; at: number; target?: SessionTarget } | null = null;

export function setUiUsageTracker(next: UiUsageTracker | null, now: () => number = Date.now): void {
	offEnabled?.();
	tracker = next;
	// An opt-out or opt-in starts a fresh window: nothing in flight from before it counts.
	offEnabled = next?.onEnabledChange?.(() => {
		epoch += 1;
	});
	if (next && early) {
		// Still inside the tracker's window only if it arrived just now.
		if (now() - early.at <= OPENED_BY_MS) next.openedBy(early.source, early.target);
		early = null;
	}
}

/** The tracker, or null before telemetry starts and in dev builds. */
export function uiUsage(): UiUsageTracker | null {
	return tracker;
}

/** The app went to the background. */
export function noteBackgrounded(): void {
	epoch += 1;
}

/** Taken when a request starts; hand it back to reportRequestTimed. */
export function requestStartedIn(): number {
	return epoch;
}

/**
 * A daemon request's time to an answer (`null`: none came), over the connection
 * kind it used. Not counted when the app went to the background, or telemetry
 * was switched off or on, since `startedIn` (requestStartedIn when it began).
 */
export function reportRequestTimed(ms: number | null, transport: Transport, ok: boolean, startedIn: number): void {
	if (startedIn !== epoch) return;
	tracker?.requestTimed(ms, transport, ok);
}

/** The connection kind a machine's endpoint uses, as the performance summary names it. */
export function transportOf(kind: string | undefined): Transport {
	return kind && (TRANSPORTS as readonly string[]).includes(kind) ? kind as Transport : "unknown";
}

/** A user-visible error, by how it was shown and a fixed kind. Never its text. */
export function reportErrorShown(presentation: ErrorPresentation, kind: ErrorKind): void {
	tracker?.errorShown(presentation, kind);
}

/** A push, a link or the launch's restore is about to open a session (`target`, when known). */
export function reportOpenedBy(source: OpenedBySource, target?: SessionTarget, now: () => number = Date.now): void {
	if (tracker) tracker.openedBy(source, target);
	else early = { source, at: now(), ...(target ? { target } : {}) };
}

function decode(value: string): string {
	try {
		return decodeURIComponent(value);
	} catch {
		return value;
	}
}

/**
 * The session a `/session/<id>[?hostId=<host>]` path or link opens, decoded;
 * undefined for anything else. `hostId` is the same value the session route
 * keys its session by.
 */
export function sessionTargetInPath(path: string): SessionTarget | undefined {
	const match = /^(?:[a-z][\w+.-]*:\/\/\/?|\/)session\/([^/?#]+)(?:\?([^#]*))?/i.exec(path);
	if (!match) return undefined;
	const hostId = match[2] ? new URLSearchParams(match[2]).get("hostId") : null;
	return { sessionId: decode(match[1]), ...(hostId ? { hostId } : {}) };
}
