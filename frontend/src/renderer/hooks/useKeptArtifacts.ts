import { useQuery } from "@tanstack/react-query";
import { apiClient, hasTrustedApiBaseUrl } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import type { SessionArtifact } from "../types/workspace";
import { toSessionArtifact } from "./useWorkspaceQuery";

export const keptArtifactsQueryKey = ["kept-artifacts"] as const;

/**
 * Artifacts no session or project lists: those of a session deleted with its
 * artifacts kept, and of a session in a removed project. They are view only.
 */
export type KeptArtifactSet = {
	sessionId: string;
	/** The session's project; undefined once it has none. */
	projectId?: string;
	title: string;
	/** Set when the session itself was deleted; otherwise its project was removed. */
	sessionDeleted: boolean;
	artifactFiles: SessionArtifact[];
};

async function fetchKeptArtifacts(): Promise<KeptArtifactSet[]> {
	const { data, error } = await apiClient.GET("/api/v1/kept-artifacts");
	if (error) throw error;
	return (data?.sets ?? []).map((set) => ({
		sessionId: set.sessionId,
		...(set.projectId ? { projectId: set.projectId } : {}),
		title: set.title || set.sessionId,
		sessionDeleted: set.sessionDeleted,
		artifactFiles: set.artifactFiles.map(toSessionArtifact),
	}));
}

/**
 * This computer's kept artifacts. A daemon too old to list them lists none;
 * the list is refreshed with the workspace list.
 */
export function useKeptArtifacts() {
	return useQuery({
		queryKey: keptArtifactsQueryKey,
		queryFn: fetchKeptArtifacts,
		enabled: !usesPreviewWorkspaceData && hasTrustedApiBaseUrl(),
		retry: false,
	});
}
