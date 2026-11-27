import { EXIT_FEEDBACK_EVENTS, EXIT_FEEDBACK_TIMEOUT_MS, sanitizeExitFeedbackProperties, type ExitFeedbackVersion } from "../shared/exit-feedback";
import { sanitizeSensitiveString } from "../shared/telemetry-text";

/** Spent the moment the dialog appears, whatever the outcome. Lives in the AO data dir. */
export const EXIT_FEEDBACK_MARKER = "exit_feedback_shown";

const DAY_MS = 24 * 60 * 60_000;
const MIN_OPEN_MS = 10_000;
/** The renderer must draw the dialog within this, or the quit goes ahead. */
export const EXIT_FEEDBACK_DRAW_TIMEOUT_MS = 3_000;
/** Bound on delivering the events once the dialog is done. */
export const EXIT_FEEDBACK_FLUSH_MS = 2_000;
/** A second quit gives the renderer this long to record second_close. */
export const EXIT_FEEDBACK_SECOND_CLOSE_MS = 1_000;
/** Main's own backstop behind the renderer's 15 s countdown. */
export const EXIT_FEEDBACK_BACKSTOP_MS = EXIT_FEEDBACK_TIMEOUT_MS + 2_000;
const MAX_EVENT_BYTES = 32 * 1024;

export type ExitFeedbackOverride = { force: boolean; version?: ExitFeedbackVersion };

/**
 * Testing aid, not a user setting. AO_EXIT_FEEDBACK_FORCE=1 shows the prompt on
 * the next quit regardless of the once-only marker, the first-day rule and the
 * 10 second rule; =a or =b also forces version A (no_project) or B
 * (project_no_work). Analytics, the kill switch, "no work started" and every
 * quit safety rule still apply.
 */
export function parseExitFeedbackOverride(raw: string | undefined): ExitFeedbackOverride {
	const value = raw?.trim().toLowerCase();
	if (value === "a" || value === "no_project") return { force: true, version: "no_project" };
	if (value === "b" || value === "project_no_work") return { force: true, version: "project_no_work" };
	if (value === "1" || value === "true" || value === "on") return { force: true };
	return { force: false };
}

/** Facts kept ready while the app runs, so a quit never waits on the daemon. */
export type ExitFeedbackFacts = {
	/** telemetry_install_id mtime. */
	installCreatedMs?: number;
	/** Worker and standalone sessions ever started; undefined while unknown. */
	workSessions?: number;
	projectCount?: number;
	markerExists: boolean;
};

/** Which version to show now, or null. Pure: every input is already in memory. */
export function decideExitFeedback(
	facts: ExitFeedbackFacts,
	context: { nowMs: number; launchedAtMs: number; analyticsOn: boolean; override: ExitFeedbackOverride },
): ExitFeedbackVersion | null {
	if (!context.analyticsOn) return null;
	// Unknown is not zero: if the daemon could not say, do not ask.
	if (facts.workSessions !== 0) return null;
	if (!context.override.force) {
		if (facts.markerExists) return null;
		const created = facts.installCreatedMs;
		if (created === undefined || created > context.nowMs || context.nowMs - created >= DAY_MS) return null;
		if (context.nowMs - context.launchedAtMs < MIN_OPEN_MS) return null;
	}
	if (context.override.version) return context.override.version;
	if (facts.projectCount === undefined) return null;
	return facts.projectCount > 0 ? "project_no_work" : "no_project";
}

/** Whether a refresh can never make this install eligible again. */
export function exitFeedbackSettled(facts: ExitFeedbackFacts, nowMs: number, override: ExitFeedbackOverride): boolean {
	if (facts.workSessions !== undefined && facts.workSessions > 0) return true;
	if (override.force) return false;
	if (facts.markerExists) return true;
	return facts.installCreatedMs !== undefined && nowMs - facts.installCreatedMs >= DAY_MS;
}

/**
 * Keeps ExitFeedbackFacts current: on start, on every poll, and when asked
 * (a session was just created). Polling stops once the install is settled.
 * ponytail: 10 s poll; a CLI spawn in the last 10 s before quit can still
 * prompt. A daemon change stream would close that gap.
 */
