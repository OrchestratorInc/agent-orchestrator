import * as Device from "expo-device";
import type { ConnectDeps } from "./connect";
import type { ServerConfig } from "./config";
import { pairDevice, releaseDeviceToken, type PairDeviceResult } from "./deviceToken";
import { updateHostToken } from "./hosts";

const askAgainAt = new Map<string, number>();
const ASK_AGAIN_MS = 60 * 60_000;
// A token issued for a one-time code, so a retry with the same, now spent, code gets it back.
// A password is not spent, so a retry with it pairs again.
const issued = new Map<string, { result: Extract<PairDeviceResult, { kind: "paired" }>; until: number }>();
const REUSE_MS = 10 * 60_000;
// Without a machine id the address stands in, so one machine's token is never handed to another.
const issuedKey = (config: ServerConfig) => `${config.hostId ? `host:${config.hostId}` : `address:${config.secure ? "https" : "http"}://${config.host}:${config.httpPort}`}\n${config.password}`;

/** A reconnect does not ask a machine without device tokens again within the hour; pairing by hand always asks. */
export async function pairThisDevice(config: ServerConfig, options: { fresh?: boolean } = {}): Promise<PairDeviceResult> {
	const machine = config.hostId ?? "";
	const kept = issued.get(issuedKey(config));
	if (kept && Date.now() < kept.until) return kept.result;
	if (!options.fresh && machine && Date.now() < (askAgainAt.get(machine) ?? 0)) return { kind: "unsupported" };
	const result = await pairDevice(config, { name: Device.deviceName ?? undefined, platform: Device.osName === "Android" ? "android" : "ios" });
	if (result.kind === "paired" && result.credential === "pairing-code") issued.set(issuedKey(config), { result, until: Date.now() + REUSE_MS });
	if (machine) {
		if (result.kind === "unsupported") askAgainAt.set(machine, Date.now() + ASK_AGAIN_MS);
		else askAgainAt.delete(machine);
	}
	return result;
}

/** Hands back a token issued for a pairing the user abandoned. */
export async function releaseThisDevice(config: ServerConfig): Promise<void> {
	for (const [key, kept] of issued) if (kept.result.token === config.password) issued.delete(key);
	await releaseDeviceToken(config);
}

export function deviceTokenConnectDeps(): Pick<ConnectDeps, "pair" | "saveToken"> {
	return {
		pair: (config) => pairThisDevice(config),
		saveToken: (hostId, token, fallbackToken) => updateHostToken(hostId, token, fallbackToken),
	};
}
