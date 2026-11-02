import AsyncStorage from "@react-native-async-storage/async-storage";
import * as SecureStore from "expo-secure-store";
import { ApiError, unpairFromDaemon } from "./api";
import { configForEndpoint } from "./connect";
import { probeEndpoint } from "./connectRuntime";
import { findHost, type Host } from "./hosts";
import { raceEndpoints } from "./race";

// Only opaque job keys go in AsyncStorage. Each credential-bearing snapshot
// has its own SecureStore entry, avoiding a growing, size-limited secret blob.
const INDEX_KEY = "ao.pendingUnpairIds";
const KEY_PREFIX = "ao.pendingUnpair.";
type PendingUnpair = { key: string; host: Host; installId: string; removed: boolean; queuedAt?: number };

// A decommissioned machine never answers again. Bound how long and how many
// forgotten credentials the phone keeps trying, so cleanup cannot become a
// permanent background prober or an ever-growing keychain.
export const PENDING_UNPAIR_MAX_AGE_MS = 30 * 24 * 60 * 60_000;
export const MAX_PENDING_UNPAIRS = 20;
// Unreachable machines back off from the 30s retry tick up to this ceiling.
const RETRY_BASE_MS = 30_000;
const RETRY_MAX_MS = 30 * 60_000;
const backoff = new Map<string, { failures: number; nextAttemptAt: number }>();

let storageTail: Promise<unknown> = Promise.resolve();

function mutate<T>(action: () => Promise<T>): Promise<T> {
	const result = storageTail.then(action, action);
	storageTail = result.catch(() => {});
	return result;
}

async function readKeys(): Promise<string[]> {
	const raw = await AsyncStorage.getItem(INDEX_KEY);
	if (!raw) return [];
	try {
		const keys: unknown = JSON.parse(raw);
		return Array.isArray(keys) ? keys.filter((key): key is string => typeof key === "string" && key.startsWith(KEY_PREFIX)) : [];
	} catch {
		return [];
	}
}

function parseJob(key: string, raw: string): PendingUnpair | null {
	try {
		const job = JSON.parse(raw) as Partial<PendingUnpair>;
		if (job.key !== key || typeof job.installId !== "string" || !job.host?.id || !Array.isArray(job.host.endpoints)) return null;
		return job as PendingUnpair;
	} catch {
		return null;
	}
}

/** Unreadable keychain entries are kept for a later attempt; missing or
 * malformed ones are reported so one bad entry cannot stall every other job. */
async function readJobs(): Promise<{ jobs: PendingUnpair[]; invalid: string[] }> {
	const jobs: PendingUnpair[] = [];
	const invalid: string[] = [];
	await Promise.all((await readKeys()).map(async (key) => {
		const raw = await SecureStore.getItemAsync(key).catch(() => undefined);
		if (raw === undefined) return;
		const job = raw === null ? null : parseJob(key, raw);
		if (job) jobs.push(job);
		else invalid.push(key);
	}));
	return { jobs, invalid };
}

function queuedAt(job: PendingUnpair): number {
	if (typeof job.queuedAt === "number") return job.queuedAt;
	const stamp = Number(job.key.slice(KEY_PREFIX.length).split(".")[0]);
	return Number.isFinite(stamp) ? stamp : Date.now();
}

async function removeJobs(keys: readonly string[]): Promise<void> {
	if (keys.length === 0) return;
	for (const key of keys) backoff.delete(key);
	await mutate(async () => {
		const stored = await readKeys();
		for (const key of keys) await SecureStore.deleteItemAsync(key);
		const remaining = stored.filter((candidate) => !keys.includes(candidate));
		if (remaining.length) await AsyncStorage.setItem(INDEX_KEY, JSON.stringify(remaining));
		else await AsyncStorage.removeItem(INDEX_KEY);
	});
}

function deferRetry(key: string, now: number): void {
	const failures = (backoff.get(key)?.failures ?? 0) + 1;
	backoff.set(key, { failures, nextAttemptAt: now + Math.min(RETRY_BASE_MS * 2 ** (failures - 1), RETRY_MAX_MS) });
}

/** Persist first: losing this write must not throw away the only credential
 * with which the phone can revoke its remote notification registration. */
export async function queueMachineUnpair(host: Host, installId: string): Promise<void> {
	if (!host.id || !installId) throw new Error("Cannot safely queue machine unpairing without identity");
	await mutate(async () => {
		const keys = await readKeys();
		const now = Date.now();
		const key = `${KEY_PREFIX}${now}.${Math.random().toString(36).slice(2)}`;
		await SecureStore.setItemAsync(key, JSON.stringify({ key, host, installId, removed: false, queuedAt: now } satisfies PendingUnpair));
		// Keys sort by queue time; past the cap the oldest cleanup is abandoned.
		const kept = [...keys, key];
		const dropped = kept.splice(0, Math.max(0, kept.length - MAX_PENDING_UNPAIRS));
		try {
			await AsyncStorage.setItem(INDEX_KEY, JSON.stringify(kept));
		} catch (error) {
			await SecureStore.deleteItemAsync(key);
			throw error;
		}
		for (const old of dropped) {
			backoff.delete(old);
			await SecureStore.deleteItemAsync(old).catch(() => {});
		}
	});
}

/** A durable marker distinguishes a deliberate re-pair from the brief window
 * between queuing cleanup and finishing local removal. */
export async function markMachineUnpaired(hostId: string): Promise<void> {
	await mutate(async () => {
		for (const job of (await readJobs()).jobs) {
			if (job.host.id === hostId) await SecureStore.setItemAsync(job.key, JSON.stringify({ ...job, removed: true }));
		}
	});
}

/** Called through push's network mutation queue, so a re-pair registration
 * always runs after any already-started unpair, never before it. */
export async function flushMachineUnpairs(options: { ignoreBackoff?: boolean } = {}): Promise<void> {
	const { jobs, invalid } = await mutate(readJobs);
	await removeJobs(invalid).catch(() => {});
	await Promise.all(jobs.map(async (job) => {
		try {
			const paired = await findHost(job.host.id);
			if (paired) {
				if (job.removed) await removeJobs([job.key]); // Deliberately paired again.
				return;
			}
			const now = Date.now();
			if (now - queuedAt(job) > PENDING_UNPAIR_MAX_AGE_MS) {
				await removeJobs([job.key]);
				return;
			}
			if (!options.ignoreBackoff && now < (backoff.get(job.key)?.nextAttemptAt ?? 0)) return;
			const outcome = await raceEndpoints(job.host.endpoints, job.host.id,
				(endpoint, signal) => probeEndpoint(endpoint, signal, { retryTunnel: false }));
			if (!outcome.ok) {
				deferRetry(job.key, Date.now());
				return;
			}
			// Pairing may complete while identity probes are in flight. An old
			// cleanup must not delete that new device registration.
			if (await findHost(job.host.id)) {
				await removeJobs([job.key]);
				return;
			}
			await unpairFromDaemon(configForEndpoint(outcome.endpoint, job.host.token, job.host.id), job.installId);
			await removeJobs([job.key]);
		} catch (error) {
			// The verified machine rejected this credential: it was revoked or
			// rotated, so no later retry can succeed. Unknown pairings are already
			// a no-op on the daemon, so 404 never reaches here.
			if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
				await removeJobs([job.key]).catch(() => {});
				return;
			}
			// Keep the durable job on timeout, offline, server or storage failure.
			deferRetry(job.key, Date.now());
		}
	}));
}
