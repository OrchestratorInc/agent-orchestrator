import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";

const transport = vi.hoisted(() => ({ read: vi.fn(), run: vi.fn(), navigate: vi.fn(), get: vi.fn() }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => transport.navigate }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { operator: { readWorkflow: transport.read, runVerb: transport.run } } }));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: transport.get }, getApiBaseUrl: () => "http://127.0.0.1:5100", subscribeApiBaseUrl: () => () => undefined, apiErrorMessage: () => "失败", apiErrorRequestId: () => undefined }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("../../hooks/useConversation", () => ({ useConversation: () => ({ snapshot: { turns: [{ state: "running", providerTurnId: "turn-1" }] } }) }));

import { WorkflowIntervention } from "./WorkflowIntervention";
import { setEventsConnectionState } from "../../lib/events-connection";
import { useWorkflowUi } from "../../stores/workflow-ui-store";
import type { WorkflowDefinition } from "../../hooks/useWorkflows";

afterEach(cleanup);

// 删除 Attempt 能力路径或 Session/generation/到期门禁，会允许错误目标操作或禁用有效目标。
it("真实作用域能力启用操作，身份漂移与过期闭锁，恢复回执只读", async () => {
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	setEventsConnectionState("connected");
	useWorkflowUi.setState({ requests: {}, drafts: {} });
	transport.run.mockReset();
	const capability = { observedSessionId: "s1", generation: "g1", expiresAt: new Date(Date.now() + 60000).toISOString(), expired: false, readiness: "ready", authState: "authorized", queue: "supported", steer: "supported", interrupt: "supported" };
	transport.read.mockImplementation(async ({ verb, operationPath }) => {
		const data = verb === "binding" ? { binding: { controllerGeneration: "g1" } }
			: verb === "observe" ? { live: true, observation: { sessionId: "s1", controllerGeneration: "g1" } }
			: verb === "attempt-capabilities" && operationPath === "/api/v1/workflows/w/attempts/a/capabilities" ? { capabilities: [{ ...capability }] }
			: { interventions: [{ id: "old", mode: "queue", state: "delivered", clientMessageId: "original-message" }] };
		return { status: 200, body: data };
	});
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const attempt = { id: "a", sessionId: "s1" } as components["schemas"]["WorkflowAttemptResponse"];
	render(<QueryClientProvider client={client}><WorkflowIntervention definition={{ workflow: { id: "w" }, tasks: [] } as unknown as WorkflowDefinition} attempt={attempt} current /></QueryClientProvider>);
	fireEvent.change(screen.getByRole("textbox", { name: "workflow.guidance" }), { target: { value: "跟进" } });
	await waitFor(() => expect(screen.getByRole("button", { name: "workflow.steer" })).toBeEnabled());
	expect(screen.getByRole("button", { name: "workflow.queue" })).toBeEnabled();
	expect(screen.getByRole("button", { name: "workflow.stop" })).toBeEnabled();
	expect(screen.getByRole("status")).toHaveTextContent("original-message");
	for (const invalid of [{ observedSessionId: "other" }, { observedSessionId: "s1", generation: "old" }, { generation: "g1", expiresAt: new Date(0).toISOString() }]) {
		Object.assign(capability, invalid);
		await client.invalidateQueries();
		await waitFor(() => expect(screen.getByRole("button", { name: "workflow.queue" })).toBeDisabled());
		expect(screen.getByRole("button", { name: "workflow.steer" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "workflow.stop" })).toBeDisabled();
	}
	expect(transport.run).not.toHaveBeenCalled();
});

