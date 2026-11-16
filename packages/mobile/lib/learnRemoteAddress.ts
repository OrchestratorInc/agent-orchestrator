import type { Endpoint } from "./endpoints";
import { mergeEndpoints } from "./mergeEndpoints";

/**
 * How often, and for how long, the app asks a connected machine for an
 * internet address the phone does not know yet. A code scanned in the seconds
 * before the machine's internet address was ready holds only that network's
 * addresses: the phone connects locally, and would lose the machine once it
 * leaves the network. Asking again while the app is open picks the address up
 * as soon as the machine advertises it.
 */
// Measured: the address is ready 15-20 s after the code shows, so 5 s learns it
// soon after, and 2 minutes covers a slow start. Each ask is one small request
// over the local network.
export const LEARN_REMOTE_EVERY_MS = 5_000;
export const LEARN_REMOTE_FOR_MS = 2 * 60_000;

/** Whether stored endpoints reach their machine from any network. */
export function knowsRemoteAddress(endpoints: readonly Endpoint[]): boolean {
	return endpoints.some((endpoint) => endpoint.kind === "tunnel" || endpoint.kind === "relay");
}

/**
 * Saves the machine's advertised endpoints, merged into the stored ones, once
 * they include an internet address. Reports whether the phone knows one now.
 */
export async function learnRemoteAddress(
	stored: readonly Endpoint[],
	advertised: () => Promise<Endpoint[]>,
	save: (endpoints: Endpoint[]) => Promise<void>,
): Promise<boolean> {
	const fresh = await advertised();
	if (!knowsRemoteAddress(fresh)) return false;
	await save(mergeEndpoints([...stored], fresh));
	return true;
}
