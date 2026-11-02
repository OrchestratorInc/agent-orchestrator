import type { components } from "../../api/schema";
import { LOCAL_HOST } from "./hosts";

type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const SNAPSHOT_PREFIX = "ao.agentModels.v1:";
const MAX_SNAPSHOTS = 10;
const MAX_SNAPSHOT_BYTES = 1024 * 1024;

function cachePrefix(agentId: string, hostId?: string): string {
	return `${SNAPSHOT_PREFIX}${JSON.stringify([hostId || LOCAL_HOST, agentId])}:`;
}

function cacheKey(agentId: string, scope: string, hostId?: string): string {
	return cachePrefix(agentId, hostId) + JSON.stringify(scope);
}

/** Refresh catalog facts without dropping recency while history is pending. */
export function withCachedModelUsage(catalog: AgentModelCatalog, cached: AgentModelCatalog | undefined): AgentModelCatalog {
	if (!cached) return catalog;
	const usage = new Map(cached.models.map((model) => [model.id, model.lastUsedAt]));
	return {
		...catalog,
		models: catalog.models.map((model) => {
			const lastUsedAt = model.lastUsedAt ?? usage.get(model.id);
			return lastUsedAt ? { ...model, lastUsedAt } : model;
		}),
	};
}

/** A display snapshot only; the daemon DB remains the catalog authority. */
export function readCachedAgentModels(agentId: string, scope: string, hostId?: string): AgentModelCatalog | undefined {
	try {
		const catalog = JSON.parse(window.localStorage.getItem(cacheKey(agentId, scope, hostId)) ?? "null") as (AgentModelCatalog & { snapshotWrittenAt?: number }) | null;
		if (!catalog || catalog.agentId !== agentId || !Array.isArray(catalog.models)
			|| !catalog.models.every((model) => model && typeof model.id === "string" && typeof model.label === "string"
				&& (model.provider === undefined || typeof model.provider === "string")
				&& (model.efforts === undefined || (Array.isArray(model.efforts) && model.efforts.every((effort) => typeof effort === "string"))))
			|| typeof catalog.allowCustom !== "boolean" || typeof catalog.source !== "string"
			|| typeof catalog.fetchedAt !== "string" || typeof catalog.stale !== "boolean"
			|| !["catalog", "text", "mode"].includes(catalog.selectionMode)
			|| !["none", "direct", "configured"].includes(catalog.customModelEntry)) return undefined;
		delete catalog.snapshotWrittenAt;
		// Refresh state belongs to the running daemon, not a previous UI visit.
		return { ...catalog, refreshRecommended: false, refreshState: "idle", refreshError: undefined, retryAt: undefined };
	} catch {
		return undefined;
	}
}

export function cacheAgentModels(catalog: AgentModelCatalog, agentId: string, scope: string, hostId?: string): void {
	try {
		const key = cacheKey(agentId, scope, hostId);
		const snapshots: { key: string; bytes: number; writtenAt: number }[] = [];
		for (let index = 0; index < window.localStorage.length; index++) {
			const savedKey = window.localStorage.key(index);
			if (!savedKey?.startsWith(SNAPSHOT_PREFIX) || savedKey === key) continue;
			const value = window.localStorage.getItem(savedKey) ?? "";
			let writtenAt = 0; // Legacy or corrupt snapshots are evicted first.
			try {
				const timestamp = JSON.parse(value)?.snapshotWrittenAt;
				if (typeof timestamp === "number" && Number.isFinite(timestamp)) writtenAt = timestamp;
			} catch { /* A corrupt snapshot must not prevent pruning the cache. */ }
			snapshots.push({ key: savedKey, bytes: (savedKey.length + value.length) * 2, writtenAt });
		}
		const snapshotWrittenAt = Math.max(Date.now(), ...snapshots.map((snapshot) => snapshot.writtenAt + 1));
		const value = JSON.stringify({ ...catalog, snapshotWrittenAt });
		const bytes = (key.length + value.length) * 2;
		if (bytes > MAX_SNAPSHOT_BYTES) {
			window.localStorage.removeItem(key);
			return;
		}
		snapshots.sort((a, b) => a.writtenAt - b.writtenAt);
		let totalBytes = snapshots.reduce((total, snapshot) => total + snapshot.bytes, bytes);
		while (snapshots.length >= MAX_SNAPSHOTS || totalBytes > MAX_SNAPSHOT_BYTES) {
			const oldest = snapshots.shift()!;
			window.localStorage.removeItem(oldest.key);
			totalBytes -= oldest.bytes;
		}
		window.localStorage.setItem(key, value);
	} catch {
		// Blocked or full browser storage must not interrupt the DB read.
	}
}

export function clearCachedAgentModels(agentId: string, hostId?: string): void {
	try {
		const prefix = cachePrefix(agentId, hostId);
		for (let index = window.localStorage.length - 1; index >= 0; index--) {
			const key = window.localStorage.key(index);
			if (key?.startsWith(prefix)) window.localStorage.removeItem(key);
		}
	} catch {
		// The in-memory cache is invalidated independently of browser storage.
	}
}
