import { LOCAL_HOST } from "./hosts";

// Serialize startup and disposal across panel instances. In particular, a late
// response must be disposed before another panel can recover the same login.
const tasks = new Map<string, Promise<void>>();
const listeners = new Set<() => void>();
let snapshot: ReadonlySet<string> = new Set();

export const agentLoginKey = (agentId: string, hostId?: string) => JSON.stringify([hostId || LOCAL_HOST, agentId]);
export const pendingAgentLogins = () => snapshot;
export const isAgentLoginPending = (agentId: string, hostId?: string) => tasks.has(agentLoginKey(agentId, hostId));
export function subscribeAgentLogins(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

function publish() {
	snapshot = new Set(tasks.keys());
	for (const listener of listeners) listener();
}

export function runAgentLoginTask<T>(agentId: string, hostId: string | undefined, task: () => Promise<T>): Promise<T> {
	const key = agentLoginKey(agentId, hostId);
	const previous = tasks.get(key);
	const result = previous ? previous.then(task) : task();
	const settled = result.then(() => undefined, () => undefined).finally(() => {
		if (tasks.get(key) !== settled) return;
		tasks.delete(key);
		publish();
	});
	tasks.set(key, settled);
	publish();
	return settled.then(() => result);
}
