/**
 * Production wiring for the endpoint race.
 *
 * UNVERIFIED. Everything this file calls is unit-tested — race.ts, hosts.ts,
 * connect.ts, connectionAction.ts — but this glue itself is not: it needs a
 * real device or simulator, because it depends on React Native's fetch,
 * AbortSignal and timer behaviour rather than the Node equivalents vitest
 * provides. Treat the shapes as correct and the runtime behaviour as claimed
 * until it has been run. See docs for the local end-to-end check.
 */
import { type ConnectDeps, connectHost, type ConnectResult } from "./connect";
import { deviceTokenConnectDeps } from "./deviceTokenRuntime";
import type { ServerConfig } from "./config";
import { type Endpoint, endpointBaseUrl } from "./endpoints";
import { noteRequestAnswered } from "./answeredRequests";
import { shouldRetryProbe, TUNNEL_PROBE_RETRY_DELAY_MS } from "./probeRetry";
import { adoptHostIdentity, findHost, touchHost, updateHostEndpoints } from "./hosts";
import { IncompatibleHostVersionError, type ProbeAnswer, ProbeTimeoutError, raceEndpoints, type RaceOutcome } from "./race";

function identityHost(body: { hostId?: unknown; apiVersion?: unknown }): string {
	const hostId = typeof body.hostId === "string" ? body.hostId : "";
	if (hostId && body.apiVersion !== 1) throw new IncompatibleHostVersionError(hostId);
	return hostId;
}

/** How long a single endpoint gets to identify itself.
 *
 * Short on purpose. Every candidate is probed at once, so this is the ceiling
 * on the whole race, and a dead LAN address is the common case after changing
 * networks — waiting on it is exactly what the race exists to avoid. */
export const PROBE_TIMEOUT_MS = 3_000;

/**
 * How long each endpoint that ran out the short timeout gets in a second race:
 * the same as an ordinary request (see api.ts's REQUEST_TIMEOUT_MS).
 *
 * Only a timeout earns the second, slower attempt. A refused connection or a
 * different machine answering is a real answer for that endpoint, so it is not
 * asked again; but it does not stop a slow endpoint beside it (an old LAN
 * address next to a working tunnel) from getting its longer try. A link whose round trip is longer than PROBE_TIMEOUT_MS (a weak
 * cellular signal, a congested tunnel) otherwise could never connect, though
 * every request on it would have succeeded, and a working session dropped
 * whenever a periodic re-check landed on a slow moment.
 */
export const SLOW_LINK_PROBE_TIMEOUT_MS = 12_000;

export type ProbeOptions = {
	retryTunnel?: boolean;
	timeoutMs?: number;
	/**
	 * Whether our own timeout ends the attempt at once instead of earning a
	 * tunnel propagation retry. Set only by raceWithSlowLinkRetry, which gives
	 * an endpoint that ran out its time one longer try of its own; every other
	 * caller (pairing, push, manual connect) has no slow race behind it and
	 * keeps the retry. A tunnel that fails fast is still retried either way.
	 */
	endOnTimeout?: boolean;
};

/**
 * Asks an endpoint which machine it is.
 *
 * Unauthenticated by design: the answer is what decides whether this endpoint
 * is safe to send a credential to, so it has to come first. See
 * docs/adr/0003-unauthenticated-identity-probe.md.
 */
export async function probeEndpoint(endpoint: Endpoint, signal: AbortSignal, options: ProbeOptions = {}): Promise<ProbeAnswer> {
	// A tunnel hostname may simply not have propagated yet; see probeRetry.
	const startedAt = Date.now();
	for (;;) {
		try {
			return await probeOnce(endpoint, signal, options.timeoutMs);
		} catch (e) {
			if (signal.aborted) throw e; // The race already has a winner.
			if (e instanceof IncompatibleHostVersionError) throw e;
			// When the slow race (raceWithSlowLinkRetry) follows, it gives this
			// endpoint its one longer try, so retrying a timeout here would only
			// stretch a dead tunnel's attempt.
			if (options.endOnTimeout && e instanceof ProbeTimeoutError) throw e;
			if (options.retryTunnel === false || !shouldRetryProbe(endpoint.kind, Date.now() - startedAt)) throw e;
			await waitOrAbort(TUNNEL_PROBE_RETRY_DELAY_MS, signal);
		}
	}
}

