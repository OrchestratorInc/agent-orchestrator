import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { IpcMainInvokeEvent } from "electron";
import type { components } from "../../../api/schema";
import { registerOperatorIpc } from "../../../main/operator-ipc";

const transport = vi.hoisted(() => ({ base: "http://127.0.0.1:5100", get: vi.fn(), run: vi.fn(), listeners: new Set<() => void>() }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { operator: { runVerb: transport.run } } }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: transport.get }, getApiBaseUrl: () => transport.base,
	subscribeApiBaseUrl: (listener: () => void) => { transport.listeners.add(listener); return () => transport.listeners.delete(listener); },
	apiErrorMessage: (error: { message?: string }) => error.message || "失败", apiErrorRequestId: () => undefined,
}));
vi.mock("react-i18next", async () => {
	const { createFullAppI18n } = await import("../../i18n/all-messages");
	const i18n = createFullAppI18n("zh-CN");
	return { useTranslation: () => ({ t: i18n.t.bind(i18n) }) };
});

import { SchedulerSettingsSection } from "./SchedulerSettingsSection";
import { useWorkflowUi } from "../../stores/workflow-ui-store";
import { setEventsConnectionState } from "../../lib/events-connection";

type S = components["schemas"];
let client: QueryClient;
let status: S["SchedulerStatusResponse"];
const save = () => screen.getByRole("button", { name: "审批并保存策略" });
const field = (name: string) => screen.getByRole("spinbutton", { name });
const edit = (name: string, value: string) => fireEvent.change(field(name), { target: { value } });

beforeEach(() => {
	vi.clearAllMocks();
	transport.base = "http://127.0.0.1:5100";
	Object.defineProperty(window, "ao", { configurable: true, value: {} });
	setEventsConnectionState("connected");
	useWorkflowUi.setState({ requests: {}, drafts: {} });
	client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const global: S["SchedulerPolicyResponse"] = { id: "global", scope: "global", scopeId: "", revision: 3, maxConcurrent: 5,
		resourceLimitsDisabled: false, cpuRecoverPercent: 65, cpuSoftPercent: 75, cpuHardPercent: 85,
		ramSoftPercent: 25, ramHardPercent: 15, ramFloorBytes: 2 * 2 ** 30 };
	status = { availableBytes: 4 * 2 ** 30, totalBytes: 16 * 2 ** 30, byHarness: {}, epoch: 1, leaderLeaseUntil: "2099-01-01T00:00:00Z",
		limit: 5, maxNewClaims: 1, observedAt: "2026-11-10T00:00:00Z", occupied: 2, reasonCode: "RESOURCE_SOFT_LIMIT", recovered: true,
		policies: [global, { ...global, id: "adapter:codex", scope: "adapter", scopeId: "codex", revision: 0, maxConcurrent: 2 }] };
	transport.get.mockImplementation(async () => ({ data: structuredClone(status) }));
});
afterEach(() => { cleanup(); client.clear(); });

/** 模拟服务端保存并递增 revision，让真实 Hook 失效查询后取得新快照。 */
function applyPolicy(raw: string) {
	const { harness, expectedRevision, idempotencyKey: _key, reason: _reason, ...values } = JSON.parse(raw);
	status = { ...status, policies: status.policies.map(policy => policy.scopeId === harness ? { ...policy, ...values, revision: expectedRevision + 1 } : policy) };
	return { policy: status.policies.find(policy => policy.scopeId === harness) };
}

function renderSettings() {
	return render(<QueryClientProvider client={client}><SchedulerSettingsSection /></QueryClientProvider>);
}

/** 选择真实 Select，验证未保存的 adapter 默认值也能直接编辑。 */
async function selectPolicy(name: string) {
	fireEvent.keyDown(screen.getByRole("combobox", { name: "策略范围" }), { key: "ArrowDown" });
	fireEvent.click(await screen.findByRole("option", { name }));
}

