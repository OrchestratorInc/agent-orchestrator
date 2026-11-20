import { useLayoutEffect, useRef } from "react";
import { aoBridge } from "./bridge";
import { queryClient } from "./query-client";
import { captureRendererEvent, isDeniedEvent, withTelemetryContext } from "./telemetry";
import {
	LONG_FRAME_CAUSES,
	memoryBucket,
	openSessionsBucket,
	uptimeBucket,
} from "./performance-telemetry-properties";
import { sessionIsActive, type WorkspaceSummary } from "../types/workspace";

/**
 * ao.usage.ui_performance: one numeric summary per 45 to 60 minutes of use,
 * covering session switch speed, slow clicks and keys, frozen frames, chat
 * open time, and memory and CPU. Measurements count only while the window is
 * visible. See docs/telemetry.md, "UI responsiveness".
 *
 * The window schedule copies session-management-telemetry.ts. The slowest
 * interaction's surface is added once the shared UI surface list lands.
 */

const EVENT = "ao.usage.ui_performance";
const STORAGE_KEY = "ao.telemetry.performance.v1";
const CHECKPOINT_MS = 5 * 60_000;
const IDLE_MS = 5 * 60_000;
const FLUSH_MIN_MS = 45 * 60_000;
const FLUSH_JITTER_MS = 15 * 60_000;
/** Installs with experiment_cohort below this send the summary. */
const SAMPLED_COHORTS = 50;
/** An input this recent started the switch; otherwise the switch starts when it is routed. */
const INPUT_START_WINDOW_MS = 1_000;
/** A switch slower than this is not timed: the machine likely slept. */
const MAX_OPEN_MS = 30_000;
const SLOW_INTERACTION_MS = 200;
const VERY_SLOW_INTERACTION_MS = 500;
const LONGEST_KEPT = 10;
const SAMPLES_KEPT = 100;
const SWITCH_BUCKETS_MS = [150, 500, 1000] as const;

type Cause = (typeof LONG_FRAME_CAUSES)[number];
type SwitchBuckets = [number, number, number, number];

type Summary = {
	activeMs: number;
	switchCached: SwitchBuckets;
	switchUncached: SwitchBuckets;
	switchVisibleMs: number[];
	switchVisibleSeen: number;
	switchDropped: number;
	interactions: number;
	slowInteractions: number;
	verySlowInteractions: number;
	/** The longest interactions in the window; id is 0 for ones from an earlier page. */
	longest: Array<{ id: number; ms: number }>;
	longFrames: number;
	longFrameBlockingMs: number;
	causes: Partial<Record<Cause, number>>;
	chatOpenMs: number[];
	chatOpenSeen: number;
};

type PersistedState = {
	version: 1;
	windowStartedAt: number;
	windowFlushAt: number;
	summary: Summary;
};

export type ResourceReader = () => Promise<{ rendererPeakMb: number; appPeakMb: number; cpuAvgPct: number } | null>;

export type OpenTiming = {
	/** A session-to-session switch, as opposed to opening a session from another page. */
	isSwitch: boolean;
	/** The destination showed its content in the first frame: a chat in memory, or a terminal session. */
	cached: boolean;
	/** The destination is a chat that had to load its messages. */
	chatLoaded: boolean;
	visibleMs: number;
	usableMs: number;
};

const zeroSummary = (): Summary => ({
	activeMs: 0,
	switchCached: [0, 0, 0, 0],
	switchUncached: [0, 0, 0, 0],
	switchVisibleMs: [],
	switchVisibleSeen: 0,
	switchDropped: 0,
	interactions: 0,
	slowInteractions: 0,
	verySlowInteractions: 0,
	longest: [],
	longFrames: 0,
	longFrameBlockingMs: 0,
	causes: {},
	chatOpenMs: [],
	chatOpenSeen: 0,
});

function freshState(now: number): PersistedState {
	return { version: 1, windowStartedAt: now, windowFlushAt: now + FLUSH_MIN_MS + Math.random() * FLUSH_JITTER_MS, summary: zeroSummary() };
}

