import type { ServerConfig } from "./config";

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
