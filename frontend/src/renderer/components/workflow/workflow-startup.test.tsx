import { cleanup, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";

const transport = vi.hoisted(() => ({ get: vi.fn(), run: vi.fn(), read: vi.fn(), navigate: vi.fn() }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => transport.navigate }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { operator: { runVerb: transport.run, readWorkflow: transport.read } } }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: transport.get },
	getApiBaseUrl: () => "http://127.0.0.1:5100",
	subscribeApiBaseUrl: () => () => undefined,
	apiErrorMessage: (e: { message?: string }) => e.message || "失败",
	apiErrorRequestId: () => undefined,
}));
vi.mock("react-i18next", async () => {
	const { createFullAppI18n } = await import("../../i18n/all-messages");
	const i18n = createFullAppI18n("zh-CN");
	return { useTranslation: () => ({ t: i18n.t.bind(i18n) }) };
});

import { WorkflowCreate, WorkflowPlanEditor } from "./WorkflowPlanEditor";
import { WorkflowStartupSteps, usePlannerAttempt } from "./WorkflowStartupSteps";
import { useWorkflowUi } from "../../stores/workflow-ui-store";
import { setEventsConnectionState } from "../../lib/events-connection";
import type { WorkflowDefinition } from "../../hooks/useWorkflows";
import { canExtractPlan, createBlockers, plannerPollInterval, plannerStatus, startupStep } from "../../lib/workflow-startup";

const sha = "a".repeat(40);
const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>{children}</QueryClientProvider>;

beforeEach(() => {
	vi.clearAllMocks();
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	setEventsConnectionState("connected");
	useWorkflowUi.setState({ requests: {}, drafts: {} });
});

afterEach(cleanup);

/** 删除步骤投影会让全部步骤同时出现或跳过确认/恢复；UNKNOWN 必须独立于成功。 */
it("启动步骤只显示当前一步，按持久事实推进", () => {
	const base = { state: "active", candidates: [] as components["schemas"]["WorkflowPlanCandidate"][], paused: true };
	expect(startupStep({ ...base, state: "", planner: undefined })).toBe("create");
	expect(startupStep({ ...base, state: "draft", planner: undefined })).toBe("activate");
	expect(startupStep({ ...base, state: "completed", planner: undefined })).toBe("done");
	expect(startupStep({ ...base, planner: undefined })).toBe("wait");
	expect(startupStep({ ...base, planner: { state: "running" } })).toBe("wait");
	expect(startupStep({ ...base, planner: { state: "succeeded" } })).toBe("extract");
	const candidate = { confirmedRevision: 0 } as components["schemas"]["WorkflowPlanCandidate"];
	expect(startupStep({ ...base, planner: { state: "succeeded" }, candidates: [candidate] })).toBe("confirm");
	expect(startupStep({ ...base, planner: { state: "succeeded" }, candidates: [{ ...candidate, confirmedRevision: 2 }] })).toBe("resume");
	expect(startupStep({ ...base, paused: false, planner: { state: "succeeded" }, candidates: [{ ...candidate, confirmedRevision: 2 }] })).toBe("done");
});

/** Planner 状态分类错误会把 UNKNOWN 当成功并允许提取不存在的结果。 */
it("Planner 状态按 attempt 分类，仅 succeeded 可提取且会轮询", () => {
	expect(plannerStatus(undefined)).toBe("idle");
	expect(plannerStatus({ state: "claimed" })).toBe("planning");
	expect(plannerStatus({ state: "settling" })).toBe("planning");
	expect(plannerStatus({ state: "succeeded" })).toBe("succeeded");
	expect(plannerStatus({ state: "failed" })).toBe("failed");
	expect(plannerStatus({ state: "unknown" })).toBe("unknown");
	expect(plannerStatus({ state: "something-new" })).toBe("unknown");
	expect(canExtractPlan("succeeded")).toBe(true);
	for (const status of ["idle", "planning", "failed", "unknown"] as const) expect(canExtractPlan(status)).toBe(false);
	expect(plannerPollInterval("planning")).toBe(3000);
	expect(plannerPollInterval("idle")).toBe(3000);
	expect(plannerPollInterval("succeeded")).toBe(false);
	expect(plannerPollInterval("failed")).toBe(false);
	expect(plannerPollInterval("unknown")).toBe(false);
});

/** 表单阻塞原因必须逐字段给出；SHA 必须是完整提交，effort 不能脱离模型。 */
it("创建校验逐字段报告阻塞原因", () => {
	const repo = { baseSha: sha, targetRef: "refs/heads/main" };
	expect(createBlockers({ title: "", goal: "", repositories: [], model: "", effort: "" })).toEqual(["title", "goal", "repository"]);
	expect(createBlockers({ title: "t", goal: "g", repositories: [repo], model: "", effort: "" })).toEqual([]);
	expect(createBlockers({ title: "t", goal: "g", repositories: [{ ...repo, baseSha: "abc" }], model: "", effort: "" })).toEqual(["baseSha"]);
	expect(createBlockers({ title: "t", goal: "g", repositories: [repo], model: "", effort: "high" })).toEqual(["effortModel"]);
});

