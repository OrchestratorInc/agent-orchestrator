import { forgetOnCloudAccountChange } from "./accountCaches";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import { cloudModelLabel } from "../../../shared/models/cloud-model-label";
import type { CloudChatModel, CloudChatModels } from "./types";

// Named as the desktop names them: one copy, in packages/shared.
export { cloudModelLabel };

/**
 * A Cloud catalog's models named by cloudModelLabel ("Opus 5.5", not Claude's
 * bare alias name "Opus"), for every picker that lists them.
 */
export function namedCloudModels(agent: string | undefined, models: readonly CloudChatModel[]): CloudChatModel[] {
	return models.map((model) => {
		const displayName = cloudModelLabel(agent ?? "", model);
		return displayName === model.displayName ? model : { ...model, displayName };
	});
}

// Where a cloud chat's model picker gets its models, as on the desktop
// (CloudSessionChatSurface): the session's worker is the authority, but it has
// to be awake and start the agent to answer, which takes seconds. Until it
// does, the picker shows the control plane's catalog for the agent (what a
// worker on the same CLI last reported), which needs no worker. Both are kept
// for this app run, so reopening a session shows its models at once while
// they are asked again.

const AGENT_CATALOG_FRESH_MS = 5 * 60_000;

/**
 * While Cloud only knows the CLI release's list (source "release") it is
 * learning the account's own from a running session, so the list is read
 * again until it arrives, as the desktop does (useCloudModelCatalog): after
 * this long, then twice as long each time it is still the release list, up to
 * MAX_UNVERIFIED_CATALOG_REFRESH_MS. A release list is also fresh only this
 * long, so a picker opened later asks again rather than showing it from memory.
 */
export const UNVERIFIED_CATALOG_REFRESH_MS = 15_000;
export const MAX_UNVERIFIED_CATALOG_REFRESH_MS = 5 * 60_000;

/** Whether a catalog is still the release list, not the account's own. */
export function isReleaseCatalog(catalog: Pick<CloudChatModels, "source"> | undefined): boolean {
	return catalog?.source === "release";
}

/** How long until a release list is read again, after `reads` reads that each gave one. */
export function releaseCatalogRefreshMs(reads: number): number {
	return Math.min(UNVERIFIED_CATALOG_REFRESH_MS * 2 ** Math.max(0, reads - 1), MAX_UNVERIFIED_CATALOG_REFRESH_MS);
}

const sessionCatalogs = new Map<string, CloudChatModels>();
const agentCatalogs = new Map<string, { at: number; catalog: CloudChatModels }>();
const agentRequests = new Map<string, Promise<CloudChatModels>>();
// Another account's lists are not this one's: forgotten on sign-out and an
// account switch, and an answer to a request made before that is dropped.
let accountGeneration = 0;
forgetOnCloudAccountChange(() => {
	accountGeneration += 1;
	sessionCatalogs.clear();
	agentCatalogs.clear();
	agentRequests.clear();
});

/** A worker's answer belongs to that worker: a restart can bring another CLI. */
export function sessionCatalogKey(orgId: string, sessionId: string, workerEpoch: number | undefined): string {
	return `${orgId}/${sessionId}/${workerEpoch ?? 0}`;
}

export function cachedSessionCatalog(key: string): CloudChatModels | undefined {
	return sessionCatalogs.get(key);
}

/** The account the lists in memory belong to, taken when a read starts and given back to rememberSessionCatalog. */
export function catalogAccount(): number {
	return accountGeneration;
}

/** Keeps a worker's answer, unless the account changed after `askedFor` (catalogAccount when it was asked). */
export function rememberSessionCatalog(key: string, catalog: CloudChatModels, askedFor = accountGeneration): void {
	if (askedFor !== accountGeneration) return;
	sessionCatalogs.set(key, catalog);
}

export function cachedAgentCatalog(orgId: string, agent: string, now = Date.now()): CloudChatModels | undefined {
	const entry = agentCatalogs.get(`${orgId}/${agent}`);
	const freshFor = isReleaseCatalog(entry?.catalog) ? UNVERIFIED_CATALOG_REFRESH_MS : AGENT_CATALOG_FRESH_MS;
	return entry && now - entry.at < freshFor ? entry.catalog : undefined;
}