// 删除阈值校验、GiB 换算、开关入参或草稿快照隔离，真实编辑到 PUT 的行为会失败。
it("校验实际输入，保留刷新中的草稿，经真实 Operator IPC 保存开关与阈值并回显新版本", async () => {
	const handlers = new Map<string, (event: IpcMainInvokeEvent, input: unknown) => unknown>();
	const frame = { url: "app://renderer/index.html", parent: null };
	const sender = { mainFrame: frame, isDestroyed: () => false };
	const event = { sender, senderFrame: frame } as unknown as IpcMainInvokeEvent;
	const confirmOperation = vi.fn(async () => true);
	let approval: Record<string, unknown>;
	// 仅替换系统确认及 HTTP；组件、Hook、请求重放与 Main 审批绑定均为正式实现。
	const request = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
		if (String(url).endsWith("/operator/approvals")) {
			const created = JSON.parse(init!.body as string);
			approval = { approvalId: "approval-1", code: "12345678", state: "pending", expiresAt: "2099-01-01T00:00:00Z",
				operation: { method: created.operationMethod, path: created.operationPath }, payloadDigest: created.payloadDigest };
			return Response.json({ approval }, { status: 201 });
		}
		if (String(url).endsWith("/confirm")) return Response.json({ approval: { ...approval, code: undefined, state: "confirmed" } });
		return Response.json(applyPolicy(init!.body as string));
	});
	registerOperatorIpc({ ipcMain: { handle: (channel, handler) => { handlers.set(channel, handler); } },
		baseURL: () => transport.base, trustedWebContents: () => sender as never, rendererURL: () => frame.url, confirmOperation, request });
	transport.run.mockImplementation(input => handlers.get("operator:runVerb")!(event, input));
	renderSettings();
	await screen.findByRole("spinbutton", { name: "并发上限" });
	expect(field("并发上限")).toHaveValue(5);
	expect(screen.getByText(/资源紧张，已降低并发/)).toBeInTheDocument();
	expect(screen.getByText("可用内存 4.00 GiB / 总内存 16.00 GiB")).toBeInTheDocument();
	expect(screen.getByRole("switch", { name: "CPU 与内存资源限制" })).toBeChecked();
	// 空值、非整数和阈值次序合并进一条用户编辑流程，不拆参数矩阵。
	edit("并发上限", ""); expect(save()).toBeDisabled();
	edit("并发上限", "3.5"); expect(save()).toBeDisabled();
	edit("并发上限", "10");
	edit("CPU 恢复阈值（%）", "75"); expect(save()).toBeDisabled();
	edit("CPU 恢复阈值（%）", "55");
	edit("CPU 软阈值（%）", "70");
	edit("CPU 硬阈值（%）", "90");
	edit("可用内存硬阈值（%）", "25"); expect(save()).toBeDisabled();
	edit("可用内存软阈值（%）", "30");
	edit("可用内存硬阈值（%）", "20");
	edit("内存绝对保留量（GiB）", ""); expect(save()).toBeDisabled();
	edit("内存绝对保留量（GiB）", "1.5");
	fireEvent.click(screen.getByRole("switch", { name: "CPU 与内存资源限制" }));
	await act(async () => { await client.invalidateQueries({ queryKey: ["workflows"] }); });
	expect(field("CPU 恢复阈值（%）")).toHaveValue(55);
	expect(field("内存绝对保留量（GiB）")).toHaveValue(1.5);
	expect(transport.run).not.toHaveBeenCalled();
	fireEvent.click(save());
	await screen.findByText("策略已保存并生效，无需重启。");
	const raw = transport.run.mock.calls[0][0].body;
	expect(JSON.parse(raw)).toEqual({ harness: "", expectedRevision: 3, maxConcurrent: 10, resourceLimitsDisabled: true,
		cpuRecoverPercent: 55, cpuSoftPercent: 70, cpuHardPercent: 90, ramSoftPercent: 30, ramHardPercent: 20,
		ramFloorBytes: 1610612736, reason: "SCHEDULER_POLICY_UPDATE", idempotencyKey: expect.any(String) });
	expect(confirmOperation).toHaveBeenCalledExactlyOnceWith({ verb: "scheduler-policy", method: "PUT", operationPath: "/api/v1/scheduler/policy", body: raw });
	expect(request.mock.calls[2]).toEqual([`${transport.base}/api/v1/scheduler/policy`, expect.objectContaining({ method: "PUT", body: raw })]);
	expect(transport.get.mock.calls.length).toBeGreaterThanOrEqual(3);
	expect(screen.getByRole("switch", { name: "CPU 与内存资源限制" })).not.toBeChecked();
	expect(field("并发上限")).toHaveValue(10);
	fireEvent.click(screen.getByRole("switch", { name: "CPU 与内存资源限制" }));
	fireEvent.click(save());
	await screen.findByText("策略已保存并生效，无需重启。");
	expect(JSON.parse(transport.run.mock.calls[1][0].body)).toMatchObject({ expectedRevision: 4, resourceLimitsDisabled: false, cpuRecoverPercent: 55, ramFloorBytes: 1610612736 });
});

// 删除 requestKey 或 daemon 草稿/查询隔离，将把失败的全局正文重放到 adapter 或另一 daemon。
it("默认 Harness 可保存且与失败的全局请求独立，切换 daemon 不复用旧草稿", async () => {
	transport.run.mockRejectedValueOnce(new Error("请求超时"));
	transport.run.mockImplementation(async input => ({ status: 200, body: applyPolicy(input.body) }));
	renderSettings();
	await screen.findByRole("spinbutton", { name: "并发上限" });
	edit("并发上限", "8");
	fireEvent.click(save());
	expect(await screen.findByRole("alert")).toHaveTextContent("请求超时");
	const original = transport.run.mock.calls[0][0];
	await selectPolicy("codex");
	expect(field("并发上限")).toHaveValue(2);
	expect(screen.queryByRole("switch")).not.toBeInTheDocument();
	expect(screen.queryByRole("spinbutton", { name: "CPU 恢复阈值（%）" })).not.toBeInTheDocument();
	edit("并发上限", "4");
	fireEvent.click(save());
	await screen.findByText("策略已保存并生效，无需重启。");
	const adapter = JSON.parse(transport.run.mock.calls[1][0].body);
	expect(adapter).toMatchObject({ harness: "codex", maxConcurrent: 4, expectedRevision: 0, resourceLimitsDisabled: false });
	expect(adapter.idempotencyKey).not.toBe(JSON.parse(original.body).idempotencyKey);
	await selectPolicy("全局");
	expect(field("并发上限")).toHaveValue(8);
	expect(screen.getByRole("button", { name: "重试原请求" })).toBeEnabled();
	await act(async () => { transport.base = "http://127.0.0.1:5200"; transport.listeners.forEach(listener => listener()); });
	await waitFor(() => expect(field("并发上限")).toHaveValue(5));
	expect(save()).toBeDisabled();
	expect(screen.queryByRole("button", { name: "重试原请求" })).not.toBeInTheDocument();
	expect(client.getQueryData(["workflows", transport.base, "scheduler-status"])).toBeDefined();
	await act(async () => { transport.base = "http://127.0.0.1:5100"; transport.listeners.forEach(listener => listener()); });
	await waitFor(() => expect(field("并发上限")).toHaveValue(8));
	fireEvent.click(screen.getByRole("button", { name: "重试原请求" }));
	await screen.findByText("策略已保存并生效，无需重启。");
	expect(transport.run.mock.calls[2][0]).toEqual(original);
});
