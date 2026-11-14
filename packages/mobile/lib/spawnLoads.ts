import type { AgentModelInfo } from "./api";
import type { ServerConfig } from "./config";
import { sameServerConfig } from "./sameConfig";

// What the New task pickers keep between loads. They load once per machine,
// not once per connection (an app switch drops the connection and brings the
// same one back), but a connection that really changed is loaded through again.

/**
 * Counts the connections the pickers have loaded through. It moves on only
 * when a connection replaces one that differs (another endpoint, credential or
 * identity: an upgrade race moving to a better path, or a return on another
 * address). The connection going away and coming back the same leaves it.
 */
export type ConnectionEpoch = { last: ServerConfig | null; epoch: number };

export function nextConnectionEpoch(state: ConnectionEpoch, config: ServerConfig | null): ConnectionEpoch {
	if (!config || state.last === config) return state;
	if (!state.last || sameServerConfig(state.last, config)) return { last: config, epoch: state.epoch };
	return { last: config, epoch: state.epoch + 1 };
}

/**
 * Every agent's machine-wide models for one scope (a machine on one
 * connection). Only lists that loaded are kept: a failed agent is asked again
 * on the next load, and the ones that loaded are not.
 */
export type AgentModelCache = {
	/** Of `ids`, the agents with no list in `scope` yet. A new scope forgets the old one's lists. */
	missing(scope: string, ids: readonly string[]): string[];
	/** Keeps one agent's list, if `scope` is still the cache's. */
	record(scope: string, id: string, models: AgentModelInfo[]): void;
	/** The lists for `ids`, empty for an agent with none; the same object until one changes. */
	lists(ids: readonly string[]): Record<string, AgentModelInfo[]>;
};

export function createAgentModelCache(): AgentModelCache {
	let scope = "";
	const loaded = new Map<string, AgentModelInfo[]>();
	let shown: { key: string; value: Record<string, AgentModelInfo[]> } | undefined;
	return {
		missing(nextScope, ids) {
			if (nextScope !== scope) {
				scope = nextScope;
				loaded.clear();
				shown = undefined;
			}
			return ids.filter((id) => !loaded.has(id));
		},
		record(forScope, id, models) {
			if (forScope !== scope) return;
			loaded.set(id, models);
			shown = undefined;
		},
		lists(ids) {
			const key = ids.join("|");
			if (shown?.key !== key) shown = { key, value: Object.fromEntries(ids.map((id) => [id, loaded.get(id) ?? []])) };
			return shown.value;
		},
	};
}
