// Which parts of the app people look at and use, counted on the phone and sent
// as a few summaries, the mobile counterpart of the desktop's UI usage events
// (docs/telemetry.md, "UI usage"). Pure: the clock, the capture call and the
// storage write are injected, so every rule here is unit-tested. runtime.ts
// wires the real ones.
//
// Nothing here can carry content: a surface is a fixed id (surfaces.ts), a
// session-open source and an error kind are fixed ids, and everything else is
// a count or a duration.

import { isSessionSurface, SURFACES, type Surface } from "./surfaces";

export const SESSION_OPEN_SOURCES = [
	"board",
	"project",
	"prs",
	"spawn",
	"notification",
	"notifications_inbox",
	"deep_link",
	// The launch reopened the session the app was left on (RouteRestorer).
	"restore",
	// Opened from another session, such as a worker from its orchestrator.
	"session",
	"other",
] as const;
export type SessionOpenSource = (typeof SESSION_OPEN_SOURCES)[number];
export type OpenedBySource = "notification" | "deep_link" | "restore";

export const ERROR_PRESENTATIONS = ["toast", "screen", "sheet"] as const;
export type ErrorPresentation = (typeof ERROR_PRESENTATIONS)[number];

export const ERROR_KINDS = [
	"render_crash",
	"unreachable",
	"auth",
	"incompatible_host",
	"rate_limited",
	"server_error",
	"phone_offline",
	"cloud_unavailable",
	"action_failed",
] as const;
export type ErrorKind = (typeof ERROR_KINDS)[number];

/** A summary covers at least this much wall time; shorter windows carry on. */
export const UI_WINDOW_MS = 15 * 60_000;
/** A surface counts as viewed once it has been on screen this long. */
export const VIEW_DWELL_MS = 1_000;
/** The same error shown again within this long is counted but not sent again. */
export const ERROR_REPEAT_MS = 5 * 60_000;
/** A push, link or relaunch opens the session it names within this long. */
export const OPENED_BY_MS = 5_000;
export const UI_MEASUREMENT_SCHEMA_VERSION = 1;

export const UI_EVENTS = {
	viewed: "mobile.surfaces.viewed",
	summary: "mobile.ui.usage_summary",
	errorShown: "mobile.ui.error_shown",
} as const;
type UiEvent = (typeof UI_EVENTS)[keyof typeof UI_EVENTS];

const SOURCE_BY_SURFACE: Partial<Record<Surface, SessionOpenSource>> = {
	board: "board",
	projects: "project",
	project: "project",
	prs: "prs",
	compose: "spawn",
	spawn: "spawn",
	cloud_new_project: "spawn",
	notifications: "notifications_inbox",
};

export type UsageWindow = {
	startedAt: number;
	/** The last time the window was saved, which ends a window found at launch. */
	updatedAt: number;
	activeMs: number;
	foregrounds: number;
	surfaces: Partial<Record<Surface, { ms: number; opens: number }>>;
	sessionOpens: Partial<Record<SessionOpenSource, number>>;
	errors: number;
};

export type PersistedUiUsage = { window: UsageWindow; viewedDay: string; viewed: Surface[] };

export type UiUsageDeps = {
	now: () => number;
	/** Returns false when the event was not sent (opted out, kill switch, rate cap). */
	capture: (event: UiEvent, properties: Record<string, unknown>) => boolean;
	save: (state: PersistedUiUsage) => void;
	clear: () => void;
};

export type UiUsageTracker = {
	/** The surface of the route on screen, from expo-router's segments. */
	setRoute(surface: Surface): void;
	/** A route's screen naming which form it took (chat or terminal), or null on unmount. */
	setVariant(base: Surface, variant: Surface | null): void;
	setForeground(active: boolean): void;
	/**
	 * A session screen is showing `key` (its host and id, kept on the phone).
	 * Counted once per session, so a remount or a mode switch is not a new
	 * opening but going straight to another session is.
	 */
	sessionOpened(key: string): void;
	/**
	 * A push, a link or the launch's restore is about to open a session, named by
	 * `sessionId` when known: the source is then credited only to that session,
	 * so a push for the session already on screen cannot claim the next one.
	 */
	openedBy(source: OpenedBySource, sessionId?: string): void;
	errorShown(presentation: ErrorPresentation, kind: ErrorKind): void;
	/** Restores what an earlier run saved; sends it first if its window is over. */
	restore(saved: PersistedUiUsage | null): void;
	/**
	 * Off (opt-out) drops everything counted and the saved copy, and stops
	 * counting; on starts a fresh window, so nothing from before is sent.
	 */
	setEnabled(enabled: boolean): void;
	current(): Surface | null;
};

function utcDay(ms: number): string {
	return new Date(ms).toISOString().slice(0, 10);
}

function emptyWindow(now: number): UsageWindow {
	return { startedAt: now, updatedAt: now, activeMs: 0, foregrounds: 0, surfaces: {}, sessionOpens: {}, errors: 0 };
}

