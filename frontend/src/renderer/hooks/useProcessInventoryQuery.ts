import { useQuery, type Query, type UseQueryResult } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import type { MessageKey } from "../i18n";
import { apiClient } from "../lib/api-client";

export type ProcessInventory = components["schemas"]["ProcessInventoryResponse"];
export type ProcessKillTarget = components["schemas"]["ProcessKillTarget"];
export type HostMemory = NonNullable<ProcessInventory["host"]>;
export type ProcessTreeRow = ProcessInventory["trees"][number];

export const processInventoryQueryKey = ["process-inventory"] as const;

// Null on failure: the status bar degrades to nothing when the daemon is
// headless, older than this surface, or unreachable.
async function fetchProcessInventory(): Promise<ProcessInventory | null> {
	const { data } = await apiClient.GET("/api/v1/system/processes");
	return data ?? null;
}

// Shared so the status bar and tests read one cache entry. Polls quietly at
// 15s while the footprint is healthy and drops to 5s once orphaned trees
// exist, so a kill's aftermath lands on the next tick. A disabled surface
// (AO_NO_PROCINVENTORY) stops the polling entirely.
export const processInventoryQueryOptions = {
	queryKey: processInventoryQueryKey,
	queryFn: fetchProcessInventory,
	retry: 1,
	staleTime: 10_000,
	refetchInterval: (query: Query<ProcessInventory | null>) => {
		if (query.state.data?.disabled) return false;
		return (query.state.data?.totals.orphansCount ?? 0) > 0 ? 5_000 : 15_000;
	},
};

export function useProcessInventoryQuery(): UseQueryResult<ProcessInventory | null, Error> {
	return useQuery(processInventoryQueryOptions);
}

// Owned trees the batch stop controls may stop: idle or exited only.
// waiting_input and blocked mean the session stopped on the user (a question
// or an open permission dialog) — stopping those would mute a session that is
// waiting for the human, so they are never stoppable. One helper because the
// predicate is shared by the status bar and both MemoryPopover stop buttons.
export function isStoppableOwnedTree(tree: ProcessTreeRow): boolean {
	return tree.state === "owned" && (tree.activityState === "idle" || tree.activityState === "exited");
}

// I18n key for a tree's tracked activity state (statusBar.activity* keys);
// empty string when the state is unknown.
export function activityStateLabelKey(activityState: string | undefined): MessageKey | "" {
	switch (activityState) {
		case "active":
			return "statusBar.activityActive";
		case "idle":
			return "statusBar.activityIdle";
		case "exited":
			return "statusBar.activityExited";
		case "waiting_input":
			return "statusBar.activityWaitingInput";
		case "blocked":
			return "statusBar.activityBlocked";
		default:
			return "";
	}
}