/**
 * The control plane's catalog for an agent, shared by the spawn sheet and an
 * open session: answered from memory while fresh, and one request at a time.
 * `rereadRelease` asks again when what memory holds is still the release list
 * (a scheduled re-read); the account's own list is still answered from memory.
 */
export function agentModelCatalog(client: Pick<CloudClient, "listAgentModels">, orgId: string, agent: string, options: { rereadRelease?: boolean } = {}): Promise<CloudChatModels> {
	const key = `${orgId}/${agent}`;
	const known = cachedAgentCatalog(orgId, agent);
	const cached = options.rereadRelease && isReleaseCatalog(known) ? undefined : known;
	if (cached) return Promise.resolve(cached);
	const pending = agentRequests.get(key);
	if (pending) return pending;
	const generation = accountGeneration;
	const request = client.listAgentModels(orgId, agent)
		.then((catalog) => {
			if (generation === accountGeneration) agentCatalogs.set(key, { at: Date.now(), catalog });
			return catalog;
		})
		.finally(() => { if (agentRequests.get(key) === request) agentRequests.delete(key); });
	agentRequests.set(key, request);
	return request;
}

const MODELS_FAILED_RETRIES = 6;
const TIMEOUT_RETRIES = 3;
const AGENT_SETUP_RETRIES = 36;
const AGENT_SETUP_RETRY_DELAY = 5_000;
/** A worker still starting is asked again after its Retry-After, or this. */
const STARTING_RETRY_DELAY = 5_000;
const MIN_STARTING_RETRY_DELAY = 1_000;
const MAX_STARTING_RETRY_DELAY = 5 * 60_000;
/** How long a worker that keeps answering it is still starting is waited for, as on the desktop. */
export const MAX_STARTING_WAIT_MS = 30 * 60_000;

function code(error: unknown): string | undefined {
	return error instanceof CloudError ? error.code : undefined;
}

/**
 * How long to wait before asking the worker again after `failures` failed
 * reads, or undefined to stop. The desktop's schedule: a worker still
 * connecting is asked again quickly (1s, 2s, 4s… up to 30s); one still
 * installing the agent every 5s for up to three minutes; a failed operation a
 * few times with backoff; a timeout three times. A worker still preparing its
 * workspace (WORKER_STARTING) is asked again after its Retry-After (5s when it
 * sends none) until `startingForMs`, how long it has answered that, reaches
 * half an hour.
 */
export function modelsRetryDelay(failures: number, error: unknown, startingForMs = 0): number | undefined {
	switch (code(error)) {
		case "WORKER_STARTING": {
			if (startingForMs >= MAX_STARTING_WAIT_MS) return undefined;
			const retryAfter = error instanceof CloudError ? error.retryAfterMs : undefined;
			return retryAfter !== undefined && Number.isFinite(retryAfter)
				? Math.min(Math.max(retryAfter, MIN_STARTING_RETRY_DELAY), MAX_STARTING_RETRY_DELAY)
				: STARTING_RETRY_DELAY;
		}
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

/**
 * Reads a session worker's catalog until it answers, waiting between reads as
 * modelsRetryDelay says. Resolves undefined once that says to stop, or at once
 * when `signal` aborts, even in the middle of a wait.
 */
export async function readSessionCatalog<T>(read: () => Promise<T>, signal: AbortSignal, now: () => number = Date.now): Promise<T | undefined> {
	// When the worker first answered that it is still starting, for its 30-minute budget.
	let startingSince: number | undefined;
	for (let failures = 0; !signal.aborted;) {
		try {
			const answer = await read();
			return signal.aborted ? undefined : answer;
		} catch (cause) {
			failures += 1;
			if (code(cause) === "WORKER_STARTING") startingSince ??= now();
			const delay = modelsRetryDelay(failures, cause, startingSince === undefined ? 0 : now() - startingSince);
			if (delay === undefined || signal.aborted) return undefined;
			await new Promise<void>((resolve) => {
				const done = () => {
					clearTimeout(timer);
					signal.removeEventListener("abort", done);
					resolve();
				};
				const timer = setTimeout(done, delay);
				signal.addEventListener("abort", done);
			});
		}
	}
	return undefined;
}