const isCount = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value) && value >= 0;
const isCountList = (value: unknown, max: number): value is number[] => Array.isArray(value) && value.length <= max && value.every(isCount);
const isBuckets = (value: unknown): value is SwitchBuckets => isCountList(value, 4) && value.length === 4;

function loadState(storage: Storage, now: number): PersistedState {
	try {
		const parsed = JSON.parse(storage.getItem(STORAGE_KEY) ?? "null") as Partial<PersistedState> | null;
		const summary = parsed?.summary as Partial<Summary> | undefined;
		if (
			parsed?.version === 1 && isCount(parsed.windowStartedAt) && isCount(parsed.windowFlushAt) && summary &&
			isBuckets(summary.switchCached) && isBuckets(summary.switchUncached) &&
			isCountList(summary.switchVisibleMs, SAMPLES_KEPT) && isCountList(summary.chatOpenMs, SAMPLES_KEPT) &&
			Array.isArray(summary.longest) && summary.longest.length <= LONGEST_KEPT &&
			summary.longest.every((entry) => isCount(entry?.ms)) &&
			typeof summary.causes === "object" && summary.causes !== null &&
			Object.entries(summary.causes).every(([cause, ms]) => (LONG_FRAME_CAUSES as readonly string[]).includes(cause) && isCount(ms)) &&
			(["activeMs", "switchVisibleSeen", "switchDropped", "interactions", "slowInteractions", "verySlowInteractions", "longFrames", "longFrameBlockingMs", "chatOpenSeen"] as const)
				.every((key) => isCount(summary[key]))
		) {
			// Interaction ids restart with each page, so kept ones cannot be matched again.
			const restored = summary as Summary;
			return { ...(parsed as PersistedState), summary: { ...restored, longest: restored.longest.map((entry) => ({ id: 0, ms: entry.ms })) } };
		}
	} catch {
		// Replace malformed local state.
	}
	return freshState(now);
}

function addSample(list: number[], seen: number, value: number): void {
	// Reservoir sampling keeps an even sample once the list is full.
	if (list.length < SAMPLES_KEPT) list.push(value);
	else {
		const index = Math.floor(Math.random() * seen);
		if (index < SAMPLES_KEPT) list[index] = value;
	}
}

/** Nearest-rank percentile, or undefined for no samples. */
export function percentile(values: readonly number[], fraction: number): number | undefined {
	if (values.length === 0) return undefined;
	const sorted = [...values].sort((a, b) => a - b);
	return sorted[Math.max(0, Math.ceil(fraction * sorted.length) - 1)];
}

function bucketIndex(ms: number): number {
	const index = SWITCH_BUCKETS_MS.findIndex((limit) => ms < limit);
	return index === -1 ? SWITCH_BUCKETS_MS.length : index;
}

const BUCKET_SUFFIXES = ["lt150", "lt500", "lt1000", "ge1000"] as const;

export class PerformanceAccumulator {
	private state: PersistedState;
	private activeSince: number | null = null;
	private lastActivityAt: number;
	/** Recent interactions on this page, by id, so each counts once as slow. */
	private recentInteractions = new Map<number, number>();

	constructor(
		private readonly storage: Storage,
		private readonly now: () => number = Date.now,
		private readonly capture: (event: string, properties: Record<string, unknown>) => Promise<unknown> = captureRendererEvent,
		private readonly readResources: ResourceReader = async () => null,
		private readonly countOpenSessions: () => number | undefined = () => undefined,
		private readonly uptimeHours: () => number = () => performance.now() / 3_600_000,
	) {
		const current = now();
		this.state = loadState(storage, current);
		this.lastActivityAt = current;
	}

	/** The window is visible and the user did something. */
	activity(): void {
		const now = this.now();
		// After an idle gap the earlier stretch ends at the idle limit, not at this input.
		if (this.activeSince !== null && now - this.lastActivityAt > IDLE_MS) this.checkpoint();
		this.lastActivityAt = now;
		if (this.activeSince === null) this.activeSince = now;
	}

	/** The window was hidden or lost focus. */
	pause(): void {
		this.checkpoint();
		this.activeSince = null;
	}

