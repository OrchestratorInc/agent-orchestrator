import { useCallback, useRef } from "react";
import { machineIdentity, type ServerConfig } from "./config";

/**
 * The live config or, while it is gone (in the background, during an endpoint
 * race), the last one verified for the same machine.
 *
 * Only for keeping what is already on screen through an app switch: reading
 * memory caches bound to the credential that filled them (PR summaries), or
 * the source of a page this component already loaded, which loads nothing new
 * while its props stay the same (a chat's rendered page). A component mounted
 * while the connection is away gets null, so a view that would load something
 * waits for the connection to be verified. Never send a request with it: its
 * address is exactly what is no longer verified, and may belong to another
 * machine once the phone wakes.
 */
export function useLastVerifiedConfig(config: ServerConfig | null, machine: string): ServerConfig | null {
	const last = useRef<ServerConfig | null>(null);
	if (config) {
		last.current = config;
		return config;
	}
	return last.current && machine && machineIdentity(last.current) === machine ? last.current : null;
}

/**
 * `useLastVerifiedConfig` for a screen that lists several machines' rows (the
 * pull requests): each machine's live config from `configForHost`, or the last
 * one verified for it while its connection is away. Under the same rule: only
 * for reading what is already in memory, never for a request.
 */
export function useLastVerifiedConfigs(configForHost: (hostId: string) => ServerConfig | null): (hostId: string) => ServerConfig | null {
	const last = useRef(new Map<string, ServerConfig>());
	return useCallback((hostId: string) => {
		const live = configForHost(hostId);
		if (live) {
			last.current.set(hostId, live);
			return live;
		}
		return last.current.get(hostId) ?? null;
	}, [configForHost]);
}
