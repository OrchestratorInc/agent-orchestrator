import { useSyncExternalStore } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import type { OperatorVerbRequest } from "../../main/operator-ipc";
import { apiClient, apiErrorMessage, apiErrorRequestId, getApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { getEventsConnectionState } from "../lib/events-connection";

export type WorkflowDefinition = components["schemas"]["WorkflowDefinitionResponse"];
export const workflowQueryRoot = ["workflows"] as const;

/** 同一 daemon、Workflow 和资源使用独立缓存；延迟响应不能覆盖当前选择。 */
export function useWorkflowScope() {
	const base = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl);
	return [...workflowQueryRoot, base] as const;
}

/** 保留后端 request ID，便于定位权限、版本或通信失败。 */
export function workflowError(error: unknown): Error {
	const requestId = apiErrorRequestId(error);
	return new Error(`${apiErrorMessage(error)}${requestId ? ` · ${requestId}` : ""}`);
}

export function useWorkflows(cursor?: string) {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, "list", cursor], enabled: !!scope[1], queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/workflows", { signal, params: { query: { limit: 50, cursor } } });
		if (error) throw workflowError(error);
		return data!;
	} });
}

export function useWorkflow(id: string) {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, id, "definition"], enabled: !!scope[1] && !!id, queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/workflows/{workflowId}", { signal, params: { path: { workflowId: id } } });
		if (error) throw workflowError(error);
		return data!;
	} });
}

export function useWorkflowCoordination(id: string) {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, id, "coordination"], retry: false, enabled: !!scope[1] && !!id && !!window.ao, queryFn: async () => {
		return protectedWorkflowRead<components["schemas"]["WorkflowCoordinationResponse"]>(id, "coordination", `/api/v1/workflows/${id}/coordination-summary`);
	} });
}

/** refetchInterval 由调用方按持久 attempt 状态决定，终态必须返回 false。 */
export function useWorkflowTask(id: string, taskId: string, refetchInterval: (data: components["schemas"]["WorkflowTaskEnvelope"] | undefined) => number | false = () => false) {
	const scope = useWorkflowScope();
	return useQuery<components["schemas"]["WorkflowTaskEnvelope"]>({ queryKey: [...scope, id, taskId, "attempts"], refetchInterval: query => refetchInterval(query.state.data), enabled: !!scope[1] && !!id && !!taskId, queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/workflows/{workflowId}/tasks/{taskId}", { signal, params: { path: { workflowId: id, taskId } } });
		if (error) throw workflowError(error);
		return data!;
	} });
}

/** 调度暂停有独立 revision，不能借用 Workflow 的图版本。 */
export function useWorkflowScheduler(id: string) {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, id, "scheduler"], enabled: !!scope[1] && !!id, queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/workflows/{workflowId}/scheduler", { signal, params: { path: { workflowId: id } } });
		if (error) throw workflowError(error);
		return data!;
	} });
}

/** 开放只读的调度快照按 daemon 隔离，并复用工作流事件和保存后的缓存失效。 */
export function useSchedulerStatus() {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, "scheduler-status"], enabled: !!scope[1], queryFn: async ({ signal }) => {
		const { data, error } = await apiClient.GET("/api/v1/scheduler/status", { signal });
		if (error) throw workflowError(error);
		return data!;
	} });
}

export function useWorkflowCollaboration(id: string) {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, id, "collaboration"], retry: false, enabled: !!scope[1] && !!id && !!window.ao, queryFn: async () => {
		const [messages, requests] = await Promise.all([
			protectedWorkflowRead<components["schemas"]["ListWorkflowMessagesResponse"]>(id, "messages", `/api/v1/workflows/${id}/messages`),
			protectedWorkflowRead<components["schemas"]["ListWorkflowTaskRequestsResponse"]>(id, "task-requests", `/api/v1/workflows/${id}/task-requests`),
		]);
		return { messages, requests };
	} });
}

/** Main 只消费已有只读授权；自动查询永远不能发起 Windows Hello。 */
export async function protectedWorkflowRead<T>(workflowId: string, verb: OperatorVerbRequest["verb"], operationPath: string): Promise<T> {
	const result = await aoBridge.operator.readWorkflow({ workflowId, verb, operationPath });
	if (result.status < 200 || result.status >= 300) throw workflowError(result.body);
	return result.body as T;
}

/** 受保护资源共用已有 Main 授权，挂载、事件失效和重连均不发起原生确认。 */
export function useWorkflowResource<T>(id: string, verb: OperatorVerbRequest["verb"], path: string, enabled = true, refetchInterval: number | false = false) {
	const scope = useWorkflowScope();
	return useQuery({ queryKey: [...scope, id, verb, path], retry: false, refetchInterval, enabled: enabled && !!id && !!scope[1] && !!window.ao,
		queryFn: () => protectedWorkflowRead<T>(id, verb, path) });
}

/** 副作用不自动重试；调用方保留完整正文和幂等键，冲突/未知回执后刷新事实。 */
export function useWorkflowAction() {
	const client = useQueryClient();
	const scope = useWorkflowScope();
	return useMutation({ retry: false, mutationFn: async (input: OperatorVerbRequest) => {
		if (!window.ao || getEventsConnectionState() !== "connected" || getApiBaseUrl() !== scope[1]) throw new Error("连接未同步或缺少桌面审批能力");
		const result = await aoBridge.operator.runVerb(input);
		if (getApiBaseUrl() !== scope[1]) throw new Error("daemon 已变更，请重新读取原操作回执");
		if (result.status < 200 || result.status >= 300) throw Object.assign(workflowError(result.body), { status: result.status, code: result.code });
		return result.body;
	}, onSettled: () => client.invalidateQueries({ queryKey: scope }) });
}
