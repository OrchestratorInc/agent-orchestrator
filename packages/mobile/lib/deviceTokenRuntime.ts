import * as Device from "expo-device";
import type { ConnectDeps } from "./connect";
import type { ServerConfig } from "./config";
import { checkDeviceToken, pairDevice, type PairDeviceResult } from "./deviceToken";
import { updateHostToken } from "./hosts";

// Machines that answered they have no device tokens, and when to ask again.
const askAgainAt = new Map<string, number>();
const ASK_AGAIN_MS = 60 * 60_000;

/**
 * Exchanges the pairing secret in config for this phone's own token. A
 * reconnect does not ask a machine again within the hour after it answered
 * that it has no device tokens; pairing by hand (fresh) always asks.
 */
export async function pairThisDevice(config: ServerConfig, options: { fresh?: boolean } = {}): Promise<PairDeviceResult> {
	const machine = config.hostId ?? "";
	if (!options.fresh && machine && Date.now() < (askAgainAt.get(machine) ?? 0)) return { kind: "unsupported" };
	const result = await pairDevice(config, { name: Device.deviceName ?? undefined, platform: Device.osName === "Android" ? "android" : "ios" });
	if (machine) {
		if (result.kind === "unsupported") askAgainAt.set(machine, Date.now() + ASK_AGAIN_MS);
		else askAgainAt.delete(machine);
	}
	return result;
}

/** The device-token part of the production dependency set for connectHost. */
export function deviceTokenConnectDeps(): Pick<ConnectDeps, "pair" | "checkToken" | "saveToken"> {
	return {
		pair: (config) => pairThisDevice(config),
		checkToken: (config) => checkDeviceToken(config),
		saveToken: (hostId, token, fallbackToken) => updateHostToken(hostId, token, fallbackToken),
	};
}
