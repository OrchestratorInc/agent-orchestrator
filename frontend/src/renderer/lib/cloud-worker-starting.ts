// A Cloud session's worker can be connected long before its workspace is
// ready: on a slow link the repository checkout takes minutes, and every
// request the control plane forwards to the worker meanwhile waits and ends in
// a gateway timeout. The control plane does not tell clients when the workspace
// is ready, so the worker's own answers are the signal: a "still starting"
// answer pauses every workspace read of the session (review, files, Chat
// models, prompt suggestions) for a backoff window, and any answer ends it.
//
// The wait is bounded. A WORKER_STARTING answer (the control plane saying so,
// with its Retry-After) is waited out for up to 30 minutes from the first one.
// A timeout (a bare 504, WORKER_TIMEOUT or TRANSPORT_TIMEOUT) can also be a
// ready worker failing, so only the first three in a row are read as starting.
// Past either limit the read fails as it did before, and the session's other
// reads are not held back.

import type { CloudCpClientEvent } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";

/** The control plane's answer for a worker that is connected but still preparing its workspace. */
export const WORKER_STARTING_CODE = "WORKER_STARTING";

/**
 * Answers in which the control plane says at once that the worker cannot
 * answer yet: WORKER_STARTING (with its Retry-After), and the worker not
 * connected (WORKER_UNAVAILABLE).
 */
const STARTING_CODES: ReadonlySet<string> = new Set([WORKER_STARTING_CODE, "WORKER_UNAVAILABLE"]);

/**
 * Answers in which the worker did not answer in time: from a control plane
 * that predates WORKER_STARTING this is how a starting worker looks, but it is
 * also how a ready worker that is failing looks.
 */
const TIMEOUT_CODES: ReadonlySet<string> = new Set(["WORKER_TIMEOUT", "TRANSPORT_TIMEOUT"]);

type StartingAnswer = "starting" | "timeout";

function startingAnswer(error: unknown): StartingAnswer | undefined {
	if (!(error instanceof CloudCpError)) return undefined;
	if (error.code !== undefined && STARTING_CODES.has(error.code)) return "starting";
	if ((error.code !== undefined && TIMEOUT_CODES.has(error.code)) || error.status === 504) return "timeout";
	return undefined;
}

/** Whether an answer can mean the worker is still starting (see startingAnswer). */
export function isCloudWorkerStarting(error: unknown): error is CloudCpError {
	return startingAnswer(error) !== undefined;
}

const FIRST_WAIT_MS = 5_000;
const MAX_WAIT_MS = 60_000;
const MIN_RETRY_AFTER_MS = 1_000;
const MAX_RETRY_AFTER_MS = 5 * 60_000;
/** How long a session's starting answers are waited out, from the first one in a row. */
export const MAX_STARTING_WAIT_MS = 30 * 60_000;
/** How many timeouts in a row are read as a starting worker before they fail as before. */
export const MAX_STARTING_TIMEOUTS = 3;
/** A starting answer this long after the previous one begins a new wait rather than continuing the old one. */
const STARTING_STREAK_GAP_MS = 5 * 60_000;

/**
 * How long to wait before asking a starting worker again, after `attempt`
 * (1-based) starting answers in a row: the control plane's Retry-After when it
 * sent one, else 5 s doubling to a minute.
 */
export function cloudWorkerStartingDelay(attempt: number, error?: unknown): number {
	const retryAfter = error instanceof CloudCpError ? error.retryAfterMs : undefined;
	if (retryAfter !== undefined && Number.isFinite(retryAfter)) return Math.min(Math.max(retryAfter, MIN_RETRY_AFTER_MS), MAX_RETRY_AFTER_MS);
	return Math.min(FIRST_WAIT_MS * 2 ** Math.max(0, attempt - 1), MAX_WAIT_MS);
}

