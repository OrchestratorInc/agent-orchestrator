// Proof that this phone's requests reach something: when the newest request
// that got an answer was sent. Any HTTP status counts, an error included,
// because something on the other side had to answer it.
//
// The OS's own report of the phone's network can be wrong. Android leaves a
// Wi-Fi network it could not validate (a blocked connectivity check, some
// routers and hotspots) without a default network, so the OS says "offline"
// while every request the app makes is answered (#8282). linkHealth weighs
// this against the OS report; see trustedPhoneNetwork.
//
// Free of React Native / Expo imports so the request helpers and tests can use
// it directly.

let latestStart = 0;
const listeners = new Set<() => void>();

/**
 * Records that a request sent at `startedAt` (epoch ms) got an answer. The
 * send time, not the answer time, is what counts: a request that was already
 * in flight when the phone lost its network says nothing about after.
 */
export function noteRequestAnswered(startedAt: number): void {
	if (startedAt <= latestStart) return;
	latestStart = startedAt;
	for (const listener of listeners) listener();
}

/** When the newest answered request was sent, epoch ms. 0 if none has been answered. */
export function lastAnsweredRequestAt(): number {
	return latestStart;
}

/** Calls `listener` whenever a newer request is answered. Returns the unsubscribe. */
export function onRequestAnswered(listener: () => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

export function resetAnsweredRequestsForTests(): void {
	latestStart = 0;
}
