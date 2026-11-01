import { LOCAL_HOST } from "./hosts";

// Serialize startup and disposal across panel instances. In particular, a late
// response must be disposed before another panel can recover the same login.
const tasks = new Map<string, Promise<void>>();
// Disposals that did not reach the daemon, for example while a remote host is
// disconnected. The native login may still be running there, so its harness
// stays pending (no update or new login) until a retry succeeds.
const unsettled = new Map<string, Array<() => Promise<unknown>>>();
const retries = new Map<string, ReturnType<typeof setTimeout>>();
const listeners = new Set<() => void>();
let snapshot: ReadonlySet<string> = new Set();

const RETRY_FIRST_MS = 1_000;
const RETRY_MAX_MS = 15_000;

export const agentLoginKey = (agentId: string, hostId?: string) => JSON.stringify([hostId || LOCAL_HOST, agentId]);
export const pendingAgentLogins = () => snapshot;
export const isAgentLoginPending = (agentId: string, hostId?: string) => {
	const key = agentLoginKey(agentId, hostId);
	return tasks.has(key) || unsettled.has(key);
};
export function subscribeAgentLogins(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

function publish() {
	snapshot = new Set([...tasks.keys(), ...unsettled.keys()]);
	for (const listener of listeners) listener();
}

function retain(key: string, dispose: () => Promise<unknown>) {
	const pending = unsettled.get(key) ?? [];
	pending.push(dispose);
	unsettled.set(key, pending);
	publish();
}

// Run outstanding disposals first. A failure keeps them and rejects the queued
// task, so nothing can recover or replace a login a stale disposal would stop.
async function settleDisposals(key: string) {
	const pending = unsettled.get(key);
	if (!pending) return;
	while (pending.length > 0) {
		await pending[0]();
		pending.shift();
	}
	if (unsettled.get(key) !== pending) return;
	unsettled.delete(key);
	clearTimeout(retries.get(key));
	retries.delete(key);
	publish();
}

function scheduleRetry(agentId: string, hostId: string | undefined, delay = RETRY_FIRST_MS) {
	const key = agentLoginKey(agentId, hostId);
	if (retries.has(key) || !unsettled.has(key)) return;
	retries.set(key, setTimeout(() => {
		retries.delete(key);
		void runAgentLoginTask(agentId, hostId, async () => undefined)
			.catch(() => scheduleRetry(agentId, hostId, Math.min(delay * 2, RETRY_MAX_MS)));
	}, delay));
}

export function runAgentLoginTask<T>(agentId: string, hostId: string | undefined, task: () => Promise<T>): Promise<T> {
	const key = agentLoginKey(agentId, hostId);
	const previous = tasks.get(key);
	const run = async () => {
		await settleDisposals(key);
		return task();
	};
	const result = previous ? previous.then(run) : run();
	const settled = result.then(() => undefined, () => undefined).finally(() => {
		if (tasks.get(key) !== settled) return;
		tasks.delete(key);
		publish();
	});
	tasks.set(key, settled);
	publish();
	return settled.then(() => result);
}

// retainAgentLoginDisposal records a disposal that already failed inside a
// login task. It is retried, and the harness stays pending until it succeeds.
export function retainAgentLoginDisposal(agentId: string, hostId: string | undefined, dispose: () => Promise<unknown>) {
	retain(agentLoginKey(agentId, hostId), dispose);
	scheduleRetry(agentId, hostId);
}

// disposeAgentLogin stops a login the caller no longer shows. A failed attempt
// is retained and retried instead of releasing the harness.
export function disposeAgentLogin(agentId: string, hostId: string | undefined, dispose: () => Promise<unknown>): Promise<void> {
	retain(agentLoginKey(agentId, hostId), dispose);
	return runAgentLoginTask(agentId, hostId, async () => undefined).catch((err: unknown) => {
		scheduleRetry(agentId, hostId);
		throw err;
	});
}

// resetAgentLoginLifecycle forgets all state between tests.
export function resetAgentLoginLifecycle() {
	for (const timer of retries.values()) clearTimeout(timer);
	retries.clear();
	unsettled.clear();
	tasks.clear();
	publish();
}
