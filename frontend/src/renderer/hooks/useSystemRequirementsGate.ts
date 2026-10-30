import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import { markTerminalHandleFresh } from "../lib/fresh-terminal-handles";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { shellTerminalsQueryKey, shellTerminalsQueryOptions, type ShellTerminal } from "./useShellTerminals";

export type SystemRequirement = components["schemas"]["SystemRequirement"];

export const systemRequirementsQueryKey = ["system-requirements"] as const;
export const githubAuthTerminalQueryKey = ["github-auth-terminal"] as const;
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

/** Advisory authentication probe. Kept separate from the startup gate because
 * credential-store access can be slow or interactive on some machines. */
export function useGitHubAuthRequirement(loginActive = false) {
	return useQuery({
		...githubAuthRequirementQueryOptions,
		// The browser/device flow can finish before its PTY exit reaches the
		// renderer. Probe only while AO owns an active login terminal so the card
		// closes promptly after authorization without permanent background polling.
		refetchInterval: loginActive ? GITHUB_AUTH_POLL_INTERVAL_MS : false,
	});
}

export function useStartGitHubAuthTerminal() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (): Promise<ShellTerminal> => {
			// Renderer state is lost on reload, while daemon-owned login PTYs survive.
			// Reconcile before every start and fail without spawning if the list fails.
			const terminals = await queryClient.fetchQuery({ ...shellTerminalsQueryOptions, staleTime: 0 });
			const existing = terminals.find((terminal) => terminal.title === "Connect GitHub" && !terminal.sessionId && !terminal.projectId);
			if (existing) return existing;
			const { data, error } = await apiClient.POST("/api/v1/system/github-auth/terminal");
			if (error || !data) throw new Error(apiErrorMessage(error, "Could not start GitHub sign-in."));
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

/** The login PTY is intentionally app-owned rather than notice-owned. Keeping
 * its handle in query state lets the inline panel reattach after navigation
 * while the browser-based device flow is still in progress. */
export function useGitHubAuthTerminal() {
	const queryClient = useQueryClient();
	const query = useQuery<ShellTerminal | null>({
		queryKey: githubAuthTerminalQueryKey,
		queryFn: async () => null,
		enabled: false,
		initialData: null,
		// The notice renders only on the home page and the empty board, so opening
		// a project drops this query's last observer. A device-code login easily
		// outlives the default five-minute gcTime, and collecting the handle would
		// both break reattach and strand the PTY with no way to close it from the
		// notice. This entry holds a single handle and is cleared explicitly.
		gcTime: Number.POSITIVE_INFINITY,
	});
	const clear = useCallback(() => {
		queryClient.setQueryData<ShellTerminal | null>(githubAuthTerminalQueryKey, null);
	}, [queryClient]);
	return { ...query, clear };
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
