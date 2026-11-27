import { EXIT_FEEDBACK_EVENTS, EXIT_FEEDBACK_TIMEOUT_MS, type ExitFeedbackVersion } from "../shared/exit-feedback";

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
	const refresh = (): Promise<void> => {
		refreshing ??= (async () => {
			try {
				const [installCreatedMs, markerExists, daemon] = await Promise.all([
					deps.readInstallCreatedMs().catch(() => undefined),
					deps.markerExists().catch(() => true),
					deps.readDaemon().catch(() => ({})),
				]);
				facts = { installCreatedMs, markerExists: markerExists || facts.markerExists, ...daemon };
				if (timer && exitFeedbackSettled(facts, deps.now(), deps.override)) {
					deps.clearInterval(timer);
					timer = null;
				}
			} finally {
				refreshing = null;
			}
		})();
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
		/** Marks a quit that must never be held (update restart, shutdown, forced quit). */
		bypass(): void {
			if (phase === "idle") phase = "done";
			else void finish();
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

/** Returns the event to send, or null when it is not a well-formed exit feedback event for this project. */
export function validateExitFeedbackEvent(input: unknown, projectKey: string): Record<string, unknown> | null {
	if (!input || typeof input !== "object" || Array.isArray(input)) return null;
	const event = input as Record<string, unknown>;
	if (typeof event.event !== "string" || !EVENT_NAMES.has(event.event)) return null;
	const properties = event.properties;
	if (!properties || typeof properties !== "object" || Array.isArray(properties)) return null;
	if ((properties as Record<string, unknown>).token !== projectKey) return null;
	let body: string;
	try {
		body = JSON.stringify(event);
	} catch {
		return null;
	}
	return body.length <= MAX_EVENT_BYTES ? event : null;
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
			await Promise.race([
				Promise.allSettled([...inFlight]),
				new Promise<void>((resolve) => {
					timeout = setTimeout(resolve, capMs);
				}),
			]);
			clearTimeout(timeout);
		},
	};
}
