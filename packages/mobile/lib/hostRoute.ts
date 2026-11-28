import { CLOUD_HOST_ID, isCloudHost } from "./cloud/mapping";
import type { ServerConfig } from "./config";
import { classifyConnectionFailure, describeConnectionFailure, isPollRejected, type ConnectionErrorCopy } from "./connectionError";
import { canRepairWithQr } from "./machineRecovery";
import { sameServerConfig } from "./sameConfig";

export type SpawnHostOption = { id: string; label: string };

export type SpawnHostState = {
	hostId: string;
	name: string;
	connection: "closed" | "connecting" | "open";
	config: ServerConfig | null;
};

/** A verified open secondary can become primary without racing its endpoints again. */
export function promotableHostConfig(hostId: string, host: SpawnHostState | undefined): ServerConfig | null {
	if (host?.hostId !== hostId || host.connection !== "open" || host.config?.hostId !== hostId) return null;
	return host.config;
}

export async function commitHostSwitch<T>(
	hostId: string,
	actions: { persist(hostId: string): Promise<void>; claim(hostId: string): T },
): Promise<T> {
	await actions.persist(hostId);
	return actions.claim(hostId);
}

/** Only a verified, currently open connection is safe to offer for spawning. */
export function connectedSpawnHosts(hosts: readonly SpawnHostState[]): SpawnHostOption[] {
	return hosts
		.filter((host) => host.connection === "open" && host.config?.hostId === host.hostId)
		.map((host) => ({ id: host.hostId, label: host.name }));
}

export type AutoSpawnHost = { settled: false } | { settled: true; hostId: string | null };

/**
 * The machine New task picks by itself, asked again on every change until the
 * person picks one or the picked one can be used. Back from the background
 * every machine is reconnecting, so a reconnecting machine is still the one
 * wanted: the route's, the restored draft's, then the default. Another machine,
 * then Cloud, only stands in once that one is offline or refused (`state`), and
 * with nothing else to use it stays, so the page says why and picks it up when
 * it is back. An explicit route never falls through to another machine.
 */
export function autoSpawnHostId({
	routeHostId,
	draftHostId,
	currentHostId,
	hosts,
	cloud,
	configResolved,
	cloudResolved,
}: {
	routeHostId?: string;
	draftHostId?: string;
	currentHostId?: string;
	hosts: ReadonlyArray<{ hostId: string; ready: boolean; state: MachineState }>;
	/** Signed in to Orchestrator Cloud. */
	cloud: boolean;
	configResolved: boolean;
	cloudResolved: boolean;
}): AutoSpawnHost {
	const paired = (id: string | undefined) => hosts.find((host) => host.hostId === id);
	if (isCloudHost(routeHostId)) {
		return configResolved && cloudResolved ? { settled: true, hostId: cloud ? routeHostId! : null } : { settled: false };
	}
	if (routeHostId) {
		if (paired(routeHostId)) return { settled: true, hostId: routeHostId };
		return configResolved ? { settled: true, hostId: null } : { settled: false };
	}
	if (!configResolved) return { settled: false };
	for (const id of [draftHostId, currentHostId]) {
		if (isCloudHost(id)) {
			if (!cloudResolved) return { settled: false };
			if (cloud) return { settled: true, hostId: id! };
			continue;
		}
		const host = paired(id);
		if (host && (host.ready || host.state === "ok")) return { settled: true, hostId: host.hostId };
	}
	const connected = hosts.find((host) => host.ready);
	if (connected) return { settled: true, hostId: connected.hostId };
	if (cloud) return { settled: true, hostId: CLOUD_HOST_ID };
	const kept = paired(draftHostId) ?? paired(currentHostId) ?? hosts[0];
	return { settled: true, hostId: kept?.hostId ?? null };
}

/**
 * A machine New task should ask again for at once instead of waiting for the
 * next recovery try: its last try failed, nothing is retrying it now, and it
 * holds no verified connection (one that does is already polling again).
 */
export function spawnHostNeedsRetry(host: { connection: string; loading: boolean; config: ServerConfig | null } | undefined): boolean {
	return Boolean(host && host.connection === "closed" && !host.loading && !host.config);
}

