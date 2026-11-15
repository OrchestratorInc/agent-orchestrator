import { useRef } from "react";
import { machineIdentity, type ServerConfig } from "./config";

/**
 * The live config or, while it is gone (in the background, during an endpoint
 * race), the last one verified for the same machine.
 *
 * Only for reading memory caches that are bound to the credential that filled
 * them, such as PR summaries, so what is on screen does not blink out on an app
 * switch. Never send a request with it: its address is exactly what is no
 * longer verified, and may belong to another machine once the phone wakes.
 */
export function useLastVerifiedConfig(config: ServerConfig | null, machine: string): ServerConfig | null {
	const last = useRef<ServerConfig | null>(null);
	if (config) {
		last.current = config;
		return config;
	}
	return last.current && machine && machineIdentity(last.current) === machine ? last.current : null;
}