export function createExitFeedbackFacts(deps: {
	readInstallCreatedMs(): Promise<number | undefined>;
	markerExists(): Promise<boolean>;
	/** Both undefined while the daemon cannot be reached. */
	readDaemon(): Promise<{ workSessions?: number; projectCount?: number }>;
	now(): number;
	override: ExitFeedbackOverride;
	setInterval(fn: () => void, ms: number): { unref?(): void };
	clearInterval(handle: unknown): void;
	pollMs?: number;
}) {
	let facts: ExitFeedbackFacts = { markerExists: false };
	let timer: { unref?(): void } | null = null;
	let refreshing: Promise<void> | null = null;
	// Set when work may have started after the in-flight read began: that read's
	// count is stale, so it is not published and one more read follows.
	let stale = false;
	const read = async (): Promise<void> => {
		do {
			stale = false;
			const [installCreatedMs, markerExists, daemon] = await Promise.all([
				deps.readInstallCreatedMs().catch(() => undefined),
				deps.markerExists().catch(() => true),
				deps.readDaemon().catch(() => ({})),
			]);
			const next: ExitFeedbackFacts = { installCreatedMs, markerExists: markerExists || facts.markerExists, ...daemon };
			facts = stale ? { ...next, workSessions: undefined } : next;
		} while (stale);
		if (timer && exitFeedbackSettled(facts, deps.now(), deps.override)) {
			deps.clearInterval(timer);
			timer = null;
		}
	};
	/** workMayHaveStarted: a session was just created, so the cached count is no longer trusted. */
	const refresh = (workMayHaveStarted = false): Promise<void> => {
		if (workMayHaveStarted) {
			stale = true;
			facts = { ...facts, workSessions: undefined };
		}
		refreshing ??= read().finally(() => {
			refreshing = null;
		});
		return refreshing;
	};
	return {
		start(): void {
			if (timer) return;
			timer = deps.setInterval(() => void refresh(), deps.pollMs ?? 10_000);
			timer.unref?.();
			void refresh();
		},
		refresh,
		get: (): ExitFeedbackFacts => facts,
		/** The dialog appeared: this install is spent. */
		markShown(): void {
			facts = { ...facts, markerExists: true };
		},
		stop(): void {
			if (timer) deps.clearInterval(timer);
			timer = null;
		},
	};
}

type Timers = {
	setTimeout(fn: () => void, ms: number): unknown;
	clearTimeout(handle: unknown): void;
};

/**
 * Holds one user quit while the dialog is up. Every path ends in proceed():
 * the dialog finishing, a draw that never happens, a second quit, the 15 s
 * backstop, a renderer that goes away, or the system shutting down. Once a quit
 * has gone through, later quits always pass.
 */
