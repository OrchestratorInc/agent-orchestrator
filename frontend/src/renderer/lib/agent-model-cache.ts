import type { components } from "../../api/schema";
import { LOCAL_HOST } from "./hosts";

type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

function cachePrefix(agentId: string, hostId?: string): string {
	return `ao.agentModels.v1:${JSON.stringify([hostId || LOCAL_HOST, agentId])}:`;
}

function cacheKey(agentId: string, projectId: string, hostId?: string): string {
	return cachePrefix(agentId, hostId) + JSON.stringify(projectId);
}

/** A display snapshot only; the daemon DB remains the catalog authority. */
export function readCachedAgentModels(agentId: string, projectId: string, hostId?: string): AgentModelCatalog | undefined {
	try {
		const catalog = JSON.parse(window.localStorage.getItem(cacheKey(agentId, projectId, hostId)) ?? "null") as AgentModelCatalog | null;
		if (!catalog || catalog.agentId !== agentId || !Array.isArray(catalog.models)
			|| !catalog.models.every((model) => model && typeof model.id === "string" && typeof model.label === "string"
				&& (model.provider === undefined || typeof model.provider === "string")
				&& (model.efforts === undefined || (Array.isArray(model.efforts) && model.efforts.every((effort) => typeof effort === "string"))))
			|| typeof catalog.allowCustom !== "boolean" || typeof catalog.source !== "string"
			|| typeof catalog.fetchedAt !== "string" || typeof catalog.stale !== "boolean"
			|| !["catalog", "text", "mode"].includes(catalog.selectionMode)
			|| !["none", "direct", "configured"].includes(catalog.customModelEntry)) return undefined;
		// Refresh state belongs to the running daemon, not a previous UI visit.
		return { ...catalog, refreshRecommended: false, refreshState: "idle", refreshError: undefined, retryAt: undefined };
	} catch {
		return undefined;
	}
}

export function cacheAgentModels(catalog: AgentModelCatalog, agentId: string, projectId: string, hostId?: string): void {
	try {
		window.localStorage.setItem(cacheKey(agentId, projectId, hostId), JSON.stringify(catalog));
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