/** Resolves when the delay elapses, or rejects as soon as the race is over. */
function waitOrAbort(ms: number, signal: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", onAbort);
			resolve();
		}, ms);
		function onAbort() {
			clearTimeout(timer);
			reject(new Error("probe aborted"));
		}
		signal.addEventListener("abort", onAbort, { once: true });
	});
}

async function probeOnce(endpoint: Endpoint, signal: AbortSignal, timeoutMs = PROBE_TIMEOUT_MS): Promise<ProbeAnswer> {
	const controller = new AbortController();
	let timedOut = false;
	const timeout = setTimeout(() => { timedOut = true; controller.abort(); }, timeoutMs);
	// Abort when either the race picked a winner or our own timeout fired.
	const onOuterAbort = () => controller.abort();
	signal.addEventListener("abort", onOuterAbort);
	try {
		const startedAt = Date.now();
		const res = await fetch(`${endpointBaseUrl(endpoint)}/api/v1/identity`, {
			method: "GET",
			signal: controller.signal,
		});
		noteRequestAnswered(startedAt);
		if (!res.ok) throw new Error(`identity probe returned ${res.status}`);
		const hostId = identityHost((await res.json()) as { hostId?: unknown; apiVersion?: unknown });
		if (!hostId) {
			throw new Error("identity probe returned no host id");
		}
		return { hostId };
	} catch (cause) {
		// Our own timer, not the race ending: nothing answered in time.
		if (timedOut && !signal.aborted) throw new ProbeTimeoutError(endpoint);
		throw cause;
	} finally {
		clearTimeout(timeout);
		signal.removeEventListener("abort", onOuterAbort);
	}
}

/**
 * The host id at a plain address, for a connection made by hand rather than by
 * scanning. Same endpoint as the race's probe, without a candidate to race.
 */
export async function probeIdentity(cfg: {
	host: string;
	httpPort: string;
	secure?: boolean;
}): Promise<string> {
	const base = `${cfg.secure ? "https" : "http"}://${cfg.host}:${cfg.httpPort}`;
	const res = await fetch(`${base}/api/v1/identity`, { method: "GET" });
	if (!res.ok) throw new Error(`identity probe returned ${res.status}`);
	return identityHost((await res.json()) as { hostId?: unknown; apiVersion?: unknown });
}

/** On a host mismatch (or ambiguous auth rejection), re-race verified endpoints. */
export async function rejectedEndpointNeedsRace(cfg: ServerConfig, status: number | undefined): Promise<boolean> {
	// A 429 is a lockout, and an unidentified legacy config cannot be checked.
	if (!cfg.hostId) return false;
	if (status === 421) return true;
	if (status !== 401 && status !== 403) return false;
	try {
		// The rejected URL just answered, so one bounded probe is enough here.
		const answer = await probeOnce({
			kind: cfg.endpointKind ?? "lan",
			host: cfg.host,
			port: Number(cfg.httpPort),
			secure: !!cfg.secure,
		}, new AbortController().signal);
		return answer.hostId !== cfg.hostId;
	} catch {
		return true;
	}
}

/**
 * How long the post-race endpoint refresh may take.
 *
 * The refresh runs inside the launch resolution, so without a bound a slow
 * network held the app in "connecting" for as long as the OS let a request
 * hang — after the race had already found a working endpoint. A refresh that
 * does not land in time is simply skipped; the stored endpoints still work.
 */
export const ENDPOINT_REFRESH_TIMEOUT_MS = 5_000;

export class EndpointRefreshError extends Error {
	constructor(readonly status: number, readonly code?: string) {
		super(`endpoint refresh returned ${status}`);
		this.name = "EndpointRefreshError";
	}
}

