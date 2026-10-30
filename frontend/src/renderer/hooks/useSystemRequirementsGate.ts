import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";

export type SystemRequirement = components["schemas"]["SystemRequirement"];

export const systemRequirementsQueryKey = ["system-requirements"] as const;

async function fetchSystemRequirements(): Promise<components["schemas"]["SystemRequirementsResponse"]> {
	const { data, error } = await apiClient.GET("/api/v1/system/requirements");
	if (error || !data) throw new Error("Could not check local requirements.");
	return data;
}

async function fetchGitHubAuthRequirement(): Promise<components["schemas"]["SystemRequirement"]> {
	const { data, error } = await apiClient.GET("/api/v1/system/github-auth");
	if (error || !data) throw new Error("Could not check GitHub authentication.");
	return data;
}

export const systemRequirementsQueryOptions = {
	queryKey: systemRequirementsQueryKey,
	queryFn: fetchSystemRequirements,
	refetchOnWindowFocus: false,
	// The preview build (VITE_NO_ELECTRON) has no real daemon behind it, so
	// there is nothing to probe — mirrors isDaemonReady's short-circuit for
	// the same flag in SessionsBoard.
	enabled: !usesPreviewWorkspaceData,
};

export const githubAuthRequirementQueryOptions = {
	queryKey: ["github-auth-requirement"] as const,
	queryFn: fetchGitHubAuthRequirement,
	refetchOnWindowFocus: false,
	enabled: !usesPreviewWorkspaceData,
};

/** GitHub credential probe, kept separate from the startup gate because
 * credential-store access can be slow on some machines. */
export function useGitHubAuthRequirement(enabled = true) {
	return useQuery({ ...githubAuthRequirementQueryOptions, enabled: githubAuthRequirementQueryOptions.enabled && enabled });
}

export type GitHubLogin = components["schemas"]["GitHubLoginResponse"];

const githubLoginQueryKey = ["github-login"] as const;
const GITHUB_LOGIN_POLL_INTERVAL_MS = 2_000;

/** GitHub device-flow sign-in owned by the daemon. The daemon polls GitHub and
 * stores the token; the renderer only shows the code and polls the state. */
export function useGitHubDeviceLogin() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: githubLoginQueryKey,
		queryFn: async (): Promise<GitHubLogin> => {
			const { data, error } = await apiClient.GET("/api/v1/github/login");
			if (error || !data) throw new Error("Could not check GitHub sign-in.");
			return data;
		},
		enabled: !usesPreviewWorkspaceData,
		refetchOnWindowFocus: false,
		refetchInterval: (current) => (current.state.data?.state === "pending" ? GITHUB_LOGIN_POLL_INTERVAL_MS : false),
	});
	const start = useMutation({
		mutationFn: async (): Promise<GitHubLogin> => {
			const { data, error } = await apiClient.POST("/api/v1/github/login");
			if (error || !data) throw new Error(apiErrorMessage(error, "Could not start GitHub sign-in."));
			return data;
		},
		onSuccess: (login) => queryClient.setQueryData(githubLoginQueryKey, login),
	});
	const cancel = useMutation({
		mutationFn: async (): Promise<GitHubLogin> => {
			const { data, error } = await apiClient.DELETE("/api/v1/github/login");
			if (error || !data) throw new Error("Could not cancel GitHub sign-in.");
			return data;
		},
		onSuccess: (login) => queryClient.setQueryData(githubLoginQueryKey, login),
	});
	return { login: query.data, start, cancel };
}

/** Single source of truth for whether the machine satisfies AO's startup
 *  requirements. Shared by SessionsBoard (which must keep the startup screen
 *  mounted while blocked) and DaemonStartupLoader (which renders the gate) so
 *  both read the same react-query cache entry and never disagree. */
export function useSystemRequirementsGate() {
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const query = useQuery({
		...systemRequirementsQueryOptions,
		queryKey: [...systemRequirementsQueryKey, baseUrl],
		// The preview build has no daemon, and the desktop must wait for its
		// trusted URL before making the first requirements request.
		enabled: !usesPreviewWorkspaceData && Boolean(baseUrl),
	});
	const requirements = query.data?.requirements ?? [];
	const requirementsBlocked =
		!usesPreviewWorkspaceData && query.isSuccess && requirements.some((r) => r.required && !r.satisfied);
	const checking = !usesPreviewWorkspaceData && query.isPending;
	// The board must stay behind the startup loader until the probe resolves.
	// `blocked` is the mount gate; `requirementsBlocked` is narrower and only
	// controls the dependency dialog once there is real missing-item data.
	const blocked = checking || requirementsBlocked;
	const ready = usesPreviewWorkspaceData || (query.isSuccess && !requirementsBlocked);
	// Once the daemon URL is trusted, fail open if its requirements probe errors
	// rather than wedging the user on the checking state forever.
	const probeFailed = query.isError;
	return { query, requirements, blocked, requirementsBlocked, checking, ready, probeFailed };
}
