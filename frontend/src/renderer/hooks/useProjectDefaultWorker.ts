import { apiErrorException } from "../lib/api-error-exception";
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";

type WorkerTuning = { model?: string; mode?: string; effort?: string };

export function useProjectWorkerDefaults(projectId: string) {
	const query = useQuery({
		queryKey: ["project", projectId],
		enabled: Boolean(projectId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw apiErrorException(error);
			if (data?.status !== "ok") throw new Error("Project config unavailable");
			return data.project as { agent?: string; config?: { agentConfig?: WorkerTuning; worker?: { agent?: string; agentConfig?: WorkerTuning } } };
		},
	});
	return {
		loaded: query.status !== "pending",
		harness: query.data?.config?.worker?.agent || query.data?.agent || "",
		agentConfig: query.data?.config?.agentConfig,
		worker: query.data?.config?.worker,
	};
}

export function useProjectDefaultWorker(projectId: string): string {
	return useProjectWorkerDefaults(projectId).harness;
}
