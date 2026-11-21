import { DEFAULT_CONFIG, type ServerConfig } from "./config";
import { DEVICE_TOKEN_PREFIX, isPairingSecret, type PairDeviceResult } from "./deviceToken";
import type { Endpoint } from "./endpoints";
import { mergeEndpoints } from "./mergeEndpoints";
import type { Host } from "./hosts";
import type { RaceOutcome } from "./race";

/**
 * Adapts a won endpoint into the shape the API layer already speaks.
 *
 * Everything downstream — api.ts, mux.ts, the terminal — is built on
 * ServerConfig, so the race hands back one of those rather than threading a new
 * type through every call site.
 */
export function configForEndpoint(endpoint: Endpoint, token: string, hostId = ""): ServerConfig {
	return {
		...DEFAULT_CONFIG,
		host: endpoint.host,
		httpPort: String(endpoint.port),
		secure: endpoint.secure,
		password: token,
		// Carried so per-machine state — the chat event cursor above all — keys on
		// the machine rather than whichever address won the race.
		...(hostId ? { hostId } : {}),
		// Carried so the poll can speed up on paths where the event stream
		// cannot deliver. See pollInterval.ts.
		endpointKind: endpoint.kind,
	};
}

export type ConnectResult =
	| { ok: true; config: ServerConfig; endpoint: Endpoint; hostId: string }
	| { ok: false; reason: "unknown-host" | "no-candidates" | "none-reachable" | "incompatible" };

export type ConnectDeps = {
	findHost: (id: string) => Promise<Host | null>;
	race: (host: Host) => Promise<RaceOutcome>;
	/** Asks the daemon what it advertises now, over the endpoint that just won. */
	refreshEndpoints: (config: ServerConfig) => Promise<Endpoint[]>;
	saveEndpoints: (hostId: string, endpoints: Endpoint[]) => Promise<void>;
	/** Records the identity a machine reported that had none stored. */
	adoptIdentity: (oldId: string, hostId: string) => Promise<void>;
	touch: (hostId: string) => Promise<void>;
	pair?: (config: ServerConfig) => Promise<PairDeviceResult>;
	saveToken?: (hostId: string, token: string, fallbackToken?: string) => Promise<void>;
};

/**
 * Connects to a paired machine: race its endpoints, then refresh what we know
 * about it from whichever one won.
 *
 * The refresh is what makes the pairing self-healing. A tunnel hostname that
 * rotated, a LAN address that changed with the lease, a machine that gained
 * Tailscale — all of it is picked up on the next successful connect without the
 * user re-pairing.
 */
export async function connectHost(id: string, deps: ConnectDeps): Promise<ConnectResult> {
	const host = await deps.findHost(id);
	if (!host) return { ok: false, reason: "unknown-host" };

	const outcome = await deps.race(host);
	if (!outcome.ok) return { ok: false, reason: outcome.reason };

	const hostKeyForConfig = host.id === "" ? outcome.hostId : host.id;
	let config = configForEndpoint(outcome.endpoint, host.token, hostKeyForConfig);

	// A machine migrated from the single-server config has no id until it
	// connects once. This is where it learns one, and it is verified from here on.
	if (host.id === "" && outcome.hostId !== "") {
		await deps.adoptIdentity(host.id, outcome.hostId);
	}

	const hostKey = host.id === "" ? outcome.hostId : host.id;

	let token = host.token;
	const swapPairingPassword = async (): Promise<PairDeviceResult["kind"] | undefined> => {
		if (!deps.saveToken || !deps.pair || !isPairingSecret(token)) return undefined;
		const paired = await deps.pair(config);
		if (paired.kind !== "paired") return paired.kind;
		await deps.saveToken(hostKey, paired.token, paired.credential === "password" ? token : undefined);
		token = paired.token;
		config = configForEndpoint(outcome.endpoint, token, hostKeyForConfig);
		return paired.kind;
	};
	const refreshEndpoints = async () => {
		const advertised = await deps.refreshEndpoints(config);
		// The daemon cannot advertise a client-side NAT or proxy address that just worked.
		const refreshed = advertised.some((e) => e.kind === outcome.endpoint.kind) &&
			!advertised.some((e) => e.kind === outcome.endpoint.kind && e.host === outcome.endpoint.host && e.port === outcome.endpoint.port && e.secure === outcome.endpoint.secure)
			? [...advertised, outcome.endpoint] : advertised;
		// Merged, not replaced: a kind the daemon omits, a tunnel mid-restart
		// above all, is unknown rather than gone. See mergeEndpoints.
		await deps.saveEndpoints(
			hostKey,
			mergeEndpoints(host.endpoints, refreshed),
		);
	};

	// A password the machine just refused would be refused again by the refresh,
	// one more failure toward its lockout.
	if (await swapPairingPassword() === "unauthorized") {
		await deps.touch(hostKey);
		return { ok: true, config, endpoint: outcome.endpoint, hostId: hostKey };
	}
	try {
		await refreshEndpoints();
	} catch (error) {
		// A failed refresh must not cost us a working connection: the endpoints
		// already stored are what got us here and remain good enough.
		//
		// One failure is acted on: only a release without device tokens, as after a
		// rollback, refuses this phone's token with BAD_PASSWORD; the kept password works there.
		const { status, code } = (typeof error === "object" && error !== null ? error : {}) as { status?: unknown; code?: unknown };
		if (deps.saveToken && host.fallbackToken && token.startsWith(DEVICE_TOKEN_PREFIX) && status === 401 && code === "BAD_PASSWORD") {
			const deviceToken = token;
			token = host.fallbackToken;
			config = configForEndpoint(outcome.endpoint, token, hostKeyForConfig);
			try {
				// The device token is replaced only once the kept password works.
				await refreshEndpoints();
				await deps.saveToken(hostKey, token);
				await swapPairingPassword();
			} catch {
				token = deviceToken;
				config = configForEndpoint(outcome.endpoint, token, hostKeyForConfig);
			}
		}
	}
	await deps.touch(hostKey);

	return { ok: true, config, endpoint: outcome.endpoint, hostId: hostKey };
}
