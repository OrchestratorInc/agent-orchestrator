import type { ServerConfig } from "./config";

/** A device's own token for a machine, as the machine issues it. */
export const DEVICE_TOKEN_PREFIX = "aod_";

/** The account's token for a machine: 32 random bytes in hex. */
const ACCOUNT_TOKEN = /^[0-9a-f]{64}$/;

/**
 * Whether a saved token is a pairing secret: the pairing password, or a
 * one-time code not yet exchanged. A machine with device tokens swaps either
 * for this phone's own token.
 */
export function isPairingSecret(token: string): boolean {
	return token !== "" && !token.startsWith(DEVICE_TOKEN_PREFIX) && !ACCOUNT_TOKEN.test(token);
}

export type PairDeviceResult =
	/** The machine issued this phone its own token. credential is what was presented. */
	| { kind: "paired"; token: string; credential: "pairing-code" | "password" }
	/** The machine has no device tokens, or will not issue one: keep the token as it is. */
	| { kind: "unsupported" }
	| { kind: "unauthorized" }
	| { kind: "offline" };

export const PAIR_DEVICE_TIMEOUT_MS = 8_000;

function base(config: ServerConfig): string {
	return `${config.secure ? "https" : "http"}://${config.host}:${config.httpPort}`;
}

function headers(config: ServerConfig): Record<string, string> {
	return {
		Authorization: `Bearer ${config.password}`,
		...(config.hostId ? { "X-AO-Expected-Host-ID": config.hostId } : {}),
	};
}

/**
 * Exchanges the pairing secret in config for this phone's own token. Call it
 * only for an endpoint whose identity the race has confirmed: it sends the
 * secret.
 */
export async function pairDevice(
	config: ServerConfig,
	device: { name?: string; platform: string },
	fetchImpl: typeof fetch = fetch,
): Promise<PairDeviceResult> {
	const controller = new AbortController();
	const timeout = setTimeout(() => controller.abort(), PAIR_DEVICE_TIMEOUT_MS);
	try {
		const response = await fetchImpl(`${base(config)}/api/v1/remote-host/devices`, {
			method: "POST",
			headers: { ...headers(config), "Content-Type": "application/json" },
			body: JSON.stringify(device),
			signal: controller.signal,
		});
		if (response.status === 401) return { kind: "unauthorized" };
		// 404 and 405: a release without device tokens. 403 and 409: this
		// credential cannot pair, or the machine's device list is full.
		if ([403, 404, 405, 409, 501].includes(response.status)) return { kind: "unsupported" };
		if (!response.ok) return { kind: "offline" };
		const body = (await response.json()) as { hostId?: unknown; token?: unknown; credential?: unknown };
		if (typeof body.token !== "string" || !body.token.startsWith(DEVICE_TOKEN_PREFIX)) return { kind: "offline" };
		if (config.hostId && body.hostId !== config.hostId) return { kind: "offline" };
		return { kind: "paired", token: body.token, credential: body.credential === "pairing-code" ? "pairing-code" : "password" };
	} catch {
		return { kind: "offline" };
	} finally {
		clearTimeout(timeout);
	}
}
