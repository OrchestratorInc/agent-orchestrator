// A Cloud session's worker can be connected long before its workspace is
// ready: on a slow link the repository checkout takes minutes, and every
// request the control plane forwards to the worker meanwhile waits and ends in
// a gateway timeout. The control plane does not tell clients when the workspace
// is ready, so the worker's own answers are the signal: a "still starting"
// answer pauses every workspace read of the session (review, Chat models,
// prompt suggestions) for a backoff window, and any answer ends it.

import type { CloudCpClientEvent } from "./cloud-cp";
import { CloudCpError } from "./cloud-cp/errors";

/** The control plane's answer for a worker that is connected but still preparing its workspace. */
export const WORKER_STARTING_CODE = "WORKER_STARTING";

/**
 * Answers that mean the worker cannot answer yet, rather than that the request
 * failed: WORKER_STARTING (with its Retry-After), and from a control plane
 * that predates it, the worker not connected (WORKER_UNAVAILABLE) or not
 * answering in time (WORKER_TIMEOUT, TRANSPORT_TIMEOUT, a bare 504).
 */
const STARTING_CODES: ReadonlySet<string> = new Set([WORKER_STARTING_CODE, "WORKER_UNAVAILABLE", "WORKER_TIMEOUT", "TRANSPORT_TIMEOUT"]);

export function isCloudWorkerStarting(error: unknown): error is CloudCpError {
	return error instanceof CloudCpError && ((error.code !== undefined && STARTING_CODES.has(error.code)) || error.status === 504);
}

const FIRST_WAIT_MS = 5_000;
const MAX_WAIT_MS = 60_000;
const MIN_RETRY_AFTER_MS = 1_000;
const MAX_RETRY_AFTER_MS = 5 * 60_000;

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

type StartingEntry = { attempts: number; until: number; timer?: ReturnType<typeof setTimeout> };

const entries = new Map<string, StartingEntry>();
const listeners = new Set<() => void>();

const keyOf = (orgId: string, sessionId: string) => `${orgId}/${sessionId}`;

function notify() {
	for (const listener of listeners) listener();
}

function setWindow(key: string, attempts: number, waitMs: number) {
	const previous = entries.get(key);
	if (previous?.timer) clearTimeout(previous.timer);
	const until = Date.now() + waitMs;
	// The window's end re-renders the readers, which then ask again.
	const timer = setTimeout(notify, waitMs);
	entries.set(key, { attempts, until, timer });
	notify();
}

/** Records a "still starting" answer from the session's worker: its workspace reads wait for the backoff. */
export function noteCloudWorkerStarting(orgId: string, sessionId: string, error?: unknown): void {
	const key = keyOf(orgId, sessionId);
	const attempts = (entries.get(key)?.attempts ?? 0) + 1;
	setWindow(key, attempts, cloudWorkerStartingDelay(attempts, error));
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
 * answered or is still starting. The error is rethrown either way.
 */
export async function cloudWorkerRequest<T>(orgId: string, sessionId: string, send: () => Promise<T>): Promise<T> {
	try {
		const answer = await send();
		noteCloudWorkerAnswered(orgId, sessionId);
		return answer;
	} catch (error) {
		if (isCloudWorkerStarting(error)) noteCloudWorkerStarting(orgId, sessionId, error);
		throw error;
	}
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
	const key = keyOf(orgId, sessionId);
	// A connect starts a new worker's backoff, without stretching one already running.
	if (cloudWorkerStartingWait(orgId, sessionId)) return;
	setWindow(key, 0, FIRST_WAIT_MS);
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