export function createExitFeedbackGate(deps: Timers & {
	/** The version to show for this quit, or null. Must not block or throw. */
	decide(): ExitFeedbackVersion | null;
	/** Asks the shell to draw the dialog; false when it cannot be reached. */
	request(version: ExitFeedbackVersion): boolean;
	/** Writes the durable once-only marker. */
	markShown(): void;
	notifySecondClose(): void;
	/** Resolves once queued events are delivered or capMs has passed. */
	flush(capMs: number): Promise<void>;
	log?(message: string, error?: unknown): void;
}) {
	type Phase = "idle" | "pending" | "open" | "closing" | "done";
	let phase: Phase = "idle";
	let resume: (() => void) | null = null;
	let timer: unknown = null;
	let secondCloseTimer: unknown = null;
	let bypassed = false;

	const arm = (ms: number) => {
		if (timer !== null) deps.clearTimeout(timer);
		timer = deps.setTimeout(() => void finish(), ms);
	};
	const clear = () => {
		if (timer !== null) deps.clearTimeout(timer);
		if (secondCloseTimer !== null) deps.clearTimeout(secondCloseTimer);
		timer = secondCloseTimer = null;
	};
	const finish = async (): Promise<void> => {
		if (phase !== "pending" && phase !== "open") return;
		phase = "closing";
		clear();
		try {
			await deps.flush(EXIT_FEEDBACK_FLUSH_MS);
		} catch (error) {
			deps.log?.("exit feedback flush failed", error);
		}
		if (phase !== "closing") return; // bypassed during the flush; already resumed
		phase = "done";
		const next = resume;
		resume = null;
		try {
			next?.();
		} catch (error) {
			deps.log?.("exit feedback resume failed", error);
		}
	};

	return {
		/**
		 * Called by a user quit or close. True means the gate holds it and will
		 * call resume() later; false means let it through now.
		 */
		intercept(resumeQuit: () => void): boolean {
			if (phase === "done") return false;
			if (phase === "closing") return true;
			if (phase === "pending" || phase === "open") {
				// Quitting again: record it as a skip and close at once.
				resume = resumeQuit;
				if (secondCloseTimer === null) {
					try {
						deps.notifySecondClose();
					} catch (error) {
						deps.log?.("exit feedback second close failed", error);
					}
					secondCloseTimer = deps.setTimeout(() => void finish(), EXIT_FEEDBACK_SECOND_CLOSE_MS);
				}
				return true;
			}
			let version: ExitFeedbackVersion | null = null;
			try {
				version = deps.decide();
			} catch (error) {
				deps.log?.("exit feedback check failed", error);
			}
			// Whatever the answer, this quit has been seen: a quit re-entered by
			// later cleanup must never open the dialog mid-shutdown.
			if (!version) {
				phase = "done";
				return false;
			}
			let sent = false;
			try {
				sent = deps.request(version);
			} catch (error) {
				deps.log?.("exit feedback request failed", error);
			}
			if (!sent) {
				phase = "done";
				return false;
			}
			phase = "pending";
			resume = resumeQuit;
			arm(EXIT_FEEDBACK_DRAW_TIMEOUT_MS);
			return true;
		},
		/**
		 * The quit that went through did not end the app (hidden to the tray, or
		 * cancelled by the chat-draft confirm), so a later quit may ask again. Only
		 * from done: a live dialog is never dropped, and a bypassed quit stays bypassed.
		 */
		reset(): void {
			if (phase === "done" && !bypassed) phase = "idle";
		},
		/** Marks a quit that must never be held (update restart, shutdown, forced quit). */
		bypass(): void {
			bypassed = true;
			// No flush wait: the held quit resumes at once and later quits pass.
			const next = phase === "pending" || phase === "open" || phase === "closing" ? resume : null;
			clear();
			phase = "done";
			resume = null;
			try {
				next?.();
			} catch (error) {
				deps.log?.("exit feedback resume failed", error);
			}
		},
		rendererState(state: unknown): void {
			if (state === "shown" && phase === "pending") {
				phase = "open";
				try {
					deps.markShown();
				} catch (error) {
					deps.log?.("exit feedback marker write failed", error);
				}
				arm(EXIT_FEEDBACK_BACKSTOP_MS);
			} else if (state === "typing" && phase === "open") {
				// The user is writing: wait for an answer or a skip, however long.
				if (timer !== null) deps.clearTimeout(timer);
				timer = null;
			} else if (state === "done" || state === "declined") {
				void finish();
			}
		},
		/** The shell went away or the system is shutting down. */
		abort(): void {
			void finish();
		},
		/** Whether events from the renderer belong to a live dialog. */
		accepting(): boolean {
			return phase === "pending" || phase === "open" || phase === "closing";
		},
		phase: (): Phase => phase,
	};
}

const EVENT_NAMES = new Set<string>(EXIT_FEEDBACK_EVENTS);

// The context every desktop event carries (buildTelemetryContext, the
// registered super properties and withTelemetryContext). SDK fields start
// with "$" and are kept only as primitive values.
const CONTEXT_KEYS = new Set([
	"token", "distinct_id", "install_id", "app_version", "ao_version", "platform", "client", "surface",
	"release_channel", "version_channel", "build_mode", "telemetry_schema_version", "experiment_cohort",
	"arch", "install_source", "installed_at", "is_ci", "is_container", "days_since_install",
	"lifetime_sessions", "is_signed_in", "github_actor", "ao_cloud_user_id", "cloud_org_kind",
	"cloud_billing_mode", "cloud_org_id_hash",
]);
const MAX_CONTEXT_STRING = 256;

