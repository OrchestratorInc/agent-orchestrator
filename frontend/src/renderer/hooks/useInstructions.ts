import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type Instructions = components["schemas"]["InstructionsResponse"];

/** A null projectId selects the user's own instructions. */
export const instructionsQueryKey = (projectId: string | null) => ["instructions", projectId ?? ""] as const;

async function fetchInstructions(projectId: string | null): Promise<Instructions> {
	const { data, error } = projectId
		? await apiClient.GET("/api/v1/projects/{projectId}/instructions", { params: { path: { projectId } } })
		: await apiClient.GET("/api/v1/instructions");
	if (error) throw new Error(apiErrorMessage(error));
	if (!data) throw new Error("Instructions response was empty");
	return data.instructions;
}

/** Polls so a dream's edit shows up while the page is open. */
export function useInstructions(projectId: string | null) {
	return useQuery({
		queryKey: instructionsQueryKey(projectId),
		queryFn: () => fetchInstructions(projectId),
		refetchInterval: 15_000,
	});
}

export function useSaveInstructions(projectId: string | null) {
	const client = useQueryClient();
	return useMutation({
		mutationFn: async (text: string) => {
			const { data, error } = projectId
				? await apiClient.PUT("/api/v1/projects/{projectId}/instructions", { params: { path: { projectId } }, body: { body: text } })
				: await apiClient.PUT("/api/v1/instructions", { body: { body: text } });
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Instructions response was empty");
			return data.instructions;
		},
		onSuccess: (instructions) => client.setQueryData(instructionsQueryKey(projectId), instructions),
	});
}