	checkpoint(): void {
		const now = this.now();
		if (this.activeSince !== null) {
			const end = Math.min(now, this.lastActivityAt + IDLE_MS);
			this.state.summary.activeMs += Math.max(0, end - this.activeSince);
			this.activeSince = now - this.lastActivityAt <= IDLE_MS ? now : null;
		}
		this.persist();
	}

	addInteractionCount(count: number): void {
		if (count > 0) this.state.summary.interactions += count;
	}

	recordInteraction(id: number, ms: number): void {
		const summary = this.state.summary;
		const prior = this.recentInteractions.get(id) ?? 0;
		if (ms <= prior) return;
		this.recentInteractions.set(id, ms);
		if (this.recentInteractions.size > 50) this.recentInteractions.delete(this.recentInteractions.keys().next().value!);
		if (prior <= SLOW_INTERACTION_MS && ms > SLOW_INTERACTION_MS) summary.slowInteractions += 1;
		if (prior <= VERY_SLOW_INTERACTION_MS && ms > VERY_SLOW_INTERACTION_MS) summary.verySlowInteractions += 1;
		const existing = summary.longest.find((entry) => entry.id === id && id > 0);
		if (existing) {
			existing.ms = ms;
		} else if (summary.longest.length < LONGEST_KEPT) {
			summary.longest.push({ id, ms });
		} else {
			const shortest = summary.longest.reduce((min, entry) => (entry.ms < min.ms ? entry : min));
			if (ms <= shortest.ms) return;
			Object.assign(shortest, { id, ms });
		}
	}

	recordLongFrame(blockingMs: number, scripts: ReadonlyArray<{ invokerType?: string; duration: number }>): void {
		const summary = this.state.summary;
		summary.longFrames += 1;
		summary.longFrameBlockingMs += Math.max(0, blockingMs);
		if (scripts.length === 0) {
			summary.causes.other = (summary.causes.other ?? 0) + Math.max(0, blockingMs);
			return;
		}
		for (const script of scripts) {
			const cause: Cause = (LONG_FRAME_CAUSES as readonly string[]).includes(script.invokerType ?? "") ? (script.invokerType as Cause) : "other";
			summary.causes[cause] = (summary.causes[cause] ?? 0) + Math.max(0, script.duration);
		}
	}

	recordOpen(timing: OpenTiming): void {
		const summary = this.state.summary;
		if (timing.usableMs < 0 || timing.usableMs > MAX_OPEN_MS) {
			if (timing.isSwitch) summary.switchDropped += 1;
			return;
		}
		if (timing.isSwitch) {
			(timing.cached ? summary.switchCached : summary.switchUncached)[bucketIndex(timing.usableMs)] += 1;
			summary.switchVisibleSeen += 1;
			addSample(summary.switchVisibleMs, summary.switchVisibleSeen, Math.max(0, timing.visibleMs));
		}
		if (timing.chatLoaded) {
			summary.chatOpenSeen += 1;
			addSample(summary.chatOpenMs, summary.chatOpenSeen, timing.usableMs);
		}
	}

	recordDroppedOpen(isSwitch: boolean): void {
		if (isSwitch) this.state.summary.switchDropped += 1;
	}

	isFlushOverdue(): boolean {
		return this.now() >= this.state.windowFlushAt;
	}

	flushDelayMs(): number {
		return Math.max(0, this.state.windowFlushAt - this.now());
	}

