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

/** An explicit route never falls through to another machine's same-ID project. */
export function initialSpawnHostId({
	routeHostId,
	currentHostId,
	hosts,
}: {
	routeHostId?: string;
	currentHostId?: string;
	hosts: readonly SpawnHostOption[];
}): string | null {
	const available = (id: string | undefined) => Boolean(id && hosts.some((host) => host.id === id));
	if (routeHostId) return available(routeHostId) ? routeHostId : null;
	if (available(currentHostId)) return currentHostId!;
	return hosts[0]?.id ?? null;
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
