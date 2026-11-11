import * as Device from "expo-device";
import type { ConnectDeps } from "./connect";
import type { ServerConfig } from "./config";
import { pairDevice, type PairDeviceResult } from "./deviceToken";
import { updateHostToken } from "./hosts";

// Machines that answered they have no device tokens, and when to ask again.
const askAgainAt = new Map<string, number>();
const ASK_AGAIN_MS = 60 * 60_000;

/** Exchanges the pairing secret for this phone's own token. A reconnect does not ask a
 * machine without device tokens again within the hour; pairing by hand always asks. */
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

export function deviceTokenConnectDeps(): Pick<ConnectDeps, "pair" | "saveToken"> {
	return {
		pair: (config) => pairThisDevice(config),
		saveToken: (hostId, token, fallbackToken) => updateHostToken(hostId, token, fallbackToken),
	};
}
