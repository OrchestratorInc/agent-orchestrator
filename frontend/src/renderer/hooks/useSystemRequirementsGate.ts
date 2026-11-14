import { apiErrorException } from "../lib/api-error-exception";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import { markTerminalHandleFresh } from "../lib/fresh-terminal-handles";
import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { shellTerminalsQueryKey, shellTerminalsQueryOptions, type ShellTerminal } from "./useShellTerminals";

export type SystemRequirement = components["schemas"]["SystemRequirement"];

export const systemRequirementsQueryKey = ["system-requirements"] as const;
export const githubAuthTerminalQueryKey = ["github-auth-terminal"] as const;
export const githubAuthAutoLoginOfferedQueryKey = ["github-auth-auto-login-offered"] as const;
const GITHUB_AUTH_POLL_INTERVAL_MS = 2_500;

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
	enabled: !usesPreviewWorkspaceData,
};

export const githubAuthRequirementQueryOptions = {
	queryKey: ["github-auth-requirement"] as const,
	queryFn: fetchGitHubAuthRequirement,
	refetchOnWindowFocus: false,
	enabled: !usesPreviewWorkspaceData,
};

export function useGitHubAuthRequirement(loginActive = false) {
	return useQuery({
		...githubAuthRequirementQueryOptions,
		refetchInterval: loginActive ? GITHUB_AUTH_POLL_INTERVAL_MS : false,
	});
}

export function useStartGitHubAuthTerminal() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (): Promise<ShellTerminal> => {
			const terminals = await queryClient.fetchQuery({ ...shellTerminalsQueryOptions, staleTime: 0 });
			const existing = terminals.find((terminal) => terminal.title === "Connect GitHub" && !terminal.sessionId && !terminal.projectId);
			if (existing) return existing;
			const { data, error } = await apiClient.POST("/api/v1/system/github-auth/terminal");
			if (error || !data) throw apiErrorException(error, "Could not start GitHub sign-in.");
			markTerminalHandleFresh(data.shellTerminal.handleId);
			return data.shellTerminal;
		},
		onSuccess: (terminal) => {
			queryClient.setQueryData<ShellTerminal | null>(githubAuthTerminalQueryKey, terminal);
			queryClient.setQueryData<ShellTerminal[]>(shellTerminalsQueryKey, (current = []) => [
				...current.filter((item) => item.handleId !== terminal.handleId),
				terminal,
			]);
			void queryClient.invalidateQueries({ queryKey: shellTerminalsQueryKey });
		},
	});
}

export function useGitHubAuthTerminal() {
	const queryClient = useQueryClient();
	const query = useQuery<ShellTerminal | null>({
		queryKey: githubAuthTerminalQueryKey,
		queryFn: async () => null,
		enabled: false,
		initialData: null,
		gcTime: Number.POSITIVE_INFINITY,
	});
	const clear = useCallback(() => {
		queryClient.setQueryData<ShellTerminal | null>(githubAuthTerminalQueryKey, null);
	}, [queryClient]);
	return { ...query, clear };
}

export function useGitHubAuthAutoLoginOffered() {
	const queryClient = useQueryClient();
	const query = useQuery<boolean>({
		queryKey: githubAuthAutoLoginOfferedQueryKey,
		queryFn: async () => false,
		enabled: false,
		initialData: false,
		gcTime: Number.POSITIVE_INFINITY,
	});
	const markOffered = useCallback(() => {
		queryClient.setQueryData(githubAuthAutoLoginOfferedQueryKey, true);
	}, [queryClient]);
	return { offered: query.data, markOffered };
}

export function useSystemRequirementsGate() {
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const query = useQuery({
		...systemRequirementsQueryOptions,
		queryKey: [...systemRequirementsQueryKey, baseUrl],
		enabled: !usesPreviewWorkspaceData && Boolean(baseUrl),
	});
	const requirements = query.data?.requirements ?? [];
	const requirementsBlocked =
		!usesPreviewWorkspaceData && query.isSuccess && requirements.some((r) => r.required && !r.satisfied);
	const checking = !usesPreviewWorkspaceData && query.isPending;
	const blocked = checking || requirementsBlocked;
	const ready = usesPreviewWorkspaceData || (query.isSuccess && !requirementsBlocked);
	const probeFailed = query.isError;
	return { query, requirements, blocked, requirementsBlocked, checking, ready, probeFailed };
}