function contextValue(value: unknown): unknown {
	if (typeof value === "string") return sanitizeSensitiveString(value).slice(0, MAX_CONTEXT_STRING);
	if (typeof value === "boolean" || (typeof value === "number" && Number.isFinite(value))) return value;
	return undefined;
}

/**
 * Rebuilds a renderer event from an explicit allowlist, or returns null. Main
 * is the last check before PostHog, so it trusts nothing the renderer sent:
 * the feedback fields go through the same sanitizer as in the renderer, the
 * context keeps known keys only, and the timestamp must be ISO text.
 */
export function validateExitFeedbackEvent(input: unknown, projectKey: string): Record<string, unknown> | null {
	if (!input || typeof input !== "object" || Array.isArray(input)) return null;
	const event = input as Record<string, unknown>;
	if (typeof event.event !== "string" || !EVENT_NAMES.has(event.event)) return null;
	const raw = event.properties;
	if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
	const received = raw as Record<string, unknown>;
	if (received.token !== projectKey || typeof received.distinct_id !== "string") return null;
	const timestamp = event.timestamp;
	if (typeof timestamp !== "string" || Number.isNaN(Date.parse(timestamp)) || new Date(timestamp).toISOString() !== timestamp) return null;
	const feedback = sanitizeExitFeedbackProperties(event.event, received);
	if (!("version" in feedback)) return null;
	if (event.event === "ao.exit_feedback.answered" && !("answer" in feedback)) return null;
	const properties: Record<string, unknown> = {};
	for (const [key, value] of Object.entries(received)) {
		if (!CONTEXT_KEYS.has(key) && !key.startsWith("$")) continue;
		const safe = contextValue(value);
		if (safe !== undefined) properties[key] = safe;
	}
	Object.assign(properties, feedback);
	const out: Record<string, unknown> = { event: event.event, properties, timestamp };
	if (typeof event.uuid === "string" && /^[0-9a-f-]{1,64}$/i.test(event.uuid)) out.uuid = event.uuid;
	return JSON.stringify(out).length <= MAX_EVENT_BYTES ? out : null;
}

/**
 * Sends the renderer's finished exit feedback events from main, which outlives
 * the window. The SDK built each event (identity, context, sanitized text,
 * timestamp); main only posts it, so it survives the quit that follows.
 */
export function createExitFeedbackSender(deps: {
	host: string;
	projectKey: string;
	fetch: typeof fetch;
	log?(message: string, error?: unknown): void;
}) {
	const inFlight = new Set<Promise<void>>();
	return {
		send(input: unknown): boolean {
			const event = validateExitFeedbackEvent(input, deps.projectKey);
			if (!event || !deps.projectKey) return false;
			const url = `${deps.host.replace(/\/+$/, "")}/e/`;
			const request = deps
				.fetch(url, {
					method: "POST",
					headers: { "content-type": "application/json" },
					// A Date timestamp serializes to an ISO string here.
					body: JSON.stringify(event),
					signal: AbortSignal.timeout(EXIT_FEEDBACK_FLUSH_MS),
				})
				.then((response) => {
					if (!response.ok) deps.log?.(`exit feedback send failed: ${response.status}`);
				})
				.catch((error: unknown) => deps.log?.("exit feedback send failed", error));
			const tracked = request.finally(() => inFlight.delete(tracked));
			inFlight.add(tracked);
			return true;
		},
		async flush(capMs: number): Promise<void> {
			let timeout: ReturnType<typeof setTimeout> | undefined;
			// Re-checked until empty, so an event admitted during the flush is awaited too.
			const drained = (async () => {
				while (inFlight.size > 0) await Promise.allSettled([...inFlight]);
			})();
			await Promise.race([
				drained,
				new Promise<void>((resolve) => {
					timeout = setTimeout(resolve, capMs);
				}),
			]);
			clearTimeout(timeout);
		},
	};
}