/**
 * Re-reads what the daemon advertises now.
 *
 * Deliberately GET /api/v1/endpoints and not /api/v1/mobile/status: the mobile
 * control routes are 404'd on the LAN listener, which is the only listener a
 * phone can reach.
 */
export async function fetchAdvertisedEndpoints(config: ServerConfig): Promise<Endpoint[]> {
	const controller = new AbortController();
	const timeout = setTimeout(() => controller.abort(), ENDPOINT_REFRESH_TIMEOUT_MS);
	try {
		const base = `${config.secure ? "https" : "http"}://${config.host}:${config.httpPort}`;
		const res = await fetch(`${base}/api/v1/endpoints`, {
			headers: {
				...(config.password ? { Authorization: `Bearer ${config.password}` } : {}),
				...(config.hostId ? { "X-AO-Expected-Host-ID": config.hostId } : {}),
			},
			signal: controller.signal,
		});
		if (!res.ok) {
			const body = (await res.json().catch(() => null)) as { code?: unknown } | null;
			throw new EndpointRefreshError(res.status, typeof body?.code === "string" ? body.code : undefined);
		}
		const body = (await res.json()) as { endpoints?: Endpoint[] };
		return body.endpoints ?? [];
	} finally {
		clearTimeout(timeout);
	}
}

export type ConnectOptions = {
	/**
	 * Whether to re-read the daemon's advertised endpoints after the race. On by
	 * default. The refresh is authenticated, so a stale password spends a failed
	 * attempt towards the daemon's lockout; a caller about to send its own
	 * authenticated request (Settings' Test connection) turns it off so one tap
	 * costs one attempt.
	 */
	refreshEndpoints?: boolean;
};

/**
 * The race, run once more with SLOW_LINK_PROBE_TIMEOUT_MS over the endpoints
 * that timed out the first time, when none answered as this machine (see
 * there). A fast link answers in the first race as before; an address that
 * refused or answered as another machine is not asked again. The second race
 * makes one attempt per endpoint (no tunnel propagation retry), so it is
 * bounded by SLOW_LINK_PROBE_TIMEOUT_MS. Each answer still goes through the
 * same identity and version checks.
 */
export async function raceWithSlowLinkRetry(endpoints: readonly Endpoint[], hostId: string, probe: typeof probeEndpoint = probeEndpoint): Promise<RaceOutcome> {
	const quick = await raceEndpoints(endpoints, hostId, (endpoint, signal) => probe(endpoint, signal, { endOnTimeout: true }));
	if (quick.ok || !quick.timedOut?.length) return quick;
	const slow = await raceEndpoints(quick.timedOut, hostId, (endpoint, signal) =>
		probe(endpoint, signal, { timeoutMs: SLOW_LINK_PROBE_TIMEOUT_MS, retryTunnel: false }));
	// Nothing compatible answered even slowly: an incompatible answer from the
	// first race still explains it better than "unreachable".
	return !slow.ok && quick.reason === "incompatible" ? { ok: false, reason: "incompatible" } : slow;
}

/** The production dependency set for connectHost. */
export function runtimeConnectDeps(options: ConnectOptions = {}): ConnectDeps {
	return {
		findHost,
		race: (host) => raceWithSlowLinkRetry(host.endpoints, host.id),
		// Skipped as nothing advertised: merged, that leaves the stored list as is.
		refreshEndpoints: (config) =>
			options.refreshEndpoints === false
				? Promise.resolve([])
				: fetchAdvertisedEndpoints(config),
		saveEndpoints: updateHostEndpoints,
		adoptIdentity: adoptHostIdentity,
		touch: touchHost,
		...deviceTokenConnectDeps(),
	};
}

/** Connects to a paired machine using the real network and storage. */
export function connectToHost(hostId: string, options?: ConnectOptions): Promise<ConnectResult> {
	return connectHost(hostId, runtimeConnectDeps(options));
}