function hasActivity(window: UsageWindow): boolean {
	return window.activeMs > 0 || window.errors > 0 || Object.keys(window.surfaces).length > 0;
}

/** The summary's properties: counters, durations and the fixed ids above. Zeros are left out. */
export function summaryProperties(window: UsageWindow, endedAt: number, reason: "background" | "startup"): Record<string, unknown> {
	const props: Record<string, unknown> = {
		measurement_schema_version: UI_MEASUREMENT_SCHEMA_VERSION,
		window_seconds: Math.max(0, Math.round((endedAt - window.startedAt) / 1000)),
		active_seconds: Math.round(window.activeMs / 1000),
		flush_reason: reason,
	};
	if (window.foregrounds > 0) props.foreground_count = window.foregrounds;
	if (window.errors > 0) props.error_shown_count = window.errors;
	let opens = 0;
	for (const source of SESSION_OPEN_SOURCES) {
		const count = window.sessionOpens[source] ?? 0;
		if (count > 0) props[`session_open_from_${source}_count`] = count;
		opens += count;
	}
	if (opens > 0) props.session_open_count = opens;
	for (const surface of SURFACES) {
		const entry = window.surfaces[surface];
		if (!entry) continue;
		const seconds = Math.round(entry.ms / 1000);
		if (seconds > 0) props[`surface_${surface}_seconds`] = seconds;
		if (entry.opens > 0) props[`surface_${surface}_opens`] = entry.opens;
	}
	return props;
}

function mergeWindows(earlier: UsageWindow, later: UsageWindow): UsageWindow {
	const surfaces = { ...earlier.surfaces };
	for (const [surface, entry] of Object.entries(later.surfaces) as Array<[Surface, { ms: number; opens: number }]>) {
		const prev = surfaces[surface];
		surfaces[surface] = { ms: (prev?.ms ?? 0) + entry.ms, opens: (prev?.opens ?? 0) + entry.opens };
	}
	const sessionOpens = { ...earlier.sessionOpens };
	for (const [source, count] of Object.entries(later.sessionOpens) as Array<[SessionOpenSource, number]>) {
		sessionOpens[source] = (sessionOpens[source] ?? 0) + count;
	}
	return {
		startedAt: earlier.startedAt,
		updatedAt: later.updatedAt,
		activeMs: earlier.activeMs + later.activeMs,
		foregrounds: earlier.foregrounds + later.foregrounds,
		surfaces,
		sessionOpens,
		errors: earlier.errors + later.errors,
	};
}

// A saved window is only trusted in the shape this file writes.
function isWindow(value: unknown): value is UsageWindow {
	const w = value as UsageWindow | null;
	return !!w && typeof w === "object" && Number.isFinite(w.startedAt) && Number.isFinite(w.updatedAt)
		&& Number.isFinite(w.activeMs) && Number.isFinite(w.foregrounds) && Number.isFinite(w.errors)
		&& typeof w.surfaces === "object" && w.surfaces !== null && typeof w.sessionOpens === "object" && w.sessionOpens !== null;
}

