import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AgentCheckError } from "../lib/agent-check-error";
import type { components } from "../../api/schema";
import { apiErrorCode, apiErrorDetails, apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { aoBridge } from "../lib/bridge";
import { LOCAL_HOST } from "../lib/hosts";
import type { AgentLoginRequest } from "../../main/agent-login-ipc";
import { shellTerminalsQueryKeyForHost, type ShellTerminal } from "./useShellTerminals";

export type AgentAuthPlan = components["schemas"]["AgentAuthPlan"];
export type StartAgentAuthResponse = components["schemas"]["StartAgentAuthResponse"];
export type AgentLoginSnapshot = components["schemas"]["AgentLoginSnapshot"];

type LoginResult<T> = { data?: T; error?: unknown; status?: number };

// Local sign-in goes through the main process so it never queues behind the
// renderer's per-origin socket limit: event streams and slow readiness probes
// can hold every slot for many seconds. Remote hosts and the browser preview
// (no main process) use HTTP.
async function viaMain<T>(hostId: string | undefined, request: AgentLoginRequest): Promise<LoginResult<T> | null> {
	if (hostId && hostId !== LOCAL_HOST) return null;
	const result = await aoBridge.agentLogin?.request(request);
	if (!result) return null;
	return result.status >= 200 && result.status < 300
		? { data: result.body as T, status: result.status }
		: { error: result.body ?? {}, status: result.status };
}

// Credentials stay out of React Query's mutation cache and terminal replay.
// An empty secret on a provider-key method reuses the key AO already saved;
// device also publishes the key to the OS user environment.
export async function startAgentUILogin(agentId: string, method: string, secret: string, hostId?: string, model?: string, device?: boolean): Promise<AgentLoginSnapshot> {
	return startLogin(agentId, method, hostId, { ...(secret ? { secret } : {}), ...(model ? { model } : {}), ...(device ? { device: true } : {}) });
}

/** Saves a key for a provider picked from the harness's own catalog, in that harness's own store. */
export async function startCatalogKeyLogin(agentId: string, provider: string, secret: string, hostId?: string): Promise<AgentLoginSnapshot> {
	return startLogin(agentId, "provider_key", hostId, { secret, provider });
}

async function startLogin(agentId: string, method: string, hostId: string | undefined, options: { secret?: string; model?: string; device?: boolean; provider?: string }): Promise<AgentLoginSnapshot> {
	const { data, error } = await viaMain<AgentLoginSnapshot>(hostId, { action: "start", agentId, method, ...options })
		?? await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/auth/operations", {
			params: { path: { agent: agentId } }, body: { method, ...options },
		});
	// Recover a login whose dialog was lost on reload without starting a second
	// native process against the same credential store.
	if (apiErrorCode(error) === "AGENT_AUTH_IN_PROGRESS") {
		const id = apiErrorDetails(error)?.operationId;
		if (typeof id === "string") return getAgentUILogin(agentId, id, hostId);
	}
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not start sign-in."));
	return data;
}

export async function getAgentUILogin(agentId: string, operationId: string, hostId?: string): Promise<AgentLoginSnapshot> {
	const result: LoginResult<AgentLoginSnapshot> = await viaMain<AgentLoginSnapshot>(hostId, { action: "get", agentId, operationId })
		?? await clientForSessionHost(hostId).GET("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: agentId, operationId } } })
			.then(({ data, error, response }) => ({ data, error, status: response?.status }));
	const { data, error, status } = result;
	if (error || !data) throw Object.assign(new Error(apiErrorMessage(error, "Could not check sign-in.")), { code: apiErrorCode(error), status });
	return data;
}

export type AgentLoginMenuAction = "previous" | "next" | "select" | "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9";

export async function actOnAgentUILogin(agentId: string, operationId: string, action: AgentLoginMenuAction, hostId?: string): Promise<AgentLoginSnapshot> {
	const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/auth/operations/{operationId}/actions", {
		params: { path: { agent: agentId, operationId } }, body: { action },
	});
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not control sign-in."));
	return data;
}

export async function cancelAgentUILogin(agentId: string, operationId: string, hostId?: string): Promise<void> {
	const { error } = await viaMain<unknown>(hostId, { action: "cancel", agentId, operationId })
		?? await clientForSessionHost(hostId).DELETE("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: agentId, operationId } } });
	if (error && apiErrorCode(error) !== "AGENT_AUTH_OPERATION_NOT_FOUND") throw new Error(apiErrorMessage(error, "Could not stop sign-in."));
}

export async function submitAgentUILoginCode(agentId: string, operationId: string, code: string, hostId?: string): Promise<AgentLoginSnapshot> {
	const { data, error } = await viaMain<AgentLoginSnapshot>(hostId, { action: "code", agentId, operationId, code })
		?? await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/auth/operations/{operationId}/code", {
			params: { path: { agent: agentId, operationId } }, body: { code },
		});
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not submit sign-in code."));
	return data;
}

export const agentAuthPlansQueryKey = ["agent-auth-plans"] as const;
export const agentAuthPlansQueryKeyForHost = (hostId?: string) => hostId ? ["agent-auth-plans", hostId] as const : agentAuthPlansQueryKey;

async function fetchAgentAuthPlans(hostId?: string): Promise<AgentAuthPlan[]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/auth-plans");
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not load agent authentication plans."));
	return data.plans;
}

export function useAgentAuthPlans(hostId?: string, enabled = true) {
	return useQuery({ queryKey: agentAuthPlansQueryKeyForHost(hostId), queryFn: () => fetchAgentAuthPlans(hostId), staleTime: 60_000, enabled });
}

export function useStartAgentAuth(hostId?: string, action: "login" | "logout" = "login") {
	const queryClient = useQueryClient();
	const shellQueryKey = shellTerminalsQueryKeyForHost(hostId);
	return useMutation({
		mutationFn: async (agentId: string): Promise<StartAgentAuthResponse> => {
			const { data, error } = await clientForSessionHost(hostId).POST(action === "logout" ? "/api/v1/agents/{agent}/logout" : "/api/v1/agents/{agent}/auth", {
				params: { path: { agent: agentId } },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, action === "logout" ? "Could not start agent logout." : "Could not start agent authentication."));
			return data;
		},
		onSuccess: (result) => {
			const terminal: ShellTerminal = {
				handleId: result.terminal.handleId,
				projectId: result.terminal.projectId,
				sessionId: result.terminal.sessionId,
				workingDir: result.terminal.workingDir,
				title: result.terminal.title,
				createdAt: result.terminal.createdAt,
				...(hostId ? { hostId } : {}),
			};
			queryClient.setQueryData<ShellTerminal[]>(shellQueryKey, (current = []) => [
				...current.filter((item) => item.handleId !== terminal.handleId),
				terminal,
			]);
			void queryClient.invalidateQueries({ queryKey: shellQueryKey });
		},
	});
}

export async function probeAgentAuth(agentId: string, hostId?: string) {
	const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/probe", {
		params: { path: { agent: agentId } },
	});
	if (error || !data) throw new AgentCheckError("Authentication probe", error, response?.status);
	return data;
}

export type CatalogProvider = components["schemas"]["CatalogProvider"];

/** Key-only providers in the harness's own provider catalog on this host. */
export async function fetchCatalogProviders(agentId: string, hostId?: string): Promise<CatalogProvider[]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/{agent}/auth/providers", { params: { path: { agent: agentId } } });
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not load providers."));
	return data.providers;
}
