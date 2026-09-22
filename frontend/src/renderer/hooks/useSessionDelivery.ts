import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { workspaceQueryKey } from "./useWorkspaceQuery";
import { sessionScmSummaryQueryKey } from "./useSessionScmSummary";
import { sessionWorkspaceFilesQueryKey } from "./useSessionWorkspaceFiles";

export type DeliveryAction = components["schemas"]["AdvanceDeliveryRequest"]["action"];

export function useSessionDelivery(sessionId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (input: components["schemas"]["AdvanceDeliveryRequest"]) => {
			void captureRendererEvent("ao.renderer.session_delivery_requested", { action: input.action });
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/delivery", { params: { path: { sessionId } }, body: input });
			if (error || !data) throw new Error(apiErrorMessage(error, "Unable to advance delivery"));
			return data;
		},
		onSuccess: (data, input) => {
			void captureRendererEvent("ao.renderer.session_delivery_succeeded", { action: input.action });
			queryClient.setQueryData(sessionWorkspaceFilesQueryKey(sessionId), (current: unknown) => current && typeof current === "object" ? { ...current, delivery: data.delivery } : current);
		},
		onError: (_error, input) => {
			void captureRendererEvent("ao.renderer.session_delivery_failed", { action: input.action });
		},
		onSettled: async () => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: sessionWorkspaceFilesQueryKey(sessionId) }),
				queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId) }),
				queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
			]);
		},
	});
}
