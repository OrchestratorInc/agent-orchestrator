import { useCallback, useSyncExternalStore } from "react";
import type { CloudCpSessionLive } from "../lib/cloud-cp";
import { subscribeSessionLiveBridged } from "../lib/cloud-cp/stream-bridge";
import { useCloudCp } from "./useCloudCp";

const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;

interface LiveSubscription {
	state: CloudCpSessionLive | undefined;
	listeners: Set<() => void>;
	users: number;
	stop: () => void;
}

const subscriptions = new Map<string, LiveSubscription>();

/**
 * One live stream per session however many components show it, since each
 * stream lists its person as a viewer: the chat and the session header share
 * it. It reconnects with backoff, and its state is undefined until the first
 * frame and again after a reconnect fails.
 */
function acquire(key: string, open: (onLive: (state: CloudCpSessionLive | undefined) => void, signal: AbortSignal) => Promise<boolean>): LiveSubscription {
	const existing = subscriptions.get(key);
	if (existing) {
		existing.users += 1;
		return existing;
	}
	const controller = new AbortController();
	let timer: number | undefined;
	const subscription: LiveSubscription = {
		state: undefined,
		listeners: new Set(),
		users: 1,
		stop: () => {
			controller.abort();
			if (timer !== undefined) window.clearTimeout(timer);
		},
	};
	const publish = (state: CloudCpSessionLive | undefined) => {
		subscription.state = state;
		for (const listener of subscription.listeners) listener();
	};
	let delay = RECONNECT_MIN_MS;
	const connect = async () => {
		const received = await open(publish, controller.signal);
		if (controller.signal.aborted) return;
		publish(undefined);
		delay = received ? RECONNECT_MIN_MS : Math.min(delay * 2, RECONNECT_MAX_MS);
		timer = window.setTimeout(() => void connect(), delay);
	};
	subscriptions.set(key, subscription);
	void connect();
	return subscription;
}

function release(key: string, subscription: LiveSubscription) {
	subscription.users -= 1;
	if (subscription.users > 0) return;
	subscription.stop();
	if (subscriptions.get(key) === subscription) subscriptions.delete(key);
}

/**
 * A cloud session's live state while it is on screen: who else has it open
 * and who holds its composer. Having it open lists this person as a viewer
 * for everyone else, once however many components ask.
 */
export function useCloudSessionLive(orgId: string | undefined, sessionId: string | undefined, enabled: boolean): CloudCpSessionLive | undefined {
	const { baseUrl, ready } = useCloudCp(enabled);
	const active = Boolean(enabled && ready && baseUrl && orgId && sessionId);
	const key = `${baseUrl}|${orgId ?? ""}|${sessionId ?? ""}`;
	const subscribe = useCallback((listener: () => void) => {
		if (!active || !orgId || !sessionId) return () => undefined;
		const subscription = acquire(key, async (onLive, signal) => {
			let received = false;
			await subscribeSessionLiveBridged({
				baseUrl, orgId, sessionId, signal,
				onLive: (state) => {
					received = true;
					onLive(state);
				},
			});
			return received;
		});
		subscription.listeners.add(listener);
		return () => {
			subscription.listeners.delete(listener);
			release(key, subscription);
		};
	}, [active, baseUrl, key, orgId, sessionId]);
	const snapshot = useCallback(() => (active ? subscriptions.get(key)?.state : undefined), [active, key]);
	return useSyncExternalStore(subscribe, snapshot);
}
