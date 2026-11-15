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
	| {
		ok: true;
		config: ServerConfig;
		endpoint: Endpoint;
		hostId: string;
		/**
		 * Present when the connection was handed back before its endpoint refresh
		 * finished. Resolves true if that refresh then found the token refused
		 * and stored the kept password in its place: `config` still carries the
		 * refused token, so whoever uses it should connect again.
		 */
		credentialRepair?: Promise<boolean>;
	}
	| { ok: false; reason: "unknown-host" | "no-candidates" | "none-reachable" | "incompatible" };

export type ConnectDeps = {
	findHost: (id: string) => Promise<Host | null>;
	race: (host: Host) => Promise<RaceOutcome>;
	/** Asks the daemon what it advertises now, over the endpoint that just won. */
	refreshEndpoints: (config: ServerConfig) => Promise<Endpoint[]>;
	/** With `unchangedSince`, saves only if no other save landed after that revision. */
	saveEndpoints: (hostId: string, endpoints: Endpoint[], unchangedSince?: number) => Promise<void>;
	/** The revision of the stored endpoint list a host from findHost was read at. */
	endpointRevision?: (host: Host) => number | undefined;
	/** Records the identity a machine reported that had none stored. */
	adoptIdentity: (oldId: string, hostId: string) => Promise<void>;
	touch: (hostId: string) => Promise<void>;
	pair?: (config: ServerConfig) => Promise<PairDeviceResult>;
	saveToken?: (hostId: string, token: string, fallbackToken?: string) => Promise<void>;
	/** How long connect waits for the endpoint refresh before using the connection; see REFRESH_WAIT_MS. */
	refreshWaitMs?: number;
};

/**
 * How long a connect waits for the endpoint refresh before handing back the
 * connection that just answered.
 *
 * The refresh is a second round trip after the race, and every connect and
 * resume waited for it before the board, the chat's live updates or anything
 * else could start: on a slow link, seconds of "reconnecting" over a machine
 * that had already answered. A fast link finishes it well inside this; a slow
 * one gets the connection now and lets the refresh finish behind it. What it
 * learns is for the next race. Its one other job, falling back to the kept
 * pairing password when a release without device tokens refuses this phone's
 * token, still happens; landing late, the result's `credentialRepair` says so,
 * so the caller connects again with the stored password.
 */
export const REFRESH_WAIT_MS = 1_000;

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
	// The revision host.endpoints was read at: the refresh below merges into
	// that list, so it must not save over a list a later connection stored.
	const endpointsRevision = host.id ? deps.endpointRevision?.(host) : undefined;

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
			...(endpointsRevision === undefined ? [] : [endpointsRevision]),
		);
	};

	// A password the machine just refused would be refused again by the refresh,
	// one more failure toward its lockout.
	if (await swapPairingPassword() === "unauthorized") {
		await deps.touch(hostKey);
		return { ok: true, config, endpoint: outcome.endpoint, hostId: hostKey };
	}
	let repaired = false;
	const refreshing = (async () => { try {
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
				repaired = true;
				await swapPairingPassword();
			} catch {
				token = deviceToken;
				config = configForEndpoint(outcome.endpoint, token, hostKeyForConfig);
			}
		}
	} })();
	let waited: ReturnType<typeof setTimeout> | undefined;
	let refreshed = false;
	await Promise.race([
		refreshing.then(() => { refreshed = true; }),
		new Promise<void>((resolve) => { waited = setTimeout(resolve, deps.refreshWaitMs ?? REFRESH_WAIT_MS); }),
	]);
	clearTimeout(waited);
	await deps.touch(hostKey);

	if (refreshed) return { ok: true, config, endpoint: outcome.endpoint, hostId: hostKey };
	// Handed back before the refresh finished: a late fallback to the kept
	// password cannot change the config returned here, so it is reported.
	const handedBack = config;
	return {
		ok: true,
		config: handedBack,
		endpoint: outcome.endpoint,
		hostId: hostKey,
		credentialRepair: refreshing.then(() => repaired && config.password !== handedBack.password),
	};
}
