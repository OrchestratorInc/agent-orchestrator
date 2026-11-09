import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { agentModelsQueryKey } from "../hooks/useAgentModelsQuery";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { AgentModelPicker } from "./AgentModelPicker";
import { ReviewerSelect } from "./ReviewerSelect";
import { AgentModelField } from "./settings/ProjectAgentRoleControls";

const models = [
	{ id: "runnable", label: "Runnable", provider: "connected" },
	{ id: "unavailable", label: "Unavailable", provider: "connected" },
	{ id: "disabled", label: "Disabled", provider: "connected" },
	{ id: "saved", label: "Saved", provider: "other" },
];

/** 同一供应商的可运行列表也可能只含部分模型，已保存项仍须保留。 */
function catalogClient(hostId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
	client.setQueryData(agentModelsQueryKey("opencode", "", hostId), {
		agent: "opencode", selectionMode: "catalog", models,
		configuredProviders: ["connected"], runnableModels: ["runnable", "disabled"],
	});
	client.setQueryData(hostId ? ["settings", hostId] : ["settings"], {
		harnessDefaults: { opencode: { disabledModels: ["disabled", "saved"] } },
	});
	return client;
}

function wrap(client: QueryClient, children: ReactNode) {
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/** 检查实际菜单，删除调用者传递 runnableModels 的代码便会暴露 Unavailable。 */
function expectLocalModels() {
	expect(screen.getByRole("menuitem", { name: "Runnable" })).toBeInTheDocument();
	expect(screen.getByRole("menuitem", { name: "Saved" })).toBeInTheDocument();
	expect(screen.queryByRole("menuitem", { name: "Unavailable" })).not.toBeInTheDocument();
	expect(screen.queryByRole("menuitem", { name: "Disabled" })).not.toBeInTheDocument();
}

describe("本地模型选择器的可运行目录", () => {
	beforeEach(() => window.localStorage.clear());
	afterEach(cleanup);

	it("任务模型选择器按目标主机目录过滤并保留已选项", async () => {
		const onModelChange = vi.fn();
		render(wrap(catalogClient("box-a"), <AgentModelPicker
			agentId="opencode" agentLabel="OpenCode" projectId="project-1" hostId="box-a"
			value="saved" mode="" onModelChange={onModelChange} onModeChange={vi.fn()} onWarningChange={vi.fn()}
		/>));
		await userEvent.click(screen.getByRole("button", { name: "Model" }));
		expectLocalModels();
		await userEvent.click(screen.getByRole("menuitem", { name: "Runnable" }));
		expect(onModelChange).toHaveBeenCalledWith("runnable");
	});

	it("项目角色过滤本地目录，云端角色不受本机模型限制", async () => {
		const client = catalogClient();
		const props = {
			role: "worker" as const, agentId: "opencode", projectId: "project-1", model: "saved", mode: "", effort: "",
			onModelChange: vi.fn(), onModeChange: vi.fn(), onEffortChange: vi.fn(), onValidityChange: vi.fn(),
		};
		const view = render(wrap(client, <AgentModelField {...props} />));
		await userEvent.click(screen.getByRole("button", { name: "Worker model" }));
		expectLocalModels();
		await userEvent.keyboard("{Escape}");
		view.rerender(wrap(client, <AgentModelField {...props} followCatalogDefaults={false} />));
		await userEvent.click(screen.getByRole("button", { name: "Worker model" }));
		expect(screen.getByRole("menuitem", { name: "Unavailable" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Disabled" })).toBeInTheDocument();
	});

	it("审查模型子菜单使用可运行目录并保留当前审查模型", async () => {
		const onConfigChange = vi.fn();
		render(wrap(catalogClient(), <ReviewerSelect
			ariaLabel="Reviewer" value="opencode" model="saved" defaultHarness="opencode"
			agents={[agentReadiness("opencode", "OpenCode")]}
			onChange={vi.fn()} onConfigChange={onConfigChange}
		/>));
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "OpenCode" }));
		expectLocalModels();
		await userEvent.click(screen.getByRole("menuitem", { name: "Runnable" }));
		expect(onConfigChange).toHaveBeenCalledWith("", { model: "runnable" });
	});
});