	async flush(reason: "interval" | "startup" = "interval"): Promise<void> {
		this.checkpoint();
		const now = this.now();
		const summary = this.state.summary;
		const windowStartedAt = this.state.windowStartedAt;
		// The next window starts now, so work recorded while this one sends lands there.
		this.state = freshState(now);
		this.recentInteractions.clear();
		this.persist();
		if (summary.activeMs < 1_000 && summary.interactions === 0) return;
		const longest = [...summary.longest].sort((a, b) => b.ms - a.ms);
		// The web-vitals INP method: skip one of the longest per 50 interactions.
		const p98 = longest.length ? longest[Math.min(longest.length - 1, Math.floor(summary.interactions / 50))]!.ms : 0;
		const topCause = (Object.entries(summary.causes) as Array<[Cause, number]>)
			.filter(([, ms]) => ms > 0)
			.sort((a, b) => b[1] - a[1])[0]?.[0];
		const resources = await this.readResources().catch(() => null);
		const openSessions = this.countOpenSessions();
		const properties: Record<string, unknown> = {
			measurement_schema_version: 1,
			flush_reason: reason,
			window_seconds: Math.round((now - windowStartedAt) / 1000),
			active_seconds: Math.round(summary.activeMs / 1000),
			switch_visible_p75_ms: percentile(summary.switchVisibleMs, 0.75),
			switch_dropped_count: summary.switchDropped,
			interaction_count: summary.interactions,
			slow_interaction_count: summary.slowInteractions,
			very_slow_interaction_count: summary.verySlowInteractions,
			interaction_p98_ms: Math.round(p98),
			long_frame_count: summary.longFrames,
			long_frame_blocking_ms: Math.round(summary.longFrameBlockingMs),
			long_frame_top_cause: topCause,
			chat_open_count: summary.chatOpenSeen,
			chat_open_p75_ms: percentile(summary.chatOpenMs, 0.75),
			open_sessions_bucket: openSessions === undefined ? undefined : openSessionsBucket(openSessions),
			uptime_hours_bucket: uptimeBucket(this.uptimeHours()),
			...(resources
				? {
					renderer_mb_bucket: resources.rendererPeakMb > 0 ? memoryBucket(resources.rendererPeakMb) : undefined,
					app_mb_bucket: memoryBucket(resources.appPeakMb),
					app_cpu_avg_pct: resources.cpuAvgPct,
				}
				: {}),
		};
		BUCKET_SUFFIXES.forEach((suffix, index) => {
			properties[`switch_cached_${suffix}_count`] = summary.switchCached[index];
			properties[`switch_uncached_${suffix}_count`] = summary.switchUncached[index];
		});
		await this.capture(EVENT, properties);
	}

	private persist(): void {
		try {
			this.storage.setItem(STORAGE_KEY, JSON.stringify(this.state));
		} catch {
			// Telemetry must never affect the product.
		}
	}
}

// --- Runtime: observers, input tracking and session open timing ---

type EventTimingEntry = PerformanceEntry & { interactionId?: number };
type LongFrameEntry = PerformanceEntry & { blockingDuration?: number; scripts?: Array<{ invokerType?: string; duration: number }> };

type PendingOpen = {
	sessionId: string;
	isSwitch: boolean;
	startedAt: number;
	visibleAt?: number;
	cached?: boolean;
	chatLoaded?: boolean;
	timer: number;
};

let runtime: PerformanceAccumulator | null = null;
let lastInputAt = Number.NEGATIVE_INFINITY;
let pendingOpen: PendingOpen | null = null;
/** Mounted local chat surfaces by session id: true once its messages rendered. */
const chatTranscripts = new Map<string, boolean>();

function countOpenSessions(): number | undefined {
	const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(["workspaces"]);
	return workspaces?.reduce((total, workspace) => total + workspace.sessions.filter(sessionIsActive).length, 0);
}

function dropPendingOpen(): void {
	const open = pendingOpen;
	if (!open) return;
	window.clearTimeout(open.timer);
	pendingOpen = null;
	runtime?.recordDroppedOpen(open.isSwitch);
}

function finishOpen(open: PendingOpen, usableAt: number): void {
	if (pendingOpen !== open) return;
	window.clearTimeout(open.timer);
	pendingOpen = null;
	runtime?.recordOpen({
		isSwitch: open.isSwitch,
		cached: open.cached ?? true,
		chatLoaded: open.chatLoaded ?? false,
		visibleMs: (open.visibleAt ?? usableAt) - open.startedAt,
		usableMs: usableAt - open.startedAt,
	});
}

