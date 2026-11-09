import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import type { IpcMainInvokeEvent } from "electron";
import { registerOperatorIpc } from "../../../main/operator-ipc";

const transport = vi.hoisted(() => ({ base: "http://127.0.0.1:5100", run: vi.fn(), read: vi.fn(), authorize: vi.fn() }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { operator: { runVerb: transport.run, readWorkflow: transport.read, authorizeWorkflow: transport.authorize } } }));
vi.mock("../../lib/api-client", () => ({ apiClient: {}, getApiBaseUrl: () => transport.base, subscribeApiBaseUrl: () => () => undefined, apiErrorMessage: (e: { message?: string }) => e.message || "失败", apiErrorRequestId: () => undefined }));
vi.mock("react-i18next", async () => {
	const { createFullAppI18n } = await import("../../i18n/all-messages");
	const i18n = createFullAppI18n("zh-CN");
	return { useTranslation: () => ({ t: i18n.t.bind(i18n) }) };
});

import { WorkflowAction } from "./WorkflowAction";
import { currentArtifactGate } from "./WorkflowQualityPanel";
import { WorkflowBudgetPanel, workflowMoney } from "./WorkflowBudgetPanel";
import { WorkflowGraph } from "./WorkflowGraph";
import { useWorkflowResource, type WorkflowDefinition } from "../../hooks/useWorkflows";
import { useWorkflowDraft, useWorkflowUi } from "../../stores/workflow-ui-store";
import { setEventsConnectionState } from "../../lib/events-connection";

function harness() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	vi.clearAllMocks();
	transport.base = "http://127.0.0.1:5100";
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	setEventsConnectionState("connected");
	useWorkflowUi.setState({ requests: {}, drafts: {} });
});
afterEach(cleanup);

// 删除 Task 选择值的范围隔离后，同名任务会回显其他策略并送往错误的审批目标。
it("同名 Task 与全局、Workflow 预算独立回显并经真实 IPC 审批正确目标", async () => {
	const targets = [
		{ label: "任务：global", scope: "task", scopeId: "global", path: "/api/v1/workflows/wf/tasks/global/budget", limit: 3, mode: "hard_admission", revision: 7 },
		{ label: "任务：workflow", scope: "task", scopeId: "workflow", path: "/api/v1/workflows/wf/tasks/workflow/budget", limit: 4, mode: "soft", revision: 8 },
		{ label: "全局", scope: "global", scopeId: "", path: "/api/v1/scheduler/budget", limit: 1, mode: "observe", revision: 5 },
		{ label: "工作流", scope: "workflow", scopeId: "wf", path: "/api/v1/workflows/wf/budget", limit: 2, mode: "soft", revision: 6 },
	] as const;
	const data: components["schemas"]["WorkflowCoordinationResponse"] = { budget: { snapshots: [], policies: targets.map(target => ({
		id: `policy-${target.limit}`, scope: target.scope, scopeId: target.scopeId, period: "lifetime", currency: "USD",
		limitNanos: target.limit * 1e9, reservationNanos: target.limit * 1e8, reservationBasis: "operator_approved", reservationRef: "fixture", mode: target.mode, revision: target.revision,
	})) }, coordination: { workflowId: "wf", observedAt: "2026-11-07T00:00:00Z", messages: [], reservations: [], tasks: [], truncated: false },
	quality: { artifacts: [], commits: [], currentPolicy: null, gates: [], integrations: [], operations: [], reviews: [], truncated: false } };
	const handlers = new Map<string, (event: IpcMainInvokeEvent, input: unknown) => unknown>();
	const frame = { url: "app://renderer/index.html", parent: null };
	const sender = { mainFrame: frame, isDestroyed: () => false };
	const event = { sender, senderFrame: frame } as unknown as IpcMainInvokeEvent;
	const confirmOperation = vi.fn(async () => true);
	let approval: Record<string, unknown>;
	// 只替换系统确认及 HTTP；组件、Hook、动作重放和 Main 路由/正文绑定均使用正式实现。
	const request = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
		if (String(url).endsWith("/operator/approvals")) {
			const created = JSON.parse(init!.body as string);
			approval = { approvalId: "approval-1", code: "12345678", state: "pending", expiresAt: "2099-01-01T00:00:00Z",
				operation: { method: created.operationMethod, path: created.operationPath }, payloadDigest: created.payloadDigest };
			return Response.json({ approval }, { status: 201 });
		}
		if (String(url).endsWith("/confirm")) return Response.json({ approval: { ...approval, code: undefined, state: "confirmed" } });
		return Response.json({ applied: true });
	});
	registerOperatorIpc({ ipcMain: { handle: (channel, handler) => { handlers.set(channel, handler); } },
		baseURL: () => transport.base, trustedWebContents: () => sender as never,
		rendererURL: () => frame.url, confirmOperation, request });
	transport.run.mockImplementation(input => handlers.get("operator:runVerb")!(event, input));
	render(<WorkflowBudgetPanel id="wf" taskIds={["global", "workflow"]} data={data} />, { wrapper: harness() });
	for (const target of targets) {
		fireEvent.keyDown(screen.getByRole("combobox", { name: "预算范围" }), { key: "ArrowDown" });
		fireEvent.click(await screen.findByRole("option", { name: target.label }));
		await waitFor(() => expect(screen.getByRole("spinbutton", { name: "上限 USD" })).toHaveValue(target.limit));
		expect(screen.getByRole("spinbutton", { name: "每次准入预留 USD" })).toHaveValue(target.limit / 10);
		expect(screen.getByRole("combobox", { name: "控制模式" })).toHaveTextContent(target.mode);
		fireEvent.change(screen.getByRole("spinbutton", { name: "上限 USD" }), { target: { value: String(target.limit + 10) } });
		fireEvent.change(screen.getByRole("textbox", { name: "修改原因" }), { target: { value: target.label } });
		request.mockClear(); confirmOperation.mockClear();
		fireEvent.click(screen.getByRole("button", { name: "审批并保存预算" }));
		await waitFor(() => expect(request).toHaveBeenCalledTimes(3));
		await waitFor(() => expect(Object.keys(useWorkflowUi.getState().requests)).toHaveLength(0));
		const raw = transport.run.mock.calls.at(-1)![0].body;
		expect(JSON.parse(raw)).toEqual({ id: `policy-${target.limit}`, currency: "USD", expectedRevision: target.revision,
			limitNanos: (target.limit + 10) * 1e9, mode: target.mode, period: "lifetime", reason: target.label,
			reservationNanos: target.limit * 1e8, reservationBasis: "operator_approved", reservationRef: target.label, idempotencyKey: expect.any(String) });
		expect(confirmOperation).toHaveBeenCalledExactlyOnceWith({ verb: "budget", method: "PUT", operationPath: target.path, body: raw });
		expect(JSON.parse(request.mock.calls[0][1]!.body as string)).toMatchObject({ operationMethod: "PUT", operationPath: target.path, payload: raw });
		expect(request.mock.calls[2]).toEqual([`${transport.base}${target.path}`, expect.objectContaining({ method: "PUT", body: raw })]);
	}
});