export function createUiUsageTracker(deps: UiUsageDeps): UiUsageTracker {
	const start = deps.now();
	// The launch is the window's first time in the foreground.
	let window: UsageWindow = { ...emptyWindow(start), foregrounds: 1 };
	let viewedDay = utcDay(start);
	const viewed = new Set<Surface>();
	let route: Surface | null = null;
	let variant: { base: Surface; surface: Surface } | null = null;
	let shown: Surface | null = null;
	let shownAt = start;
	let foreground = true;
	let activeSince = start;
	let lastOutsideSession: Surface | null = null;
	// The session counted since the app last left the session screens.
	let countedSession: string | null = null;
	let pendingSource: { source: SessionOpenSource; at: number; sessionId?: string } | null = null;
	const lastErrorAt = new Map<string, number>();
	let lastSaveAt = start;
	let enabled = true;

	const save = (now: number) => {
		if (!enabled) return;
		// Bank the foreground time so far, so a window saved before the process
		// ends carries it with the surface times.
		if (foreground) {
			window.activeMs += Math.max(0, now - activeSince);
			activeSince = now;
		}
		lastSaveAt = now;
		window.updatedAt = now;
		deps.save({ window, viewedDay, viewed: [...viewed] });
	};

	const markViewed = (surface: Surface, now: number) => {
		if (!enabled) return;
		const day = utcDay(now);
		if (day !== viewedDay) {
			viewedDay = day;
			viewed.clear();
		}
		if (viewed.has(surface)) return;
		// Marked only once sent, so a capped or blocked event is tried again on
		// the next view rather than lost for the day.
		if (deps.capture(UI_EVENTS.viewed, { surface })) viewed.add(surface);
	};

	// Closes the current stint on screen at `now`.
	const account = (now: number) => {
		if (enabled && foreground && shown) {
			const ms = Math.max(0, now - shownAt);
			const entry = window.surfaces[shown] ?? { ms: 0, opens: 0 };
			entry.ms += ms;
			window.surfaces[shown] = entry;
			if (ms >= VIEW_DWELL_MS) markViewed(shown, now);
		}
		shownAt = now;
	};

	const show = (next: Surface | null) => {
		if (next === shown) return;
		const now = deps.now();
		account(now);
		const relabel = shown === "session" && next !== null && next !== "session" && isSessionSurface(next);
		if (relabel) {
			// The session route became the chat or terminal it shows: one opening, not two.
			const base = window.surfaces.session;
			if (base && base.opens > 0) base.opens -= 1;
		}
		shown = next;
		if (next && enabled) {
			const entry = window.surfaces[next] ?? { ms: 0, opens: 0 };
			entry.opens += 1;
			window.surfaces[next] = entry;
			if (!isSessionSurface(next)) {
				lastOutsideSession = next;
				countedSession = null;
			}
		}
		if (now - lastSaveAt >= 60_000) save(now);
	};

	const effective = (): Surface | null => {
		if (!route) return null;
		return variant && variant.base === route ? variant.surface : route;
	};

	const flush = (reason: "background" | "startup", now: number) => {
		if (!enabled) return;
		if (now - window.startedAt < UI_WINDOW_MS) return;
		if (hasActivity(window)) deps.capture(UI_EVENTS.summary, summaryProperties(window, now, reason));
		window = emptyWindow(now);
		if (foreground) activeSince = now;
		save(now);
	};

	return {
		setRoute(surface) {
			route = surface;
			show(effective());
		},
		setVariant(base, surface) {
			if (surface) variant = { base, surface };
			else if (variant?.base === base) variant = null;
			show(effective());
		},
		setForeground(active) {
			if (active === foreground) return;
			const now = deps.now();
			if (!active) {
				account(now);
				if (enabled) window.activeMs += Math.max(0, now - activeSince);
				foreground = false;
				save(now);
				flush("background", now);
				return;
			}
			foreground = true;
			activeSince = now;
			shownAt = now;
			if (!enabled) return;
			window.foregrounds += 1;
			if (shown) {
				const entry = window.surfaces[shown] ?? { ms: 0, opens: 0 };
				entry.opens += 1;
				window.surfaces[shown] = entry;
			}
		},
		sessionOpened(key) {
			if (!enabled || key === countedSession) return;
			const fromSession = countedSession !== null;
			countedSession = key;
			const now = deps.now();
			// Only what marked itself (a push, a link, the launch's restore) for this
			// session, or the screen it was opened from, names the source; nothing is
			// guessed. A mark for another session is left for that one.
			const marked = pendingSource && now - pendingSource.at <= OPENED_BY_MS
				&& (pendingSource.sessionId === undefined || pendingSource.sessionId === key.slice(key.lastIndexOf("|") + 1))
				? pendingSource : null;
			const source: SessionOpenSource = marked
				? marked.source
				: fromSession ? "session"
					: lastOutsideSession ? SOURCE_BY_SURFACE[lastOutsideSession] ?? "other" : "other";
			if (marked) pendingSource = null;
			window.sessionOpens[source] = (window.sessionOpens[source] ?? 0) + 1;
		},
		openedBy(source, sessionId) {
			pendingSource = { source, at: deps.now(), ...(sessionId ? { sessionId } : {}) };
		},
		errorShown(presentation, kind) {
			if (!enabled) return;
			const now = deps.now();
			window.errors += 1;
			const surface = shown ?? "other";
			const key = `${presentation}|${kind}|${surface}`;
			const last = lastErrorAt.get(key);
			if (last !== undefined && now - last < ERROR_REPEAT_MS) return;
			// Held back only once sent, as with screen views: a dropped send is retried.
			if (deps.capture(UI_EVENTS.errorShown, { presentation, error_kind: kind, surface })) lastErrorAt.set(key, now);
		},
		restore(saved) {
			if (!enabled || !saved || !isWindow(saved.window)) return;
			const now = deps.now();
			if (saved.viewedDay === viewedDay && Array.isArray(saved.viewed)) {
				for (const surface of saved.viewed) if ((SURFACES as readonly string[]).includes(surface)) viewed.add(surface);
			}
			if (now - saved.window.startedAt >= UI_WINDOW_MS) {
				// Its window ran out while the app was closed: send it as it was saved.
				if (hasActivity(saved.window)) deps.capture(UI_EVENTS.summary, summaryProperties(saved.window, saved.window.updatedAt, "startup"));
			} else {
				window = mergeWindows(saved.window, window);
			}
			save(now);
		},
		setEnabled(next) {
			if (next === enabled) return;
			const now = deps.now();
			enabled = next;
			window = emptyWindow(now);
			viewed.clear();
			lastErrorAt.clear();
			pendingSource = null;
			shownAt = now;
			activeSince = now;
			if (!next) deps.clear();
		},
		current: () => shown,
	};
}
