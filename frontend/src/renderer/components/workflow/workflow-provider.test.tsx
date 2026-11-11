import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const transport = vi.hoisted(() => ({ get: vi.fn(), run: vi.fn() }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { operator: { runVerb: transport.run } } }));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: transport.get }, getApiBaseUrl: () => "http://127.0.0.1:5100", subscribeApiBaseUrl: () => () => undefined, apiErrorMessage: (e: { message?: string }) => e.message || "失败", apiErrorRequestId: () => undefined }));
vi.mock("react-i18next", async () => {
	const { createFullAppI18n } = await import("../../i18n/all-messages");
	const i18n = createFullAppI18n("zh-CN");
	return { useTranslation: () => ({ t: i18n.t.bind(i18n) }) };
});

import { WorkflowCreate } from "./WorkflowPlanEditor";
import { WorkflowProviderSelect } from "./WorkflowProviderSelect";
import { useWorkflowUi } from "../../stores/workflow-ui-store";
import { setEventsConnectionState } from "../../lib/events-connection";

const reference = `test@${"a".repeat(64)}`;
const profile = { reference, name: "测试供应商", harness: "codex", model: "provider-model", effort: "high" };

beforeEach(() => {
	vi.clearAllMocks();
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	setEventsConnectionState("connected");
	useWorkflowUi.setState({ requests: {}, drafts: {} });
	transport.get.mockImplementation(async (path: string) => ({ data: path.endsWith("provider-profiles") ? { profiles: [profile] }
		: path === "/api/v1/projects" ? { projects: [{ id: "project", name: "本地仓库" }] }
		: { status: "ok", project: { id: "project", defaultBranch: "main" } } }));
	transport.run.mockResolvedValue({ status: 200, body: { workflow: { id: "created" } } });
});
afterEach(cleanup);

/** 删除选择结果保存或清除分支，会丢失跨挂载草稿或继续提交旧供应商。 */
it("选择后保存固定引用与模型，重开保留草稿，清除恢复原生输入", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const onCreated = vi.fn();
	const component = <QueryClientProvider client={client}><WorkflowCreate onCreated={onCreated} /></QueryClientProvider>;
	const view = render(component);
	fireEvent.keyDown(screen.getByRole("combobox", { name: "供应商配置" }), { key: "ArrowDown" });
	fireEvent.click(await screen.findByRole("option", { name: "测试供应商 · provider-model" }));
	expect(screen.getByRole("textbox", { name: "模型" })).toHaveValue("provider-model");
	expect(screen.getByRole("textbox", { name: "模型" })).toBeDisabled();
	expect(screen.getByRole("textbox", { name: "推理强度" })).toHaveValue("high");
	view.unmount();
	render(component);
	expect(screen.getByText(/已固定供应商：test/)).toBeVisible();
	fireEvent.change(screen.getByRole("textbox", { name: "标题" }), { target: { value: "供应商规划" } });
	fireEvent.change(screen.getByRole("textbox", { name: "目标" }), { target: { value: "实现功能" } });
	fireEvent.click(await screen.findByRole("checkbox", { name: "本地仓库" }));
	fireEvent.change(screen.getByRole("textbox", { name: "基准提交 SHA" }), { target: { value: "b".repeat(40) } });
	fireEvent.click(screen.getByRole("button", { name: "创建规划任务" }));
	await waitFor(() => expect(onCreated).toHaveBeenCalledWith("created"));
	expect(JSON.parse(transport.run.mock.calls[0][0].body)).toMatchObject({ profile: reference, model: "provider-model", effort: "high" });
	fireEvent.keyDown(screen.getByRole("combobox", { name: "供应商配置" }), { key: "ArrowDown" });
	fireEvent.click(await screen.findByRole("option", { name: "原生配置" }));
	expect(screen.getByRole("textbox", { name: "模型" })).toBeEnabled();
	expect(screen.getByRole("textbox", { name: "模型" })).toHaveValue("");
	fireEvent.change(screen.getByRole("textbox", { name: "模型" }), { target: { value: "native-model" } });
	fireEvent.click(screen.getByRole("button", { name: "创建规划任务" }));
	await waitFor(() => expect(transport.run).toHaveBeenCalledTimes(2));
	expect(JSON.parse(transport.run.mock.calls[1][0].body)).toMatchObject({ profile: "", model: "native-model", effort: "" });
});

/** 删除目录失效/错误反馈会把已失效引用伪装成可用；读取失败不能自动清空选择。 */
it("目录漂移、跨 Agent 和读取失败显示错误且不自动改变固定选择", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const change = vi.fn();
	const view = render(<QueryClientProvider client={client}><WorkflowProviderSelect value={reference} harness="claude-code" onChange={change} /></QueryClientProvider>);
	expect(await screen.findByRole("alert")).toHaveTextContent("配置已变化或与 Agent 不匹配");
	transport.get.mockResolvedValue({ error: { message: "宿主目录不可用" } });
	view.rerender(<QueryClientProvider client={client}><WorkflowProviderSelect value={reference} harness="codex" onChange={change} /></QueryClientProvider>);
	await client.invalidateQueries();
	expect(await screen.findByText("宿主目录不可用")).toBeVisible();
	expect(screen.getByText(/已固定供应商：test/)).toBeVisible();
	expect(change).not.toHaveBeenCalled();
	expect(transport.run).not.toHaveBeenCalled();
});