function beginSessionOpen(sessionId: string, shownSessionId: string | undefined): void {
	if (!runtime) return;
	if (pendingOpen) {
		// A newer switch replaced this one before it finished; it is not a measurement.
		window.clearTimeout(pendingOpen.timer);
		pendingOpen = null;
	}
	if (document.visibilityState !== "visible") return;
	const now = performance.now();
	const isSwitch = shownSessionId !== undefined && shownSessionId !== sessionId;
	const fromInput = now - lastInputAt <= INPUT_START_WINDOW_MS;
	// An open nobody asked for (the launch restoring the last session, a new
	// session opening itself) is not timed; a switch from a menu shortcut is.
	if (!isSwitch && !fromInput) return;
	const open: PendingOpen = {
		sessionId,
		isSwitch,
		// Start at the click or key that asked for it, so time spent waiting
		// behind other work counts. A menu shortcut has no DOM input.
		startedAt: fromInput ? lastInputAt : now,
		timer: 0,
	};
	open.timer = window.setTimeout(() => {
		if (pendingOpen === open) dropPendingOpen();
	}, MAX_OPEN_MS);
	pendingOpen = open;
}

function commitSessionOpen(sessionId: string): void {
	const open = pendingOpen;
	if (!open || open.sessionId !== sessionId) return;
	// Chat surfaces commit with the session view, before this host's layout
	// effect, so whether the destination's messages are in already is known.
	const transcript = chatTranscripts.get(sessionId);
	open.cached = transcript !== false;
	open.chatLoaded = transcript === false;
	requestAnimationFrame(() => {
		if (pendingOpen !== open) return;
		open.visibleAt = performance.now();
		if (!open.chatLoaded) finishOpen(open, open.visibleAt);
	});
}

function transcriptShown(sessionId: string): void {
	const open = pendingOpen;
	if (!open || open.sessionId !== sessionId || !open.chatLoaded) return;
	requestAnimationFrame(() => finishOpen(open, performance.now()));
}

/**
 * Times opening the session the shell shows. Called by LocalSessionHost with
 * the routed session and the one on screen (they differ while a switch holds).
 */
export function useSessionOpenTiming(liveSessionId: string | undefined, shownSessionId: string | undefined): void {
	const previousLive = useRef<string | undefined>(undefined);
	const previousShown = useRef<string | undefined>(undefined);
	useLayoutEffect(() => {
		if (liveSessionId && liveSessionId !== previousLive.current) beginSessionOpen(liveSessionId, previousShown.current);
		previousLive.current = liveSessionId;
	}, [liveSessionId]);
	useLayoutEffect(() => {
		if (shownSessionId && shownSessionId !== previousShown.current) commitSessionOpen(shownSessionId);
		previousShown.current = shownSessionId;
	}, [shownSessionId]);
}

/** Called by a local chat surface with whether its messages have rendered. */
export function useTranscriptTiming(sessionId: string, shown: boolean, hostId: string | undefined): void {
	useLayoutEffect(() => {
		if (hostId) return;
		chatTranscripts.set(sessionId, shown);
		if (shown) transcriptShown(sessionId);
		return () => {
			chatTranscripts.delete(sessionId);
		};
	}, [hostId, sessionId, shown]);
}

function observe(type: string, callback: (entries: PerformanceEntry[]) => void, options: Record<string, unknown>): PerformanceObserver | null {
	if (typeof PerformanceObserver === "undefined" || !PerformanceObserver.supportedEntryTypes?.includes(type)) return null;
	try {
		const observer = new PerformanceObserver((list) => callback(list.getEntries()));
		observer.observe({ type, ...options } as PerformanceObserverInit);
		return observer;
	} catch {
		return null;
	}
}

function interactionCount(): number {
	const count = (performance as Performance & { interactionCount?: number }).interactionCount;
	return typeof count === "number" ? count : 0;
}

/** Drops the stored window, so nothing counted before an opt-out is sent after an opt-in. */
export function discardPerformanceWindow(storage: Storage): void {
	try {
		storage.removeItem(STORAGE_KEY);
	} catch {
		// Telemetry must never affect the product.
	}
}

let stopRuntime: (() => void) | null = null;
let started = false;
/** Set once the summary has stopped, so a restart does not replay earlier frames. */
let restarted = false;

/** Starts the summary for this window once telemetry is enabled. */
export function startPerformanceTelemetry(): void {
	started = true;
	startRuntime();
}

/**
 * Applies the analytics opt-out at launch and whenever it changes. An opt-out
 * stops the summary and discards what it counted, as the updater counts are,
 * so nothing from before it is sent after a later opt-in. An opt-in starts a
 * fresh window.
 */
