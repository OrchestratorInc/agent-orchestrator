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

// Memoised on environment identity. Cloud identity includes the client, org,
// and account session epoch; retaining a source across any of those boundaries
// could let an old account's request publish into the new board.
let cachedEnvironment: EnvironmentKind | undefined;
let cachedConfig: ServerConfig | undefined;
let cachedOrgId: string | null | undefined;
let cachedCloudClient: CloudClient | undefined;
let cachedCloudSessionEpoch: number | undefined;
let cachedSource: SessionSource | undefined;

function resetCache(): void {
	cachedEnvironment = undefined;
	cachedConfig = undefined;
	cachedOrgId = undefined;
	cachedCloudClient = undefined;
	cachedCloudSessionEpoch = undefined;
	cachedSource = undefined;
}

/**
 * The source for the active environment, or undefined when it isn't ready
 * (no daemon paired for local; not signed in or no org resolved yet for
 * cloud).
 *
 * The store uses this source for the Cloud board while daemon mutations retain
 * their existing Local-only paths.
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
			cachedCloudClient = undefined;
			cachedCloudSessionEpoch = undefined;
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
		cachedOrgId !== cloud.orgId ||
		cachedCloudClient !== cloud.client ||
		cachedCloudSessionEpoch !== (cloud.sessionEpoch ?? 0)
	) {
		cachedEnvironment = "cloud";
		cachedConfig = undefined;
		cachedOrgId = cloud.orgId;
		cachedCloudClient = cloud.client;
		cachedCloudSessionEpoch = cloud.sessionEpoch ?? 0;
		cachedSource = createCloudSessionSource({ client: cloud.client, orgId: cloud.orgId });
	}
	return cachedSource;
}
