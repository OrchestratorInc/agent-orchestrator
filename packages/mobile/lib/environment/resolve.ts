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
	/** Increments whenever the authenticated Cloud account session changes. */
	sessionEpoch?: number;
};

// Separate slots keep one source's resolution from evicting the other. The
// authenticated Cloud slot is never reused across a session epoch.
let cachedLocalConfig: ServerConfig | undefined;
let cachedLocalSource: SessionSource | undefined;
let cachedOrgId: string | undefined;
let cachedCloudClient: CloudClient | undefined;
let cachedCloudSessionEpoch: number | undefined;
let cachedCloudSource: SessionSource | undefined;

export function resolveLocalSource(cfg: ServerConfig | null): SessionSource | undefined {
	if (!cfg || !isConfigured(cfg)) {
		cachedLocalConfig = undefined;
		cachedLocalSource = undefined;
		return undefined;
	}
	if (!cachedLocalSource || !sameServerConfig(cfg, cachedLocalConfig ?? null)) {
		cachedLocalConfig = cfg;
		cachedLocalSource = createLocalSessionSource(cfg);
	}
	return cachedLocalSource;
}

export function resolveCloudSource(cloud: CloudResolveInput | undefined): SessionSource | undefined {
	if (!cloud || !cloud.signedIn || !cloud.orgId) {
		cachedOrgId = undefined;
		cachedCloudClient = undefined;
		cachedCloudSessionEpoch = undefined;
		cachedCloudSource = undefined;
		return undefined;
	}
	if (!cachedCloudSource || cachedOrgId !== cloud.orgId || cachedCloudClient !== cloud.client ||
		cachedCloudSessionEpoch !== (cloud.sessionEpoch ?? 0)) {
		cachedOrgId = cloud.orgId;
		cachedCloudClient = cloud.client;
		cachedCloudSessionEpoch = cloud.sessionEpoch ?? 0;
		cachedCloudSource = createCloudSessionSource({ client: cloud.client, orgId: cloud.orgId });
	}
	return cachedCloudSource;
}

/**
 * Compatibility selector for the previously active environment, or undefined when it isn't ready
 * (no daemon paired for local; not signed in or no org resolved yet for
 * cloud).
 *
 * Combined screens resolve Local and Cloud independently through the functions
 * above; older screens still use this selected-source adapter during migration.
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

	if (environment === "local") return resolveLocalSource(cfg);
	if (environment === "cloud") return resolveCloudSource(cloud);
	return undefined;
}