export function applyPerformanceOptOut(optedOut: boolean): void {
	if (optedOut) {
		stopRuntime?.();
		discardPerformanceWindow(window.localStorage);
		return;
	}
	if (!started || runtime) return;
	// Memory and CPU main kept during the opt-out start over too.
	void aoBridge.telemetry.takeResourceSummary?.().catch(() => null);
	startRuntime();
}

function startRuntime(): void {
	if (runtime || isDeniedEvent(EVENT)) return;
	const cohort = withTelemetryContext({}).experiment_cohort;
	if (typeof cohort !== "number" || cohort >= SAMPLED_COHORTS) return;
	const accumulator = new PerformanceAccumulator(
		window.localStorage,
		Date.now,
		captureRendererEvent,
		() => aoBridge.telemetry.takeResourceSummary?.() ?? Promise.resolve(null),
		countOpenSessions,
	);
	runtime = accumulator;
	const visible = () => document.visibilityState === "visible";
	let countedInteractions = interactionCount();
	// A hidden window takes no input, so the count since the last sync is
	// all visible use, even when the sync runs as the window hides.
	const syncInteractionCount = () => {
		const count = interactionCount();
		accumulator.addInteractionCount(count - countedInteractions);
		countedInteractions = count;
	};

	const onInput = (event: Event) => {
		lastInputAt = event.timeStamp;
		if (visible()) accumulator.activity();
	};
	const onVisibility = () => {
		syncInteractionCount();
		if (visible()) {
			accumulator.activity();
			return;
		}
		accumulator.pause();
		dropPendingOpen();
	};
	const onPageHide = () => {
		syncInteractionCount();
		accumulator.pause();
	};
	document.addEventListener("pointerdown", onInput, { capture: true, passive: true });
	document.addEventListener("click", onInput, { capture: true, passive: true });
	document.addEventListener("keydown", onInput, { capture: true, passive: true });
	document.addEventListener("visibilitychange", onVisibility);
	window.addEventListener("pagehide", onPageHide);
	if (visible()) accumulator.activity();

	const eventObserver = observe("event", (entries) => {
		if (!visible()) return;
		for (const entry of entries as EventTimingEntry[]) {
			if (entry.interactionId) accumulator.recordInteraction(entry.interactionId, entry.duration);
		}
	}, { durationThreshold: 40 });
	// A restart after an opt-out skips the buffered frames: ones from before or
	// during the opt-out belong to no window.
	const frameObserver = observe("long-animation-frame", (entries) => {
		if (!visible()) return;
		for (const entry of entries as LongFrameEntry[]) accumulator.recordLongFrame(entry.blockingDuration ?? 0, entry.scripts ?? []);
	}, { buffered: !restarted });

	const checkpoint = window.setInterval(() => {
		syncInteractionCount();
		accumulator.checkpoint();
	}, CHECKPOINT_MS);
	let flushTimer = 0;
	const flush = (reason: "interval" | "startup") => {
		syncInteractionCount();
		void accumulator.flush(reason).catch(() => undefined).finally(scheduleFlush);
	};
	const scheduleFlush = () => {
		if (runtime !== accumulator) return;
		flushTimer = window.setTimeout(() => flush("interval"), accumulator.flushDelayMs());
	};
	if (accumulator.isFlushOverdue()) flush("startup");
	else scheduleFlush();

	stopRuntime = () => {
		stopRuntime = null;
		restarted = true;
		window.clearInterval(checkpoint);
		window.clearTimeout(flushTimer);
		eventObserver?.disconnect();
		frameObserver?.disconnect();
		document.removeEventListener("pointerdown", onInput, { capture: true });
		document.removeEventListener("click", onInput, { capture: true });
		document.removeEventListener("keydown", onInput, { capture: true });
		document.removeEventListener("visibilitychange", onVisibility);
		window.removeEventListener("pagehide", onPageHide);
		accumulator.pause();
		if (pendingOpen) window.clearTimeout(pendingOpen.timer);
		pendingOpen = null;
		runtime = null;
	};
}
