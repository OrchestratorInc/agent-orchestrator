/**
 * When a run of failed polls should trigger racing the endpoints again.
 *
 * This is the mechanism that recovers a session when the network changes.
 * There is no network-change listener on this platform build, and leaving a
 * Wi-Fi network usually makes the socket hang rather than error, so a run of
 * failed polls is the signal that the endpoint we chose is gone and the others
 * deserve another try — which is how a phone that paired over LAN ends up on
 * the tunnel after Wi-Fi is switched off.
 */

/**
 * How many consecutive failed polls count as a dead endpoint.
 *
 * Not one: a single failure is routine — a dropped packet, a radio waking up,
 * a daemon mid-restart — and re-racing on every blip would thrash the
 * connection for no benefit.
 */
export const RE_RACE_AFTER_FAILURES = 2;

/**
 * Minimum gap between races.
 *
 * A genuinely offline phone fails every poll forever; without this it would
 * race continuously, draining the battery and hammering the daemon the moment
 * it came back.
 */
export const RE_RACE_COOLDOWN_MS = 15_000;

export type ReRaceState = {
	consecutiveFailures: number;
	/** Epoch ms of the last race, or 0 if there has not been one. */
	lastReRaceAt: number;
	now: number;
	/**
	 * The last failure had no HTTP status: nothing answered at all.
	 *
	 * This is what leaving a network looks like, and it is qualitatively
	 * different from a server replying with an error — that server is reachable,
	 * so racing would rediscover the same endpoint and change nothing.
	 */
	unreachable: boolean;
};

export function shouldReRace(state: ReRaceState): boolean {
	// An unreachable endpoint is not a blip to ride out. Waiting for a second
	// failure costs another poll interval plus another request timeout — up to
	// forty seconds of the app looking broken while a working endpoint sits
	// unused.
	const enough = state.unreachable
		? state.consecutiveFailures >= 1
		: state.consecutiveFailures >= RE_RACE_AFTER_FAILURES;
	if (!enough) return false;
	// A first failure run has nothing to wait behind.
	if (state.lastReRaceAt === 0) return true;
	return state.now - state.lastReRaceAt >= RE_RACE_COOLDOWN_MS;
}

/**
 * How long a machine that could not be reached waits before the next try:
 * 2 s, 4 s, 8 s, then every 15 s. A flat 15 s left the app saying "can't
 * reach" for up to that long after a blip it had already recovered from;
 * starting short catches the common case (a network that took a moment to
 * come up), and the cap keeps a machine that is really off from being raced
 * continuously.
 */
export function recoveryDelayMs(attempt: number): number {
	return [2_000, 4_000, 8_000][attempt] ?? RE_RACE_COOLDOWN_MS;
}
