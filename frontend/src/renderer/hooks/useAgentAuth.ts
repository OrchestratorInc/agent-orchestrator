import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AgentCheckError } from "../lib/agent-check-error";
import type { components } from "../../api/schema";
import { apiErrorCode, apiErrorDetails, apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { shellTerminalsQueryKeyForHost, type ShellTerminal } from "./useShellTerminals";

export type AgentAuthPlan = components["schemas"]["AgentAuthPlan"];
export type StartAgentAuthResponse = components["schemas"]["StartAgentAuthResponse"];
export type AgentLoginSnapshot = components["schemas"]["AgentLoginSnapshot"];

// Credentials stay out of React Query's mutation cache and terminal replay.
export async function startAgentUILogin(agentId: string, method: string, secret: string, hostId?: string): Promise<AgentLoginSnapshot> {
	const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/auth/operations", {
		params: { path: { agent: agentId } }, body: { method, ...(secret ? { secret } : {}) },
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
	const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: agentId, operationId } } });
	if (error || !data) throw Object.assign(new Error(apiErrorMessage(error, "Could not check sign-in.")), { code: apiErrorCode(error), status: response?.status });
	return data;
}

export async function cancelAgentUILogin(agentId: string, operationId: string, hostId?: string): Promise<void> {
	const { error } = await clientForSessionHost(hostId).DELETE("/api/v1/agents/{agent}/auth/operations/{operationId}", { params: { path: { agent: agentId, operationId } } });
	if (error && apiErrorCode(error) !== "AGENT_AUTH_OPERATION_NOT_FOUND") throw new Error(apiErrorMessage(error, "Could not stop sign-in."));
}

export async function submitAgentUILoginCode(agentId: string, operationId: string, code: string, hostId?: string): Promise<AgentLoginSnapshot> {
	const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/auth/operations/{operationId}/code", {
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
