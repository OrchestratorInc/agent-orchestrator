import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { agentModelsQueryKey, agentModelsQueryOptions, useAgentModels, type AgentModelCatalog } from "./useAgentModelsQuery";
import { useConversationModelCatalog } from "./useConversation";

const { get, post, remoteGet } = vi.hoisted(() => ({
	get: vi.fn(),
	post: vi.fn(),
	remoteGet: vi.fn(),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: get, POST: post },
	apiErrorMessage: () => "refresh failed",
}));
vi.mock("../lib/host-clients", () => ({
	clientForHost: () => ({ GET: remoteGet, POST: post }),
}));
function catalog(agentId: string, overrides: Partial<AgentModelCatalog> = {}): AgentModelCatalog {
	return {
		agentId,
		models: [{ id: `${agentId}-model`, label: agentId, isDefault: true }],
		selectionMode: "catalog",
		allowCustom: false,
		customModelEntry: "none",
		source: "cli",
		fetchedAt: "2026-10-07T00:00:00Z",
		stale: false,
		...overrides,
	};
}
function setup() {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, wrapper };
}
beforeEach(() => {
	vi.clearAllMocks();
});

it("shows warmed providers synchronously on switch and remount", async () => {
	const { client, wrapper } = setup();
	get.mockImplementation(async (_path, options) => ({
		data: catalog(options.params.path.agent),
	}));
	await client.prefetchQuery(agentModelsQueryOptions("claude-code", "project"));
	await client.prefetchQuery(agentModelsQueryOptions("codex", "project"));
	const first = renderHook(({ agent }) => useAgentModels(agent, "project"), {
		wrapper,
		initialProps: { agent: "claude-code" },
	});
	expect(first.result.current.data?.agentId).toBe("claude-code");
	expect(first.result.current.isLoading).toBe(false);
	first.rerender({ agent: "codex" });
	expect(first.result.current.data?.agentId).toBe("codex");
	expect(first.result.current.isLoading).toBe(false);
	first.unmount();
	const second = renderHook(() => useAgentModels("claude-code", "project"), {
		wrapper,
	});
	expect(second.result.current.data?.models[0].id).toBe("claude-code-model");
	expect(second.result.current.isLoading).toBe(false);
	expect(get).toHaveBeenCalledTimes(2);
	expect(client.getQueryCache().find({ queryKey: agentModelsQueryKey("claude-code", "project") })?.gcTime).toBe(Infinity);
});

it("deduplicates validation and keeps choices after refresh failure", async () => {
	const { client, wrapper } = setup();
	client.setQueryData(agentModelsQueryKey("codex", "p"), catalog("codex", { refreshRecommended: true }));
	post.mockResolvedValue({ error: { message: "no connection" } });
	const first = renderHook(() => useAgentModels("codex", "p"), { wrapper });
	const second = renderHook(() => useAgentModels("codex", "p"), { wrapper });
	await waitFor(() => expect(first.result.current.warning).toBe("refresh failed"));
	expect(second.result.current.data?.models[0].id).toBe("codex-model");
	expect(post).toHaveBeenCalledTimes(1);
	await act(async () => {
		await expect(first.result.current.refresh()).rejects.toThrow("refresh failed");
	});
	expect(first.result.current.data?.models[0].id).toBe("codex-model");
});

it("isolates exact host and project scopes", () => {
	const { client, wrapper } = setup();
	client.setQueryData(agentModelsQueryKey("codex", "p"), catalog("codex"));
	remoteGet.mockImplementation(() => new Promise(() => {}));
	get.mockImplementation(() => new Promise(() => {}));
	const remote = renderHook(() => useAgentModels("codex", "p", "remote"), {
		wrapper,
	});
	const project = renderHook(() => useAgentModels("codex", "other"), {
		wrapper,
	});
	expect(remote.result.current.data).toBeUndefined();
	expect(project.result.current.data).toBeUndefined();
	expect(remote.result.current.isLoading).toBe(true);
	remote.unmount();
	project.unmount();
});

it("cancels an older catalog read before manual refresh", async () => {
	const { client, wrapper } = setup();
	const key = agentModelsQueryKey("codex", "p");
	client.setQueryData(key, catalog("codex"));
	let finishRead!: (value: { data: AgentModelCatalog }) => void;
	get.mockImplementation(() => new Promise((resolve) => { finishRead = resolve; }));
	post.mockResolvedValue({ data: catalog("codex", { models: [{ id: "new", label: "New" }] }) });
	const hook = renderHook(() => useAgentModels("codex", "p"), { wrapper });
	void client.invalidateQueries({ queryKey: key });
	await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
	const signal = get.mock.calls[0][1].signal as AbortSignal;
	await act(async () => {
		await hook.result.current.refresh();
		finishRead({ data: catalog("codex") });
	});
	expect(signal.aborted).toBe(true);
	expect(client.getQueryData<AgentModelCatalog>(key)?.models[0].id).toBe("new");
});

it("shares admitted chat choices without storing thread defaults", () => {
	const { client, wrapper } = setup();
	const canonical = catalog("codex", {
		models: [
			{ id: "one", label: "One", isDefault: true },
			{ id: "two", label: "Two", isDefault: false },
		],
	});
	client.setQueryData(agentModelsQueryKey("codex", "p"), canonical);
	const response = {
		modelCatalog: canonical,
		modelCatalogProjectId: "p",
		selected: {},
		models: [
			{ id: "one", displayName: "One", default: false },
			{ id: "two", displayName: "Two", default: true, defaultEffort: "high" },
		],
	};
	const chat = renderHook(() => useConversationModelCatalog(response, true), {
		wrapper,
	});
	expect(chat.result.current.find((model) => model.default)?.id).toBe("two");
	expect(chat.result.current[1].defaultEffort).toBe("high");
	expect(client.getQueryData<AgentModelCatalog>(agentModelsQueryKey("codex", "p"))?.models[0].isDefault).toBe(true);
	expect(get).not.toHaveBeenCalled();
});

it("replaces older shared choices with a newer admitted owner catalog", () => {
	const { client, wrapper } = setup();
	client.setQueryData(agentModelsQueryKey("codex", "p"), catalog("codex", { fetchedAt: "2026-10-06T00:00:00Z" }));
	const admitted = catalog("codex", { models: [{ id: "new-account-model", label: "New" }] });
	const chat = renderHook(
		() =>
			useConversationModelCatalog(
				{
					modelCatalog: admitted,
					modelCatalogProjectId: "p",
					selected: {},
					models: [{ id: "new-account-model", displayName: "New", default: true }],
				},
				true,
			),
		{ wrapper },
	);
	expect(chat.result.current[0].id).toBe("new-account-model");
	expect(client.getQueryData<AgentModelCatalog>(agentModelsQueryKey("codex", "p"))?.models[0].id).toBe("new-account-model");
});
