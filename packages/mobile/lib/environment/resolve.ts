import type { CloudClient } from "@aoagents/cloud-client";
import { isConfigured, type ServerConfig } from "../config";
import { createCloudSessionSource } from "../cloud/source";
import { sameServerConfig } from "../sameConfig";
import { createLocalSessionSource } from "./local";
import type { EnvironmentKind, SessionSource } from "./types";

/** What's known about the cloud environment when resolving a session source. */
export type CloudResolveInput = {
	client: CloudClient;
	signedIn: boolean;
	// The account's resolved org, or null while sign-in/org resolution is
	// still in flight.
	orgId: string | null;
};

// Memoised on (environment, config identity, org id) so the store hands the
// same instance to every consumer between changes. See sameServerConfig's doc
// comment: handing out a fresh source for an unchanged endpoint would tear
// down and rebuild every effect keyed on it. sameServerConfig also compares
// the password, so a rotated credential still rebuilds the local source.
let cachedEnvironment: EnvironmentKind | undefined;
let cachedConfig: ServerConfig | undefined;
let cachedOrgId: string | null | undefined;
let cachedSource: SessionSource | undefined;

function resetCache(): void {
	cachedEnvironment = undefined;
	cachedConfig = undefined;
	cachedOrgId = undefined;
	cachedSource = undefined;
}

/**
 * The source for the active environment, or undefined when it isn't ready
 * (no daemon paired for local; not signed in or no org resolved yet for
 * cloud).
 *
 * Deliberately unconsumed for now — the store exposes it via useSessionSource
 * but its existing polling/fetching/spawn paths still call the daemon
 * functions directly. Nothing calls through the SessionSource interface in
 * production yet.
 */
export function resolveSessionSource(input: {
	// `null` means the persisted choice hasn't loaded yet — see
	// lib/environment/store.tsx's loadEnvironment and lib/store.tsx's
	// AppProvider. It must not be treated as "local": that would render the
	// local empty state for a returning cloud user for a frame on every cold
	// start, which is the bug this parameter exists to prevent.
	environment: EnvironmentKind | null;
	cfg: ServerConfig | null;
	cloud?: CloudResolveInput;
}): SessionSource | undefined {
	const { environment, cfg, cloud } = input;

	if (environment === null) {
		resetCache();
		return undefined;
	}

	if (environment === "local") {
		if (!cfg || !isConfigured(cfg)) {
			resetCache();
			return undefined;
		}
		if (
			cachedSource === undefined ||
			cachedEnvironment !== "local" ||
			!sameServerConfig(cfg, cachedConfig ?? null)
		) {
			cachedEnvironment = "local";
			cachedConfig = cfg;
			cachedOrgId = undefined;
			cachedSource = createLocalSessionSource(cfg);
		}
		return cachedSource;
	}

	// environment === "cloud"
	if (!cloud || !cloud.signedIn || !cloud.orgId) {
		resetCache();
		return undefined;
	}
	if (
		cachedSource === undefined ||
		cachedEnvironment !== "cloud" ||
		cachedOrgId !== cloud.orgId
	) {
		cachedEnvironment = "cloud";
		cachedConfig = undefined;
		cachedOrgId = cloud.orgId;
		cachedSource = createCloudSessionSource({ client: cloud.client, orgId: cloud.orgId });
	}
	return cachedSource;
}
