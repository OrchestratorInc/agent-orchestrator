import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";
import type { CloudCpClient } from "./cloud-cp";

export type CueDTO = components["schemas"]["CueResponse"];
export type CueInput = components["schemas"]["CueDefinitionRequest"];
export type CueInvokeResult = components["schemas"]["InvokeCueResponse"];
/** A Cloud command cue comes back as its command, for the desktop to run in a new shell of the session. */
export type CloudCueInvokeResult = { kind: "agent" | "command"; sessionId: string; command?: string };

/** A Cloud project's cues live on the control plane; a local project's on the daemon. */
export type CueHost = { client: CloudCpClient; orgId: string };

export const CUE_LIMITS = {
	name: 64,
	command: 4096,
	prompt: 16384,
} as const;

export const projectCuesQueryKey = (projectId: string) => ["cues", projectId] as const;

export async function fetchProjectCues(projectId: string, cloud?: CueHost): Promise<CueDTO[]> {
	if (cloud) return (await cloud.client.listProjectCues(cloud.orgId, projectId)).cues;
	const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/cues", {
		params: { path: { projectId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not load cues"));
	return data?.cues ?? [];
}

export async function createCue(projectId: string, input: CueInput, cloud?: CueHost): Promise<CueDTO> {
	if (cloud) return (await cloud.client.createProjectCue(cloud.orgId, projectId, toCloudCue(input))).cue;
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/cues", {
		params: { path: { projectId } },
		body: input,
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not create cue"));
	return data.cue;
}

export async function updateCue(cueId: string, input: CueInput, cloud?: CueHost): Promise<CueDTO> {
	if (cloud) return (await cloud.client.updateProjectCue(cloud.orgId, cueId, toCloudCue(input))).cue;
	const { data, error } = await apiClient.PATCH("/api/v1/cues/{cueId}", {
		params: { path: { cueId } },
		body: input,
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not update cue"));
	return data.cue;
}

export async function deleteCue(cueId: string, cloud?: CueHost): Promise<void> {
	if (cloud) return cloud.client.deleteProjectCue(cloud.orgId, cueId);
	const { error } = await apiClient.DELETE("/api/v1/cues/{cueId}", {
		params: { path: { cueId } },
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not delete cue"));
}

export async function invokeCue(
	cueId: string,
	sessionId?: string,
	shell?: string,
): Promise<CueInvokeResult> {
	const body: components["schemas"]["InvokeCueRequest"] = {};
	if (sessionId !== undefined) body.sessionId = sessionId;
	if (shell !== undefined) body.shell = shell;
	const { data, error } = await apiClient.POST("/api/v1/cues/{cueId}/invoke", {
		params: { path: { cueId } },
		body,
	});
	if (error) throw new Error(apiErrorMessage(error, "Could not run cue"));
	return data;
}

/** Runs a Cloud cue into a session of its project. */
export function invokeCloudCue(cloud: CueHost, cueId: string, sessionId: string): Promise<CloudCueInvokeResult> {
	return cloud.client.invokeProjectCue(cloud.orgId, cueId, sessionId);
}

function toCloudCue(input: CueInput) {
	const type = input.type === "agent" ? "agent" as const : "command" as const;
	return { name: input.name, type, ...(type === "command" ? { command: input.command ?? "" } : { prompt: input.prompt ?? "" }) };
}