it("明确取消撤销待重放意图，草稿保留且下次使用新正文与新身份", async () => {
	transport.run.mockResolvedValueOnce({ status: 499, code: "OPERATOR_CANCELLED", body: { message: "已取消" } }).mockResolvedValue({ status: 200, body: {} });
	useWorkflowUi.setState({ drafts: { guidance: "原草稿" } });
	const view = render(<WorkflowAction verb="intervention" path="/api/v1/workflows/w/attempts/a/interventions" keyField="clientMessageId" payload={{ mode: "queue", text: "原文" }}>发送</WorkflowAction>, { wrapper: harness() });
	fireEvent.click(screen.getByRole("button"));
	await screen.findByRole("alert");
	await waitFor(() => expect(Object.keys(useWorkflowUi.getState().requests)).toHaveLength(0));
	expect(useWorkflowUi.getState().drafts.guidance).toBe("原草稿");
	expect(transport.run).toHaveBeenCalledOnce();
	view.rerender(<WorkflowAction verb="intervention" path="/api/v1/workflows/w/attempts/a/interventions" keyField="clientMessageId" payload={{ mode: "queue", text: "修改后" }}>发送</WorkflowAction>);
	await waitFor(() => expect(screen.getByRole("button", { name: "发送" })).toBeEnabled());
	fireEvent.click(screen.getByRole("button", { name: "发送" }));
	await waitFor(() => expect(transport.run).toHaveBeenCalledTimes(2));
	const first = JSON.parse(transport.run.mock.calls[0][0].body), next = JSON.parse(transport.run.mock.calls[1][0].body);
	expect(next.text).toBe("修改后");
	expect(next.clientMessageId).not.toBe(first.clientMessageId);
});

it("超时后版本改变仍重放原请求，旧目标回包不写入当前页面", async () => {
	transport.run.mockRejectedValueOnce(new Error("超时")).mockResolvedValue({ status: 200, body: {} });
	const onSuccess = vi.fn();
	const wrapper = harness();
	const view = render(<WorkflowAction verb="intervention" path="/api/v1/workflows/w/attempts/a/interventions" keyField="clientMessageId" payload={{ mode: "queue", text: "原文", controllerGeneration: "g1" }} onSuccess={onSuccess}>发送</WorkflowAction>, { wrapper });
	fireEvent.click(screen.getByRole("button", { name: "发送" }));
	await screen.findByRole("alert");
	const original = transport.run.mock.calls[0][0];
	view.rerender(<WorkflowAction verb="intervention" path="/api/v1/workflows/w/attempts/a/interventions" keyField="clientMessageId" payload={{ mode: "queue", text: "修改后", controllerGeneration: "g2" }} onSuccess={onSuccess}>发送</WorkflowAction>);
	fireEvent.click(screen.getByRole("button", { name: "重试原请求" }));
	await waitFor(() => expect(Object.keys(useWorkflowUi.getState().requests)).toHaveLength(0));
	expect(onSuccess).not.toHaveBeenCalled();
	expect(transport.run.mock.calls[1][0]).toEqual(original);
	expect(JSON.parse(original.body)).toMatchObject({ text: "原文", controllerGeneration: "g1", clientMessageId: expect.any(String) });
});

