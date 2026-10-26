import { useQuery } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type WorkerTuning = { model?: string; mode?: string; effort?: string };

// Resolves the worker harness a spawn would actually use for one project: the
// project's worker setup first, then the daemon-wide default. This mirrors the
// resolution the New Task composer performs, so form pickers can preselect the
// real agent instead of parking on a "project default" label. The query key
// matches the composer's project query so both share one cache entry.
export function useProjectWorkerDefaults(projectId: string) {
	const query = useQuery({
		queryKey: ["project", projectId],
		enabled: Boolean(projectId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error("Project config unavailable");
			return data.project as { agent?: string; config?: { agentConfig?: WorkerTuning; worker?: { agent?: string; agentConfig?: WorkerTuning } } };
		},
	});
	return {
		// A failed lookup is settled too; unrelated automation edits must remain usable.
		loaded: query.status !== "pending",
		harness: query.data?.config?.worker?.agent || query.data?.agent || "",
		agentConfig: query.data?.config?.agentConfig,
		worker: query.data?.config?.worker,
	};
}

export function useProjectDefaultWorker(projectId: string): string {
	return useProjectWorkerDefaults(projectId).harness;
}