type StartingEntry = {
	/** Starting answers in this wait. */
	attempts: number;
	/** Timeouts among them. */
	timeouts: number;
	/** When the wait's first starting answer came (undefined for a connect's first wait). */
	since?: number;
	/** When its latest starting answer came. */
	lastAt?: number;
	/** When the current backoff window ends (0 when there is none). */
	until: number;
	timer?: ReturnType<typeof setTimeout>;
};

const entries = new Map<string, StartingEntry>();
const listeners = new Set<() => void>();
/** Errors a read waits out (cloudWorkerRequest); any other error fails as before. */
const waitingErrors = new WeakSet<object>();

const keyOf = (orgId: string, sessionId: string) => `${orgId}/${sessionId}`;

function notify() {
	for (const listener of listeners) listener();
}

/**
 * Re-renders the readers when the session's window ends, so they ask again. A
 * timer can fire before Date.now() reaches the window's end (a busy event loop
 * schedules from a clock behind it), when readers would still see the wait and
 * nothing would wake them again: then it waits out the rest.
 */
function endWindowAfter(key: string, waitMs: number): ReturnType<typeof setTimeout> {
	return setTimeout(() => {
		const entry = entries.get(key);
		const left = entry ? entry.until - Date.now() : 0;
		if (entry && left > 0) {
			entry.timer = endWindowAfter(key, left);
			return;
		}
		notify();
	}, waitMs);
}

function setEntry(key: string, entry: Omit<StartingEntry, "until" | "timer">, waitMs: number, now: number) {
	const previous = entries.get(key);
	if (previous?.timer) clearTimeout(previous.timer);
	const timer = waitMs > 0 ? endWindowAfter(key, waitMs) : undefined;
	entries.set(key, { ...entry, until: waitMs > 0 ? now + waitMs : 0, timer });
	notify();
}

/**
 * Records a "still starting" answer from the session's worker. While the
 * session's budget lasts, its workspace reads wait for the backoff and this
 * returns true; past it nothing is held back and this returns false, so the
 * read that got the answer fails as before.
 */
export function noteCloudWorkerStarting(orgId: string, sessionId: string, error?: unknown, now = Date.now()): boolean {
	const answer = startingAnswer(error) ?? "starting";
	const key = keyOf(orgId, sessionId);
	const previous = entries.get(key);
	const streak = previous?.since !== undefined && previous.lastAt !== undefined && now - previous.lastAt <= STARTING_STREAK_GAP_MS
		? previous
		: undefined;
	const since = streak?.since ?? now;
	const attempts = (streak?.attempts ?? 0) + 1;
	const timeouts = (streak?.timeouts ?? 0) + (answer === "timeout" ? 1 : 0);
	const waits = now - since < MAX_STARTING_WAIT_MS && (answer === "starting" || timeouts <= MAX_STARTING_TIMEOUTS);
	setEntry(key, { attempts, timeouts, since, lastAt: now }, waits ? cloudWorkerStartingDelay(attempts, error) : 0, now);
	return waits;
}

/** The session's worker answered (or its workspace reported ready): its reads go ahead. */
export function noteCloudWorkerAnswered(orgId: string, sessionId: string): void {
	const key = keyOf(orgId, sessionId);
	const entry = entries.get(key);
	if (!entry) return;
	if (entry.timer) clearTimeout(entry.timer);
	entries.delete(key);
	notify();
}

/** Whether the session's workspace reads are waiting out a "still starting" answer. */
export function cloudWorkerStartingWait(orgId: string, sessionId: string): boolean {
	const entry = entries.get(keyOf(orgId, sessionId));
	return entry !== undefined && entry.until > Date.now();
}

/** How long until the session's current backoff window ends (0 when none). */
export function cloudWorkerStartingRemaining(orgId: string, sessionId: string): number {
	const entry = entries.get(keyOf(orgId, sessionId));
	return entry ? Math.max(0, entry.until - Date.now()) : 0;
}

