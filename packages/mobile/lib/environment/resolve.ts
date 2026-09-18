import { isConfigured, type ServerConfig } from "../config";
import { sameServerConfig } from "../sameConfig";
import { createLocalSessionSource } from "./local";
import type { SessionSource } from "./types";

// Memoised on the config's identity (via sameServerConfig) so the store hands
// the same instance to every consumer between config changes. See
// sameServerConfig's doc comment: handing out a fresh source for an
// unchanged endpoint would tear down and rebuild every effect keyed on it.
let cachedConfig: ServerConfig | undefined;
let cachedSource: SessionSource | undefined;

/**
 * The source for the currently configured daemon, or undefined when unpaired.
 *
 * Deliberately unconsumed for now — the store exposes it via useSessionSource
 * but its existing polling/fetching/spawn paths still call the daemon
 * functions directly. Task 15 switches those readers over to this source.
 */
export function sessionSourceForConfig(cfg: ServerConfig | null): SessionSource | undefined {
	if (!cfg || !isConfigured(cfg)) {
		cachedConfig = undefined;
		cachedSource = undefined;
		return undefined;
	}
	if (cachedSource === undefined || !sameServerConfig(cfg, cachedConfig ?? null)) {
		cachedConfig = cfg;
		cachedSource = createLocalSessionSource(cfg);
	}
	return cachedSource;
}