it("切换目标后忽略旧操作成功回调，daemon 改变时拒绝旧回包", async () => {
	let resolve!: (value: unknown) => void;
	transport.run.mockImplementation(() => new Promise(r => { resolve = r; }));
	const first = vi.fn(), second = vi.fn();
	const view = render(<WorkflowAction verb="activate" path="/api/v1/workflows/a/activate" payload={{ expectedRevision: 1 }} onSuccess={first}>激活</WorkflowAction>, { wrapper: harness() });
	fireEvent.click(screen.getByRole("button"));
	await waitFor(() => expect(transport.run).toHaveBeenCalledOnce());
	view.rerender(<WorkflowAction verb="activate" path="/api/v1/workflows/b/activate" payload={{ expectedRevision: 1 }} onSuccess={second}>激活</WorkflowAction>);
	await act(async () => resolve({ status: 200, body: {} }));
	expect(first).not.toHaveBeenCalled();
	expect(second).not.toHaveBeenCalled();
	await waitFor(() => expect(screen.getByRole("button")).toBeEnabled());
	fireEvent.click(screen.getByRole("button"));
	await waitFor(() => expect(transport.run).toHaveBeenCalledTimes(2));
	transport.base = "http://127.0.0.1:5200";
	await act(async () => resolve({ status: 200, body: {} }));
	expect(second).not.toHaveBeenCalled();
	expect(await screen.findByRole("alert")).toHaveTextContent("daemon 已变更");
});

it("受保护查询不自动授权，旧工作流快照不覆盖当前工作流", async () => {
	let oldReply!: (value: unknown) => void;
	transport.read.mockImplementation(({ workflowId }) => workflowId === "old" ? new Promise(resolve => { oldReply = resolve; }) : Promise.resolve({ status: 200, body: { title: "新工作流" } }));
	const { result, rerender } = renderHook(({ id }) => useWorkflowResource<{ title: string }>(id, "coordination", `/api/v1/workflows/${id}/coordination-summary`), { initialProps: { id: "old" }, wrapper: harness() });
	await waitFor(() => expect(transport.read).toHaveBeenCalledOnce());
	rerender({ id: "new" });
	await waitFor(() => expect(result.current.data?.title).toBe("新工作流"));
	await act(async () => oldReply({ status: 200, body: { title: "旧工作流" } }));
	expect(result.current.data?.title).toBe("新工作流");
	expect(transport.authorize).not.toHaveBeenCalled();
});

it("切换资源并重新挂载后保留各自草稿", () => {
	const first = renderHook(({ key }) => useWorkflowDraft(key, ""), { initialProps: { key: "a" } });
	act(() => first.result.current[1]("未发送"));
	first.rerender({ key: "b" });
	expect(first.result.current[0]).toBe("");
	first.unmount();
	const second = renderHook(() => useWorkflowDraft("a", ""));
	expect(second.result.current[0]).toBe("未发送");
});

it("最新失败、过期、旧 Attempt 和截断快照不能借旧 PASS；未知费用保留未知", () => {
	type S = components["schemas"];
	const artifact = { id: "a", taskId: "t", attemptId: "new", repo: { repoKey: "r" }, artifactRevision: 1, state: "verified", fingerprint: "fp" } as S["ArtifactSnapshot"];
	const pass = { id: "p", artifactId: "a", artifactFingerprint: "fp", policyDigest: "policy", createdAt: "2026-11-06T01:00:00Z", expiresAt: "2026-11-06T03:00:00Z", state: "PASS" } as S["QualityGateResponse"];
	const quality = { artifacts: [artifact], currentPolicy: { digest: "policy" }, gates: [pass], truncated: false } as S["WorkflowQualitySummaryResponse"];
	const now = Date.parse("2026-11-06T02:00:00Z");
	expect(currentArtifactGate(artifact, quality, now, "new")?.state).toBe("PASS");
	quality.gates.push({ ...pass, id: "fail", state: "FAIL", createdAt: "2026-11-06T01:30:00Z" });
	expect(currentArtifactGate(artifact, quality, now, "new")?.state).toBe("FAIL");
	expect(currentArtifactGate(artifact, quality, Date.parse(pass.expiresAt), "new")).toBeUndefined();
	expect(currentArtifactGate(artifact, quality, now, "other")).toBeUndefined();
	quality.truncated = true;
	expect(currentArtifactGate(artifact, quality, now, "new")).toBeUndefined();
	expect(workflowMoney(null)).toBe("UNKNOWN");
	expect(workflowMoney(0)).toBe("$0.0000");
});

it("不完整旧图中的任务仍可通过按钮定位", () => {
	const onSelect = vi.fn();
	const definition = { tasks: [{ id: "t", title: "旧任务", state: "blocked" }], edges: [{ fromTaskId: "missing", toTaskId: "t" }] } as WorkflowDefinition;
	render(<WorkflowGraph definition={definition} selected="" onSelect={onSelect} />);
	fireEvent.click(screen.getByRole("button", { name: /旧任务/ }));
	expect(onSelect).toHaveBeenCalledWith("t");
});
