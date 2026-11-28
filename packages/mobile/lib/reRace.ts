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

/** How long into an outage a machine is tried every RECOVERY_QUICK_DELAY_MS. */
export const RECOVERY_QUICK_FOR_MS = 60_000;
export const RECOVERY_QUICK_DELAY_MS = 2_000;
/** After the quick minute, until this far in, a try every RECOVERY_SOON_DELAY_MS. */
export const RECOVERY_SOON_FOR_MS = 300_000;
export const RECOVERY_SOON_DELAY_MS = 5_000;

/**
 * How long a machine that could not be reached waits before the next try,
 * given how long it has been out: every 2 s for the first minute, every 5 s
 * until five minutes, then every 15 s.
 *
 * Paced by time out, not by tries made. Counting tries (2 s, 4 s, 8 s, then
 * 15 s) spent the quick ones in the first 14 seconds, before the board even
 * called the machine unreachable, so a machine restarted the moment it was
 * marked waited up to 15 s for the phone to notice. One try at a time, each
 * waiting for the last to settle, so a slow race spaces them out on its own,
 * and a machine that is really off is tried no more often than before once
 * the outage is minutes old.
 */
export function recoveryDelayMs(outageMs: number): number {
	if (outageMs < RECOVERY_QUICK_FOR_MS) return RECOVERY_QUICK_DELAY_MS;
	if (outageMs < RECOVERY_SOON_FOR_MS) return RECOVERY_SOON_DELAY_MS;
	return RE_RACE_COOLDOWN_MS;
}