// 删除现有 Session 导航或定时回执刷新，将无法从工作台管理队列或永远看到旧 queued。
it("队列入口导航既有会话，刷新取消回执不重发", async () => {
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	useWorkflowUi.setState({ open: true, requests: {}, drafts: {} });
	transport.run.mockClear();
	let state = "queued";
	transport.read.mockImplementation(async ({ verb }) => ({ status: 200, body: verb === "intervention-receipts"
		? { interventions: [{ id: "queue", mode: "queue", state, clientMessageId: "same" }] }
		: verb === "binding" ? { binding: { controllerGeneration: "g" } } : verb === "attempt-capabilities" ? { capabilities: [] } : {} }));
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={client}><WorkflowIntervention definition={{ workflow: { id: "w" }, tasks: [], repositories: [{ repoKey: "r", projectId: "p" }], taskRepositories: [{ taskId: "t", repoKey: "r" }] } as unknown as WorkflowDefinition} attempt={{ id: "a", taskId: "t", sessionId: "s" } as components["schemas"]["WorkflowAttemptResponse"]} current /></QueryClientProvider>);
	fireEvent.click(await screen.findByRole("button", { name: "workflow.manageQueue" }));
	expect(transport.navigate).toHaveBeenLastCalledWith("p", "s");
	expect(useWorkflowUi.getState().open).toBe(false);
	state = "cancelled";
	await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("cancelled"), { timeout: 6500 });
	expect(screen.queryByRole("button", { name: "workflow.manageQueue" })).toBeNull();
	expect(transport.run).not.toHaveBeenCalled();
}, 8000);

/** 删除 Codex 规划选择边界会把 sonnet 提交给 Codex；忽略原任务又会丢失固定 profile。 */
it("确认停止后沿用 Codex Planner，切换固定供应商任务保留完整选择", async () => {
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	setEventsConnectionState("connected");
	useWorkflowUi.setState({ requests: {}, drafts: {} });
	transport.run.mockReset().mockResolvedValue({ status: 200, body: {} });
	const profile = `custom@${"a".repeat(64)}`;
	transport.get.mockResolvedValue({ data: { profiles: [{ reference: profile, name: "固定供应商", harness: "codex", model: "provider-model", effort: "high" }] } });
	let attempt = { id: "native-attempt", taskId: "native", sessionId: "s1" } as components["schemas"]["WorkflowAttemptResponse"];
	let state = "stop_unknown";
	transport.read.mockImplementation(async ({ verb }) => ({ status: 200, body: verb === "binding"
		? { binding: { controllerGeneration: "g1" } }
		: verb === "intervention-receipts" ? { interventions: [{ id: `stop-${attempt.id}`, mode: "stop", state, attemptId: attempt.id, sessionId: attempt.sessionId, controllerGeneration: "g1" }] }
		: verb === "attempt-capabilities" ? { capabilities: [] } : {} }));
	const definition = { workflow: { id: "w", revision: 8 }, tasks: [
		{ id: "planner", planner: true, agentHarness: "codex", model: "gpt-planner", effort: "medium", state: "succeeded" },
		{ id: "native", agentHarness: "claude-code", model: "sonnet", effort: "low" },
		{ id: "fixed", agentHarness: "codex", model: "provider-model", effort: "high", profile },
	] } as unknown as WorkflowDefinition;
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(<QueryClientProvider client={client}><WorkflowIntervention key={attempt.id} definition={definition} attempt={attempt} current /></QueryClientProvider>);
	expect(await screen.findByRole("status")).toHaveTextContent("stop_unknown");
	expect(screen.queryByRole("button", { name: "workflow.startReplan" })).toBeNull();
	state = "stop_confirmed";
	await client.invalidateQueries();
	fireEvent.click(await screen.findByRole("button", { name: "workflow.startReplan" }));
	await waitFor(() => expect(transport.run).toHaveBeenCalledTimes(1));
	expect(transport.run.mock.calls[0][0]).toMatchObject({ verb: "start-replan", operationPath: "/api/v1/workflows/w/replan" });
	expect(JSON.parse(transport.run.mock.calls[0][0].body)).toMatchObject({ stopInterventionId: "stop-native-attempt", expectedRevision: 8, agentHarness: "codex", model: "gpt-planner", effort: "medium", profile: "" });
	attempt = { ...attempt, id: "fixed-attempt", taskId: "fixed" };
	view.rerender(<QueryClientProvider client={client}><WorkflowIntervention key={attempt.id} definition={definition} attempt={attempt} current /></QueryClientProvider>);
	fireEvent.click(await screen.findByRole("button", { name: "workflow.startReplan" }));
	await waitFor(() => expect(transport.run).toHaveBeenCalledTimes(2));
	expect(JSON.parse(transport.run.mock.calls[1][0].body)).toMatchObject({ stopInterventionId: "stop-fixed-attempt", agentHarness: "codex", model: "provider-model", effort: "high", profile });
	expect(screen.getByRole("textbox", { name: "workflow.replanModel" })).toBeDisabled();
});
