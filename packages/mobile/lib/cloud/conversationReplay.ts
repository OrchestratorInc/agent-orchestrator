import type { ClientEvent } from "@aoagents/cloud-client";

export type CloudReplayEntry = { events: ClientEvent[]; cursor: number };

/** Cancels this consumer, not another consumer's shared bearer refresh. */
export function awaitCloudReplay<T>(promise: Promise<T>, signal?: AbortSignal): Promise<T> {
	if (!signal) return promise;
	if (signal.aborted) return Promise.reject(signal.reason ?? Object.assign(new Error("Cloud request cancelled."), { name: "AbortError" }));
	return new Promise<T>((resolve, reject) => {
		const abort = () => { cleanup(); reject(signal.reason ?? Object.assign(new Error("Cloud request cancelled."), { name: "AbortError" })); };
		const cleanup = () => signal.removeEventListener("abort", abort);
		signal.addEventListener("abort", abort, { once: true });
		promise.then((value) => { cleanup(); resolve(value); }, (error) => { cleanup(); reject(error); });
	});
}
/** Keep cursor and raw history inseparable. Large active views can be pinned;
 * inactive entries obey the mobile cache's two-megabyte weighted budget. */
export function createCloudConversationReplayCache(maxEntries = 8) {
	const entries = new Map<string, CloudReplayEntry & { scopeKey: string; weight: number }>();
	const pins = new Map<string, number>();
	const observers = new Map<string, Set<() => void>>();
	let generation = 0;
	const scopeGenerations = new Map<string, number>();
	const keyFor = (scopeKey: string, sessionId: string) => JSON.stringify([scopeKey, sessionId]);
	const prune = () => {
		let weight = [...entries.values()].reduce((sum, entry) => sum + entry.weight, 0);
		for (const [key, entry] of entries) {
			if (entries.size <= Math.max(1, maxEntries) && weight <= 2 * 1024 * 1024) break;
			if (pins.has(key)) continue;
			entries.delete(key); weight -= entry.weight;
		}
	};
	return {
		watchScope(scopeKey: string, callback: () => void): () => void {
			const listeners = observers.get(scopeKey) ?? new Set<() => void>();
			listeners.add(callback); observers.set(scopeKey, listeners);
			return () => { listeners.delete(callback); if (!listeners.size && observers.get(scopeKey) === listeners) observers.delete(scopeKey); };
		},
		get(scopeKey: string, sessionId: string): CloudReplayEntry | undefined {
			const key = keyFor(scopeKey, sessionId); const entry = entries.get(key);
			if (!entry) return undefined;
			entries.delete(key); entries.set(key, entry);
			return { events: [...entry.events], cursor: entry.cursor };
		},
		apply(scopeKey: string, sessionId: string, incoming: readonly ClientEvent[], cursor: number): CloudReplayEntry {
			const key = keyFor(scopeKey, sessionId); const previous = entries.get(key);
			const bySequence = new Map(previous?.events.map((event) => [event.sequence, event]) ?? []);
			for (const event of incoming) {
				if (event.sessionId !== sessionId || !Number.isSafeInteger(event.sequence) || event.sequence < 1 || bySequence.has(event.sequence)) continue;
				bySequence.set(event.sequence, event);
			}
			const events = [...bySequence.values()].sort((a, b) => a.sequence - b.sequence);
			// Empty pages cannot authorize dropping unseen history. A server cursor
			// ahead of the adopted tail is ignored, not saved as a future starting point.
			const adopted = Math.max(previous?.cursor ?? 0, events.at(-1)?.sequence ?? 0);
			const next = { events, cursor: adopted };
			void cursor;
			entries.delete(key); entries.set(key, { ...next, scopeKey, weight: JSON.stringify(events).length * 2 }); prune();
			return { events: [...events], cursor: adopted };
		},
		pin(scopeKey: string, sessionId: string): () => void {
			const key = keyFor(scopeKey, sessionId); pins.set(key, (pins.get(key) ?? 0) + 1);
			const initialGeneration = generation; const scopeGeneration = scopeGenerations.get(scopeKey);
			let released = false;
			return () => { if (released) return; released = true; if (generation !== initialGeneration || scopeGenerations.get(scopeKey) !== scopeGeneration) return; const count = (pins.get(key) ?? 0) - 1; if (count > 0) pins.set(key, count); else pins.delete(key); prune(); };
		},
		clearScope(scopeKey: string) {
			scopeGenerations.set(scopeKey, (scopeGenerations.get(scopeKey) ?? 0) + 1);
			for (const callback of observers.get(scopeKey) ?? []) callback();
			observers.delete(scopeKey);
			for (const [key, entry] of entries) if (entry.scopeKey === scopeKey) entries.delete(key);
			for (const key of pins.keys()) if (JSON.parse(key)[0] === scopeKey) pins.delete(key);
		},
		clearAccount(baseUrl: string, userId: string) {
			const scopes = new Set([...observers.keys(), ...[...entries.values()].map((entry) => entry.scopeKey), ...[...pins.keys()].map((key) => JSON.parse(key)[0] as string)]);
			for (const scopeKey of scopes) {
				let scope: unknown; try { scope = JSON.parse(scopeKey); } catch { continue; }
				if (Array.isArray(scope) && scope[0] === baseUrl.replace(/\/+$/, "") && scope[1] === userId) this.clearScope(scopeKey);
			}
		},
		clear() { generation++; for (const listeners of observers.values()) for (const callback of listeners) callback(); observers.clear(); entries.clear(); pins.clear(); scopeGenerations.clear(); },
	};
}
export const cloudConversationReplayCache = createCloudConversationReplayCache();
