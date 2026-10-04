import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

export type SpawnCouncilRequest = components["schemas"]["SpawnCouncilRequest"];
export type SpawnCouncilResponse = components["schemas"]["SpawnCouncilResponse"];
export type SpawnCouncilMember = components["schemas"]["SpawnCouncilMember"];
export type SpawnCouncilMemberResult = components["schemas"]["SpawnCouncilMemberResult"];

/**
 * useSpawnCouncil fans one brief out to several harnesses/models at once via
 * POST /api/v1/sessions/council. Every member becomes its own worker session,
 * and the members share a council group id so the board can show them as one
 * cohort for side-by-side comparison. The new sessions are refreshed onto the
 * board on success.
 */
export function useSpawnCouncil(hostId?: string) {
	const queryClient = useQueryClient();
	return useMutation<SpawnCouncilResponse, Error, SpawnCouncilRequest>({
		mutationKey: ["spawn-council", hostId ?? ""],
		mutationFn: async (body) => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST(
				"/api/v1/sessions/council",
				{ body },
			);
			if (error) throw new Error(apiErrorMessage(error, "Could not start the council"));
			if (!data) throw new Error("The daemon returned no council response");
			return data;
		},
		onSuccess: async () => {
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
		},
	});
}
