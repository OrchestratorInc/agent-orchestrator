import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import type { CloudChatModels } from "./types";

// Where a cloud chat's model picker gets its models, as on the desktop
// (CloudSessionChatSurface): the session's worker is the authority, but it has
// to be awake and start the agent to answer, which takes seconds. Until it
// does, the picker shows the control plane's catalog for the agent (what a
// worker on the same CLI last reported), which needs no worker. Both are kept
// for this app run, so reopening a session shows its models at once while
// they are asked again.

const AGENT_CATALOG_FRESH_MS = 5 * 60_000;

const sessionCatalogs = new Map<string, CloudChatModels>();
const agentCatalogs = new Map<string, { at: number; catalog: CloudChatModels }>();
const agentRequests = new Map<string, Promise<CloudChatModels>>();

/** A worker's answer belongs to that worker: a restart can bring another CLI. */
export function sessionCatalogKey(orgId: string, sessionId: string, workerEpoch: number | undefined): string {
	return `${orgId}/${sessionId}/${workerEpoch ?? 0}`;
}

export function cachedSessionCatalog(key: string): CloudChatModels | undefined {
	return sessionCatalogs.get(key);
}

export function rememberSessionCatalog(key: string, catalog: CloudChatModels): void {
	sessionCatalogs.set(key, catalog);
}

export function cachedAgentCatalog(orgId: string, agent: string, now = Date.now()): CloudChatModels | undefined {
	const entry = agentCatalogs.get(`${orgId}/${agent}`);
	return entry && now - entry.at < AGENT_CATALOG_FRESH_MS ? entry.catalog : undefined;
}

/**
 * The control plane's catalog for an agent, shared by the spawn sheet and an
 * open session: answered from memory while fresh, and one request at a time.
 */
export function agentModelCatalog(client: Pick<CloudClient, "listAgentModels">, orgId: string, agent: string): Promise<CloudChatModels> {
	const key = `${orgId}/${agent}`;
	const cached = cachedAgentCatalog(orgId, agent);
	if (cached) return Promise.resolve(cached);
	const pending = agentRequests.get(key);
	if (pending) return pending;
	const request = client.listAgentModels(orgId, agent)
		.then((catalog) => {
			agentCatalogs.set(key, { at: Date.now(), catalog });
			return catalog;
		})
		.finally(() => agentRequests.delete(key));
	agentRequests.set(key, request);
	return request;
}

const MODELS_FAILED_RETRIES = 6;
const TIMEOUT_RETRIES = 3;
const AGENT_SETUP_RETRIES = 36;
const AGENT_SETUP_RETRY_DELAY = 5_000;

function code(error: unknown): string | undefined {
	return error instanceof CloudError ? error.code : undefined;
}

/**
 * How long to wait before asking the worker again after `failures` failed
 * reads, or undefined to stop. The desktop's schedule: a worker still
 * connecting is asked again quickly (1s, 2s, 4s… up to 30s); one still
 * installing the agent every 5s for up to three minutes; a failed operation a
 * few times with backoff; a timeout three times.
 */
export function modelsRetryDelay(failures: number, error: unknown): number | undefined {
	switch (code(error)) {
		case "WORKER_UNAVAILABLE":
			return Math.min(1_000 * 2 ** (failures - 1), 30_000);
		case "TRANSPORT_TIMEOUT":
		case "WORKER_TIMEOUT":
			return failures < TIMEOUT_RETRIES ? Math.min(1_000 * 2 ** (failures - 1), 30_000) : undefined;
		case "WORKER_OPERATION_FAILED":
			return failures < MODELS_FAILED_RETRIES ? Math.min(2_000 * 2 ** (failures - 1), 15_000) : undefined;
		case "CHAT_AGENT_INSTALLING":
		case "CHAT_ADAPTER_INSTALLING":
			return failures < AGENT_SETUP_RETRIES ? AGENT_SETUP_RETRY_DELAY : undefined;
		default:
			return undefined;
	}
}
