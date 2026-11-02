import {
	useIsMutating,
	useMutation,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type AwakeState = components["schemas"]["AwakeState"];
export type AwakeSettings = components["schemas"]["AwakeSettings"];
export type AwakeRule = components["schemas"]["AwakeRule"];
export type AwakeCondition = components["schemas"]["AwakeCondition"];
export type AwakeStart = components["schemas"]["StartAwakeRequest"];
export const awakeQueryKey = ["awake"] as const;

type Action =
	| { kind: "start"; request: Partial<AwakeStart> }
	| { kind: "end"; id: string }
	| { kind: "extend"; id: string; seconds: number }
	| { kind: "stop" | "resume" | "recover" | "clearHistory" | "authorizeLowPower" | "revokeLowPower" }
	| { kind: "settings"; settings: AwakeSettings }
	| { kind: "rules"; rules: AwakeRule[] };

export function useAwake(enabled = true, detailed = false) {
	const cache = useQueryClient();
	const query = useQuery<AwakeState>({
		queryKey: detailed ? [...awakeQueryKey, "observations"] : awakeQueryKey,
		enabled,
		queryFn: async () => {
			const { data, error } = detailed
				? await apiClient.GET("/api/v1/awake/observations")
				: await apiClient.GET("/api/v1/awake");
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Stay Awake status is unavailable");
			return data;
		},
		// Back off while the endpoint is failing (Stay Awake disabled at startup,
		// daemon restarting) so every poll is not reported as a fresh API error.
		refetchInterval: enabled ? (q) => (q.state.status === "error" ? 60_000 : 5_000) : false,
		retry: false,
	});
	const mutation = useMutation<AwakeState, Error, Action>({
		mutationKey: awakeQueryKey,
		mutationFn: async (action: Action) => {
			const result = await (async () => {
				switch (action.kind) {
					case "start":
						return apiClient.POST("/api/v1/awake/sessions", {
							body: {
								scope: "ao",
								sessionId: "",
								mode: "indefinite",
								durationSeconds: 0,
								boundId: "",
								lidClosed: true,
								...action.request,
							},
						});
					case "end":
						return apiClient.POST("/api/v1/awake/sessions/{awakeId}/end", {
							params: { path: { awakeId: action.id } },
						});
					case "extend":
						return apiClient.POST("/api/v1/awake/sessions/{awakeId}/extend", {
							params: { path: { awakeId: action.id } },
							body: { durationSeconds: action.seconds },
						});
					case "authorizeLowPower":
						return apiClient.POST("/api/v1/awake/low-power/authorize");
					case "revokeLowPower":
						return apiClient.POST("/api/v1/awake/low-power/revoke");
					case "stop":
						return apiClient.POST("/api/v1/awake/stop");
					case "resume":
						return apiClient.POST("/api/v1/awake/resume");
					case "recover":
						return apiClient.POST("/api/v1/awake/recover");
					case "clearHistory":
						return apiClient.DELETE("/api/v1/awake/history");
					case "settings":
						return apiClient.PUT("/api/v1/awake/settings", {
							body: action.settings,
						});
					case "rules":
						return apiClient.PUT("/api/v1/awake/rules", {
							body: { rules: action.rules },
						});
				}
			})();
			if (result.error) throw new Error(apiErrorMessage(result.error));
			if (!result.data) throw new Error("Stay Awake status is unavailable");
			return result.data;
		},
		onSuccess: (data) =>
			cache.setQueriesData({ queryKey: awakeQueryKey }, data),
		onSettled: () => {
			void cache.invalidateQueries({ queryKey: awakeQueryKey });
		},
	});
	const busy = useIsMutating({ mutationKey: awakeQueryKey }) > 0;
	return {
		state: query.data,
		loading: query.isLoading,
		queryError: query.error,
		error: mutation.error,
		busy,
		act: mutation.mutate,
		actAsync: mutation.mutateAsync,
	};
}

export function awakeCapability(state: AwakeState | undefined, id: string) {
	return state?.capabilities?.find((capability) => capability.id === id);
}