/** 目标分支只来自项目详情 defaultBranch；读不到时不猜分支并保持禁用。 */
it("创建表单用项目默认分支作 targetRef，并手填完整基线 SHA", async () => {
	transport.get.mockImplementation(async (path: string) => path === "/api/v1/projects"
		? { data: { projects: [{ id: "p1", name: "仓库一" }] } }
		: path === "/api/v1/projects/{id}"
			? { data: { status: "ok", project: { id: "p1", defaultBranch: "develop" } } }
			: { data: { models: [{ id: "gpt-5", label: "GPT-5", efforts: ["low", "high"] }] } });
	transport.run.mockResolvedValue({ status: 200, body: { workflow: { id: "created" } } });
	render(<WorkflowCreate onCreated={vi.fn()} />, { wrapper });
	fireEvent.change(screen.getByRole("textbox", { name: "标题" }), { target: { value: "规划" } });
	fireEvent.change(screen.getByRole("textbox", { name: "目标" }), { target: { value: "实现功能" } });
	fireEvent.click(await screen.findByRole("checkbox", { name: "仓库一" }));
	expect(await screen.findByText("refs/heads/develop")).toBeVisible();
	const submit = screen.getByRole("button", { name: "创建规划任务" });
	expect(submit).toBeDisabled();
	expect(screen.getByText(/请先完成：.*基准提交 SHA/)).toBeVisible();
	fireEvent.change(screen.getByRole("textbox", { name: "基准提交 SHA" }), { target: { value: "b".repeat(7) } });
	expect(screen.getByText("基线 SHA 必须是完整的 40 或 64 位十六进制提交 ID。")).toBeVisible();
	expect(submit).toBeDisabled();
	fireEvent.change(screen.getByRole("textbox", { name: "基准提交 SHA" }), { target: { value: sha } });
	await waitFor(() => expect(submit).toBeEnabled());
	fireEvent.click(submit);
	await waitFor(() => expect(transport.run).toHaveBeenCalledOnce());
	expect(JSON.parse(transport.run.mock.calls[0][0].body)).toMatchObject({
		agentHarness: "codex",
		repositories: [{ projectId: "p1", repoKey: "p1", baseSha: sha, targetRef: "refs/heads/develop" }],
	});
});

/** 项目默认分支缺失时不能回退成 main，否则会静默提交到错误目标。 */
it("缺少 defaultBranch 时 targetRef 留空并阻止提交", async () => {
	transport.get.mockImplementation(async (path: string) => path === "/api/v1/projects"
		? { data: { projects: [{ id: "p1", name: "仓库一" }] } }
		: { data: { status: "ok", project: { id: "p1" } } });
	render(<WorkflowCreate onCreated={vi.fn()} />, { wrapper });
	fireEvent.change(screen.getByRole("textbox", { name: "标题" }), { target: { value: "规划" } });
	fireEvent.change(screen.getByRole("textbox", { name: "目标" }), { target: { value: "实现功能" } });
	fireEvent.click(await screen.findByRole("checkbox", { name: "仓库一" }));
	fireEvent.change(await screen.findByRole("textbox", { name: "基准提交 SHA" }), { target: { value: sha } });
	expect(await screen.findByText(/无法读取项目默认分支/)).toBeVisible();
	expect(screen.getByRole("button", { name: "创建规划任务" })).toBeDisabled();
	expect(transport.run).not.toHaveBeenCalled();
});

/** 提取必须在 attempt 成功后才可用；UNKNOWN 不能当作成功，规划会话入口始终可导航。 */
it("提取仅对 succeeded 启用，并保留查看规划会话入口", async () => {
	let state = "unknown";
	transport.get.mockImplementation(async (path: string) => path.includes("/tasks/")
		? { data: { task: { id: "plan" }, attempts: [{ id: "a1", state, sessionId: "s1" }] } }
		: { data: { candidates: [] } });
	transport.read.mockResolvedValue({ status: 200, body: { candidates: [] } });
	const definition = {
		workflow: { id: "w", state: "active", revision: 3 },
		tasks: [{ id: "plan", planner: true, title: "规划", state: "running", createdAt: "2026-01-01T00:00:00Z", model: "", agentHarness: "codex" }],
		repositories: [{ projectId: "p1", repoKey: "p1" }],
		taskRepositories: [{ taskId: "plan", repoKey: "p1" }],
	} as unknown as WorkflowDefinition;
	const view = render(<WorkflowPlanEditor definition={definition} />, { wrapper });
	expect(await screen.findByText("结果未知，不视为成功")).toBeVisible();
	expect(screen.getByRole("button", { name: "提取已完成的规划输出" })).toBeDisabled();
	expect(screen.getByText("UNKNOWN 不等于成功，attempt 报告成功前不能提取。")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "查看规划会话" }));
	expect(transport.navigate).toHaveBeenCalledWith("p1", "s1");
	state = "succeeded";
	view.unmount();
	render(<WorkflowPlanEditor definition={definition} />, { wrapper });
	await waitFor(() => expect(screen.getByText("已完成，可提取")).toBeVisible());
	expect(screen.getByRole("button", { name: "提取已完成的规划输出" })).toBeEnabled();
});

/** 轮询必须停在终态：失败或成功都不能无限刷新。 */
it("Planner 轮询在终态停止", async () => {
	transport.get.mockImplementation(async () => ({ data: { task: { id: "plan" }, attempts: [{ id: "a1", state: "failed" }] } }));
	const definition = {
		workflow: { id: "w", state: "active", revision: 3 },
		tasks: [{ id: "plan", planner: true, title: "规划", state: "running", createdAt: "2026-01-01T00:00:00Z" }],
	} as unknown as WorkflowDefinition;
	const { result } = renderHook(() => usePlannerAttempt(definition), { wrapper });
	await waitFor(() => expect(result.current.status).toBe("failed"));
	expect(result.current.query.isFetching).toBe(false);
	expect(result.current.attempt?.state).toBe("failed");
});

/** 步骤投影漏掉标签映射时，页面只显示 key 而不是可读步骤。 */
it("步骤条显示当前步骤名称与进度", () => {
	render(<WorkflowStartupSteps step="confirm" />);
	expect(screen.getByRole("status")).toBeVisible();
	expect(screen.getByText("确认任务图")).toBeVisible();
	expect(screen.getByText("5 / 6")).toBeVisible();
	expect(screen.queryByText("激活工作流")).toBeNull();
});
