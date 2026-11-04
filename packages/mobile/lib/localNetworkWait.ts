import { isLocalNetworkHost } from "./connectionError";
import type { Endpoint } from "./endpoints";
import type { RaceOutcome } from "./race";

/**
 * How long a scan keeps trying a LAN address that iOS may still be holding
 * behind its Local Network prompt; past it, the failure is reported as usual.
 *
 * Short because it is also how long someone on the wrong network waits for the
 * error: off the machine's subnet iOS shows no prompt at all, yet fails each
 * request with the same instant "offline" error, so the two cannot be told
 * apart from here. People tapped Allow about 3 s after the prompt appeared on
 * an iPhone 13; an answer that comes later still connects, because the pair
 * screen retries when the prompt is answered (see ScanGate.promptAnswered).
 */
export const LOCAL_NETWORK_WAIT_MS = 10_000;

/**
 * How long to keep trying after the prompt has been answered. Allow takes
 * effect within a second; a machine still silent after this is out of reach.
 */
export const LOCAL_NETWORK_SETTLE_MS = 3_000;

/** Between attempts. Each one fails at once while access is not granted, so
 *  this is how soon after Allow the connection lands. */
export const LOCAL_NETWORK_RETRY_MS = 1_000;

/**
 * Whether a scan that reached nothing may only be waiting on iOS's Local
 * Network prompt.
 *
 * On a fresh install the first request to a LAN address fails immediately and
 * raises the prompt; the request never leaves the phone. There is no API to ask
 * whether access was granted, so `confirmed` records that a LAN request has
 * gone through before: after that, reaching nothing means the machine really
 * is unreachable.
 */
export function mayAwaitLocalNetwork(
	endpoints: readonly Endpoint[],
	platform: string,
	confirmed: boolean,
): boolean {
	return platform === "ios" && !confirmed && endpoints.some((e) => isLocalNetworkHost(e.host));
}

export type LocalNetworkWaitOptions = {
	now: () => number;
	sleep: (ms: number) => Promise<void>;
	/** True once the user has left the screen; stops further attempts. */
	stopped: () => boolean;
	/**
	 * When the user answered the prompt, or null if they have not yet. Tapping
	 * Allow or Don't Allow sends the app inactive and straight back to active,
	 * which is the only signal iOS gives.
	 */
	answeredAt: () => number | null;
	/** Called before each retry, so the screen can say it is still connecting. */
	onWaiting: () => void;
};

/**
 * Races the code's endpoints, and while nothing answers keeps racing them for
 * up to {@link LOCAL_NETWORK_WAIT_MS}, so tapping Allow on the prompt connects
 * without the user doing anything else.
 *
 * Retries on a timer rather than once when the app returns to the
 * foreground: on an iPhone 13 (iOS 27.2) the app was active again 8 ms after
 * Allow, before the grant applied, so a single retry then still failed. The
 * foreground return only shortens the wait (see `answeredAt`).
 */
export async function raceAwaitingLocalNetwork(
	race: () => Promise<RaceOutcome>,
	options: LocalNetworkWaitOptions,
): Promise<RaceOutcome> {
	const startedAt = options.now();
	let outcome = await race();
	const waiting = () => {
		const now = options.now();
		const answered = options.answeredAt();
		if (answered !== null && now - answered >= LOCAL_NETWORK_SETTLE_MS) return false;
		return now - startedAt < LOCAL_NETWORK_WAIT_MS;
	};
	while (!outcome.ok && outcome.reason === "none-reachable" && waiting() && !options.stopped()) {
		options.onWaiting();
		await options.sleep(LOCAL_NETWORK_RETRY_MS);
		if (options.stopped()) break;
		outcome = await race();
	}
	return outcome;
}