export function spawnHostRequestIsCurrent(
	generation: number,
	currentGeneration: number,
	requestedHostId: string,
	selectedHostId: string | null,
): boolean {
	return generation === currentGeneration && requestedHostId === selectedHostId;
}

/** Update the composer immediately; persisting/reconnecting the shared default may take longer. */
export async function selectSpawnHost(
	hostId: string,
	effects: { select(hostId: string): void; persistDefault(hostId: string): Promise<void> },
): Promise<void> {
	effects.select(hostId);
	await effects.persistDefault(hostId);
}

/** A bare local ID is never enough to open or act on another machine. */
export function hostRouteMatches(routeHostId: string | undefined, currentHostId: string | undefined): boolean {
	return Boolean(routeHostId && currentHostId && routeHostId === currentHostId);
}

/** Never render an old preview URL using a new endpoint's bearer credential. */
export function previewForConfig<T>(
	loaded: { config: ServerConfig; value: T } | null,
	config: ServerConfig | null,
	routeHostId: string | undefined,
): T | null {
	return loaded && loaded.config === config && hostRouteMatches(routeHostId, config?.hostId)
		? loaded.value
		: null;
}

/**
 * The preview to keep on screen, with the connection whose credential goes
 * with its URL: the one loaded for this connection; or, while the connection
 * is away (an app switch, an endpoint race) or back on the same endpoint with
 * the same credential, the one already showing, so its page is not loaded
 * again. Never an old URL with a different endpoint's credential, and never
 * another machine's preview.
 */
export function heldPreview<T>(
	loaded: { config: ServerConfig; value: T } | null,
	config: ServerConfig | null,
	routeHostId: string | undefined,
): { value: T; config: ServerConfig } | null {
	if (!loaded || !hostRouteMatches(routeHostId, loaded.config.hostId)) return null;
	if (previewForConfig(loaded, config, routeHostId) !== null) return { value: loaded.value, config: loaded.config };
	return !config || sameServerConfig(loaded.config, config) ? { value: loaded.value, config: loaded.config } : null;
}

/** What a screen with nothing to show yet says about its machine (see machineState). */
export type MachineState = "ok" | "offline" | "rejected";

/**
 * What a screen with nothing to show yet says about its machine, by the same
 * rule as HostScope and New task:
 *
 * - "offline": the link tracker has given up on it and its last poll failed as
 *   unreachable (`unreachable`), or no machine is paired at all;
 * - "rejected": its last poll was answered with a refusal that stopped the
 *   poll (isPollRejected): a rejected pairing, the lockout, or a version
 *   mismatch. No reconnect is coming, so waiting would spin for good; the
 *   screen says what the board says instead (rejectedMachineCopy);
 * - "ok": anything else, which is waited for. The app drops its verified
 *   connection every time it goes to the background, so a closed connection
 *   with no failed poll behind it is a reconnect: read as offline, it flashed
 *   an error after every app switch, into the app-switcher snapshot too.
 */
export function machineState(app: {
	unreachable: boolean;
	paired: boolean | null;
	connection: string;
	error: string | null;
	errorStatus: number | null;
}): MachineState {
	if (app.paired === false) return "offline";
	if (isPollRejected(app)) return "rejected";
	return app.unreachable ? "offline" : "ok";
}

/**
 * The board's own copy for a machine that refused this phone (machineState
 * "rejected"), so the same refusal reads the same on every screen. `host` is
 * the address it refused, when there is one: the lockout's copy depends on it.
 */
export function rejectedMachineCopy(errorStatus: number | null, host = ""): ConnectionErrorCopy {
	// The platform only decides the Local Network hint, which no refusal shows.
	return describeConnectionFailure(classifyConnectionFailure(errorStatus ?? undefined), { host, port: "", platform: "" });
}

/** The same copy as one line, for a place with room for a sentence only. */
export function rejectedMachineLine(errorStatus: number | null, host = ""): string {
	const copy = rejectedMachineCopy(errorStatus, host);
	return `${copy.title}. ${copy.message}`;
}

/** Whether a new pairing code fixes this refusal (a rejected pairing), so a screen offers to scan one. */
export function rejectionRepairsWithQr(errorStatus: number | null): boolean {
	return canRepairWithQr(classifyConnectionFailure(errorStatus ?? undefined));
}