export function subscribeCloudWorkerStarting(listener: () => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/**
 * Sends one workspace read to the session's worker, recording whether it
 * answered or is still starting. The error is rethrown either way; one the
 * read should wait out is marked for cloudWorkerStartingWaits.
 */
export async function cloudWorkerRequest<T>(orgId: string, sessionId: string, send: () => Promise<T>): Promise<T> {
	try {
		const answer = await send();
		noteCloudWorkerAnswered(orgId, sessionId);
		return answer;
	} catch (error) {
		// The mark follows this answer: an error seen again past the budget is no longer waited out.
		if (isCloudWorkerStarting(error) && noteCloudWorkerStarting(orgId, sessionId, error)) waitingErrors.add(error);
		else if (typeof error === "object" && error !== null) waitingErrors.delete(error);
		throw error;
	}
}

/**
 * Whether a read's error (from cloudWorkerRequest) is a starting answer the
 * read waits out and asks again after, rather than a failure to show.
 */
export function cloudWorkerStartingWaits(error: unknown): boolean {
	return typeof error === "object" && error !== null && waitingErrors.has(error);
}

/** How long a read waits before asking a starting worker again: the rest of the session's window, else the backoff. */
export function cloudWorkerStartingRetryDelay(orgId: string, sessionId: string, failureCount: number, error: unknown): number {
	return cloudWorkerStartingRemaining(orgId, sessionId) || cloudWorkerStartingDelay(failureCount + 1, error);
}

/**
 * A workspace read's retry options: a starting answer it waits out is asked
 * again when the session's window ends; any other error follows `retry`, with
 * the usual doubling delay.
 */
export function cloudWorkerReadRetry(orgId: string, sessionId: string, retry: (failureCount: number, error: Error) => boolean) {
	return {
		retry: (failureCount: number, error: Error) => cloudWorkerStartingWaits(error) || retry(failureCount, error),
		retryDelay: (failureCount: number, error: Error) => cloudWorkerStartingWaits(error)
			? cloudWorkerStartingRetryDelay(orgId, sessionId, failureCount, error)
			: Math.min(1_000 * 2 ** failureCount, 30_000),
	};
}

/** How old a worker connect may be and still mean a workspace is being prepared now. */
const FRESH_CONNECT_MS = 2 * 60_000;

/**
 * Events the worker sends only once its workspace is prepared: the project's
 * Setup (which runs in the prepared checkout), the agent's start, or a Chat
 * turn it took.
 */
const WORKSPACE_READY_EVENTS: ReadonlySet<string> = new Set(["workspace.setup", "workspace.script.step", "agent.ready", "chat.turn_started"]);

/**
 * Follows a session's startup in its event stream: a worker that has just
 * connected is preparing its workspace, so its reads wait one window before
 * the first is sent; a sign that the workspace is ready ends any wait.
 */
export function recordCloudWorkspaceStartupEvent(orgId: string, sessionId: string, event: CloudCpClientEvent, now = Date.now()): void {
	if (event.sessionId !== sessionId) return;
	if (WORKSPACE_READY_EVENTS.has(event.type)) {
		noteCloudWorkerAnswered(orgId, sessionId);
		return;
	}
	if (event.type !== "worker.connected" && event.type !== "worker.ready") return;
	const at = Date.parse(event.createdAt);
	if (!Number.isFinite(at) || now - at > FRESH_CONNECT_MS) return;
	// A connect starts a new worker's backoff, without stretching one already running.
	if (cloudWorkerStartingWait(orgId, sessionId)) return;
	setEntry(keyOf(orgId, sessionId), { attempts: 0, timeouts: 0 }, FIRST_WAIT_MS, now);
}

/** Forgets a session's backoff, as when its worker went away: the next worker starts afresh. */
export function forgetCloudWorkerStarting(orgId: string, sessionId: string): void {
	noteCloudWorkerAnswered(orgId, sessionId);
}

/** Test hook: clears every session's backoff. */
export function resetCloudWorkerStartingForTests(): void {
	for (const entry of entries.values()) if (entry.timer) clearTimeout(entry.timer);
	entries.clear();
	notify();
}
