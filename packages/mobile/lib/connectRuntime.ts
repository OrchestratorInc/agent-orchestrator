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
import { IncompatibleHostVersionError, type ProbeAnswer, ProbeTimeoutError, raceEndpoints } from "./race";

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
 * How long each endpoint gets when every one of them ran out the short
 * timeout: the same as an ordinary request (see api.ts's REQUEST_TIMEOUT_MS).
 *
 * Only a timeout earns the second, slower race. A refused connection or a
 * different machine answering is a real answer and still ends the race at
 * once. A link whose round trip is longer than PROBE_TIMEOUT_MS (a weak
 * cellular signal, a congested tunnel) otherwise could never connect, though
 * every request on it would have succeeded, and a working session dropped
 * whenever a periodic re-check landed on a slow moment.
 */
export const SLOW_LINK_PROBE_TIMEOUT_MS = 12_000;

/**
 * Asks an endpoint which machine it is.
 *
 * Unauthenticated by design: the answer is what decides whether this endpoint
 * is safe to send a credential to, so it has to come first. See
 * docs/adr/0003-unauthenticated-identity-probe.md.
 */
export async function probeEndpoint(endpoint: Endpoint, signal: AbortSignal, options: { retryTunnel?: boolean; timeoutMs?: number } = {}): Promise<ProbeAnswer> {
	// A tunnel hostname may simply not have propagated yet; see probeRetry.
	const startedAt = Date.now();
	for (;;) {
		try {
			return await probeOnce(endpoint, signal, options.timeoutMs);
		} catch (e) {
			if (signal.aborted) throw e; // The race already has a winner.
			if (e instanceof IncompatibleHostVersionError) throw e;
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
 * The race, run once more with SLOW_LINK_PROBE_TIMEOUT_MS when every endpoint
 * timed out the first time (see there). A fast link answers in the first race
 * as before; a dead address that refuses still fails at once.
 */
export async function raceWithSlowLinkRetry(endpoints: readonly Endpoint[], hostId: string, probe: typeof probeEndpoint = probeEndpoint, now: () => number = Date.now) {
	const slow = (endpoint: Endpoint, signal: AbortSignal) => probe(endpoint, signal, { timeoutMs: SLOW_LINK_PROBE_TIMEOUT_MS });
	// A machine that last answered only on the slow race goes straight to it:
	// the quick race would spend PROBE_TIMEOUT_MS failing first on every
	// resume. The longer limit is only a ceiling, so a link that got fast
	// again still answers at once.
	const knownSlow = hostId !== "" && now() - (slowLinkAt.get(hostId) ?? -Infinity) < SLOW_LINK_MEMORY_MS;
	if (knownSlow) {
		const outcome = await raceEndpoints(endpoints, hostId, slow);
		if (!outcome.ok) slowLinkAt.delete(hostId);
		return outcome;
	}
	const quick = await raceEndpoints(endpoints, hostId, probe);
	if (quick.ok || !quick.allTimedOut) return quick;
	const outcome = await raceEndpoints(endpoints, hostId, slow);
	if (outcome.ok && hostId !== "") slowLinkAt.set(hostId, now());
	return outcome;
}

/** How long a machine that needed the slow race is raced that way first. */
export const SLOW_LINK_MEMORY_MS = 10 * 60 * 1000;
const slowLinkAt = new Map<string, number>();

/** For tests: forget which machines were slow. */
export function forgetSlowLinks(): void {
	slowLinkAt.clear();
}

/**
 * Whether the connection in use still reaches its machine: its own address,
 * asked who it is with the slow-link limit. A re-check (a network change, the
 * look for a better address) whose race found nothing keeps a connection that
 * still answers instead of dropping it.
 */
export async function endpointStillAnswers(cfg: ServerConfig): Promise<boolean> {
	if (!cfg.hostId) return false;
	try {
		const answer = await probeOnce({
			kind: cfg.endpointKind ?? "lan",
			host: cfg.host,
			port: Number(cfg.httpPort),
			secure: !!cfg.secure,
		}, new AbortController().signal, SLOW_LINK_PROBE_TIMEOUT_MS);
		return answer.hostId === cfg.hostId;
	} catch {
		return false;
	}
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
