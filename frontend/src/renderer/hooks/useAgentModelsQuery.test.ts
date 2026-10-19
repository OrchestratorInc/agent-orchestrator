import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../lib/api-client";
import { cacheAgentModels, readCachedAgentModels } from "../lib/agent-model-cache";
import { agentModelsQueryOptions, invalidateAgentModelCatalogs, modelCatalogAuthIssue, type AgentModelCatalog } from "./useAgentModelsQuery";

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: vi.fn() },
	apiErrorMessage: () => "Model request failed",
}));

beforeEach(() => window.localStorage.clear());
afterEach(() => {
	vi.restoreAllMocks();
	vi.resetAllMocks();
	window.localStorage.clear();
});

describe("cached model loading", () => {
	const catalog: AgentModelCatalog = {
		agentId: "codex", models: [{ id: "model-one", label: "Model one" }],
		allowCustom: false, customModelEntry: "none", selectionMode: "catalog",
		source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z",
	};

	it("shows the DB catalog while the task response is still pending, then updates recency", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let finish!: (value: { data: AgentModelCatalog }) => void;
		const taskResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finish = resolve; });
		vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: catalog } as never).mockReturnValueOnce(taskResponse as never);
		const observer = new QueryObserver(client, agentModelsQueryOptions("codex", "project-one"));
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(observer.getCurrentResult().data).toEqual(catalog));
			expect(observer.getCurrentResult().isFetching).toBe(true);
			expect(observer.getCurrentResult().isPending).toBe(false);
			expect(apiClient.GET).toHaveBeenNthCalledWith(1, "/api/v1/agents/{agent}/models", {
				params: { path: { agent: "codex" }, query: { projectId: "project-one", catalogOnly: true } },
			});
			const enriched = { ...catalog, models: [{ ...catalog.models[0], lastUsedAt: "2026-10-18T01:00:00Z" }] };
			finish({ data: enriched });
			await vi.waitFor(() => expect(observer.getCurrentResult().data).toEqual(enriched));
		} finally {
			finish({ data: catalog });
			unsubscribe();
			client.clear();
		}
	});

	it("shows Settings' catalog for the same scope while the DB read fails", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(agentModelsQueryOptions("codex", "", undefined, true).queryKey, catalog);
		vi.mocked(apiClient.GET).mockRejectedValueOnce(new Error("daemon unavailable"));
		const options = agentModelsQueryOptions("codex", "");
		await expect(client.fetchQuery({ ...options, retry: false })).rejects.toThrow("daemon unavailable");
		expect(client.getQueryData(options.queryKey)).toEqual(catalog);
		expect(apiClient.GET).toHaveBeenCalledTimes(1);
		client.clear();
	});

	it("restores models synchronously after a frontend restart, then applies the DB update before recency", async () => {
		cacheAgentModels({ ...catalog, refreshRecommended: true, refreshState: "refreshing" }, "codex", "project-one");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let finishDB!: (value: { data: AgentModelCatalog }) => void;
		let finishRecency!: (value: { data: AgentModelCatalog }) => void;
		const dbResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishDB = resolve; });
		const recencyResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishRecency = resolve; });
		vi.mocked(apiClient.GET).mockReturnValueOnce(dbResponse as never).mockReturnValueOnce(recencyResponse as never);
		const observer = new QueryObserver(client, agentModelsQueryOptions("codex", "project-one"));
		expect(observer.getCurrentResult().data?.models).toEqual(catalog.models);
		expect(observer.getCurrentResult().data?.refreshRecommended).toBe(false);
		const unsubscribe = observer.subscribe(() => {});
		const updated = { ...catalog, models: [...catalog.models, { id: "model-two", label: "Model two" }] };
		try {
			expect(observer.getCurrentResult().isPending).toBe(false);
			finishDB({ data: updated });
			await vi.waitFor(() => expect(observer.getCurrentResult().data?.models).toEqual(updated.models));
			expect(observer.getCurrentResult().isFetching).toBe(true);
			expect(readCachedAgentModels("codex", "project-one")?.models).toEqual(updated.models);
			finishRecency({ data: updated });
			await vi.waitFor(() => expect(observer.getCurrentResult().isFetching).toBe(false));
		} finally {
			finishDB({ data: updated });
			finishRecency({ data: updated });
			unsubscribe();
			client.clear();
		}
	});

	it("keeps the saved frontend catalog when its background DB read fails", async () => {
		cacheAgentModels(catalog, "codex", "project-one");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		vi.mocked(apiClient.GET).mockRejectedValueOnce(new Error("DB unavailable"));
		const options = agentModelsQueryOptions("codex", "project-one");
		await expect(client.fetchQuery(options)).rejects.toThrow("DB unavailable");
		expect(client.getQueryData<AgentModelCatalog>(options.queryKey)?.models).toEqual(catalog.models);
		client.clear();
	});

	it("reuses fresh in-memory models on remount and revalidates after ten minutes", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		const options = agentModelsQueryOptions("codex", "project-one");
		await client.fetchQuery(options);
		expect(apiClient.GET).toHaveBeenCalledTimes(2);
		const freshObserver = new QueryObserver(client, options);
		const unsubscribeFresh = freshObserver.subscribe(() => {});
		try {
			expect(freshObserver.getCurrentResult().data).toEqual(catalog);
			expect(freshObserver.getCurrentResult().isFetching).toBe(false);
			expect(apiClient.GET).toHaveBeenCalledTimes(2);
		} finally {
			unsubscribeFresh();
		}
		client.setQueryData(options.queryKey, catalog, { updatedAt: Date.now() - 10 * 60 * 1_000 - 1 });
		const staleObserver = new QueryObserver(client, options);
		const unsubscribeStale = staleObserver.subscribe(() => {});
		try {
			expect(staleObserver.getCurrentResult().data).toEqual(catalog);
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(4));
			await vi.waitFor(() => expect(staleObserver.getCurrentResult().isFetching).toBe(false));
		} finally {
			unsubscribeStale();
			client.clear();
		}
	});

	it("does not let a slow DB read overwrite a manual refresh in memory or storage", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let finishDB!: (value: { data: AgentModelCatalog }) => void;
		const dbResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishDB = resolve; });
		vi.mocked(apiClient.GET).mockReturnValueOnce(dbResponse as never);
		const options = agentModelsQueryOptions("codex", "project-one");
		const pending = client.fetchQuery(options);
		await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
		const fresh = { ...catalog, models: [{ id: "new-model", label: "New model" }] };
		client.setQueryData(options.queryKey, fresh);
		cacheAgentModels(fresh, "codex", "project-one");
		finishDB({ data: catalog });
		expect(await pending).toEqual(fresh);
		expect(client.getQueryData(options.queryKey)).toEqual(fresh);
		expect(readCachedAgentModels("codex", "project-one")?.models).toEqual(fresh.models);
		expect(apiClient.GET).toHaveBeenCalledTimes(1);
		client.clear();
	});

	it("does not let a slow recency response overwrite a newer background catalog", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let finish!: (value: { data: AgentModelCatalog }) => void;
		const recency = new Promise<{ data: AgentModelCatalog }>((resolve) => { finish = resolve; });
		vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: catalog } as never).mockReturnValueOnce(recency as never);
		const options = agentModelsQueryOptions("codex", "project-one");
		const pending = client.fetchQuery(options);
		await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));
		const fresh = { ...catalog, models: [{ id: "new-model", label: "New model" }] };
		client.setQueryData(options.queryKey, fresh);
		finish({ data: catalog });
		expect(await pending).toEqual(fresh);
		expect(readCachedAgentModels("codex", "project-one")?.models).toEqual(fresh.models);
		client.clear();
	});

	it("preserves recency for surviving models while refreshing DB facts, even if the history read fails", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions("codex", "project-one");
		const lastUsedAt = "2026-10-18T01:00:00Z";
		client.setQueryData(options.queryKey, { ...catalog, models: [
			{ ...catalog.models[0], lastUsedAt }, { id: "removed-model", label: "Removed model", lastUsedAt },
		] });
		await client.invalidateQueries({ queryKey: options.queryKey });
		const dbCatalog = { ...catalog, models: [
			{ ...catalog.models[0], label: "Renamed model" }, { id: "new-default", label: "New default", isDefault: true },
		] };
		let failHistory!: (error: Error) => void;
		const history = new Promise<never>((_resolve, reject) => { failHistory = reject; });
		vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: dbCatalog } as never).mockReturnValueOnce(history as never);
		const observer = new QueryObserver(client, options);
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));
			const expectedModels = [{ ...dbCatalog.models[0], lastUsedAt }, dbCatalog.models[1]];
			expect(observer.getCurrentResult().data?.models).toEqual(expectedModels);
			expect(readCachedAgentModels("codex", "project-one")?.models).toEqual(expectedModels);
			failHistory(new Error("history unavailable"));
			await vi.waitFor(() => expect(observer.getCurrentResult().isError).toBe(true));
			expect(observer.getCurrentResult().data?.models).toEqual(expectedModels);
		} finally {
			failHistory(new Error("history unavailable"));
			unsubscribe();
			client.clear();
		}
	});

	it("keeps saved recency when Settings refreshes the same DB catalog", async () => {
		const lastUsedAt = "2026-10-18T01:00:00Z";
		cacheAgentModels({ ...catalog, models: [{ ...catalog.models[0], lastUsedAt }] }, "codex", "");
		vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: catalog } as never);
		const client = new QueryClient();
		await client.fetchQuery(agentModelsQueryOptions("codex", "", undefined, true));
		expect(readCachedAgentModels("codex", "")?.models[0].lastUsedAt).toBe(lastUsedAt);
		client.clear();
	});

	it("ignores corrupt saved data and still reads models from the DB", async () => {
		cacheAgentModels(catalog, "codex", "project-one");
		window.localStorage.setItem(window.localStorage.key(0)!, "not JSON");
		expect(readCachedAgentModels("codex", "project-one")).toBeUndefined();
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		const client = new QueryClient();
		expect((await client.fetchQuery(agentModelsQueryOptions("codex", "project-one"))).models).toEqual(catalog.models);
		client.clear();
	});

	it("continues using the DB when browser storage cannot be written", async () => {
		vi.spyOn(window.localStorage, "setItem").mockImplementation(() => { throw new Error("quota"); });
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		const client = new QueryClient();
		expect((await client.fetchQuery(agentModelsQueryOptions("codex", "project-one"))).models).toEqual(catalog.models);
		client.clear();
	});

	it("bounds saved snapshots by write recency without removing other renderer state", () => {
		window.localStorage.setItem("another-feature", "keep");
		for (let project = 0; project < 10; project++) cacheAgentModels(catalog, "codex", `project-${project}`);
		cacheAgentModels(catalog, "codex", "project-0");
		cacheAgentModels(catalog, "codex", "project-10");
		cacheAgentModels(catalog, "codex", "project-11");
		expect(window.localStorage.length).toBe(11);
		expect(readCachedAgentModels("codex", "project-0")?.models).toEqual(catalog.models);
		expect(readCachedAgentModels("codex", "project-1")).toBeUndefined();
		expect(readCachedAgentModels("codex", "project-2")).toBeUndefined();
		expect(readCachedAgentModels("codex", "project-11")?.models).toEqual(catalog.models);
		expect(window.localStorage.getItem("another-feature")).toBe("keep");
	});

	it("bounds the total snapshot size and skips catalogs larger than the cache budget", () => {
		const large = { ...catalog, models: [{ id: "large", label: "x".repeat(300_000) }] };
		cacheAgentModels(large, "codex", "first");
		cacheAgentModels(large, "codex", "second");
		expect(readCachedAgentModels("codex", "first")).toBeUndefined();
		expect(readCachedAgentModels("codex", "second")?.models).toEqual(large.models);
		cacheAgentModels({ ...large, models: [{ id: "huge", label: "x".repeat(600_000) }] }, "codex", "second");
		expect(readCachedAgentModels("codex", "second")).toBeUndefined();
	});

	it.each(["catalog", "history"])("ignores a cancelled pre-login %s response after login revalidation", async (phase) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const beforeLogin = { ...catalog, warning: "Not signed in", warningCode: "auth_required" as const };
		const afterLogin = { ...catalog, models: [{ id: "signed-in-model", label: "Signed in model" }] };
		cacheAgentModels(beforeLogin, "codex", "project-one");
		let finishOld!: (value: { data: AgentModelCatalog }) => void;
		const oldResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishOld = resolve; });
		if (phase === "history") vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: beforeLogin } as never);
		vi.mocked(apiClient.GET).mockReturnValueOnce(oldResponse as never).mockResolvedValue({ data: afterLogin } as never);
		const options = agentModelsQueryOptions("codex", "project-one");
		const observer = new QueryObserver(client, options);
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(phase === "catalog" ? 1 : 2));
			await invalidateAgentModelCatalogs(client, "codex");
			expect(observer.getCurrentResult().data).toEqual(afterLogin);
			finishOld({ data: beforeLogin });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(observer.getCurrentResult().data).toEqual(afterLogin);
			expect(readCachedAgentModels("codex", "project-one")?.models).toEqual(afterLogin.models);
			expect(readCachedAgentModels("codex", "project-one")?.warning).toBeUndefined();
			expect(apiClient.GET).toHaveBeenCalledTimes(phase === "catalog" ? 3 : 4);
		} finally {
			finishOld({ data: beforeLogin });
			unsubscribe();
			client.clear();
		}
	});

	it("clears all saved project catalogs for the changed login without touching another host", async () => {
		cacheAgentModels(catalog, "codex", "project-one");
		cacheAgentModels(catalog, "codex", "project-two");
		cacheAgentModels(catalog, "codex", "project-one", "box-a");
		const client = new QueryClient();
		await invalidateAgentModelCatalogs(client, "codex");
		expect(readCachedAgentModels("codex", "project-one")).toBeUndefined();
		expect(readCachedAgentModels("codex", "project-two")).toBeUndefined();
		expect(readCachedAgentModels("codex", "project-one", "box-a")?.models).toEqual(catalog.models);
		client.clear();
	});

	it("keeps a stale Settings catalog visible when its background read fails", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const settingsOptions = agentModelsQueryOptions("codex", "", undefined, true);
		client.setQueryData(settingsOptions.queryKey, catalog);
		await client.invalidateQueries({ queryKey: settingsOptions.queryKey });
		vi.mocked(apiClient.GET).mockRejectedValueOnce(new Error("daemon unavailable"));
		const options = agentModelsQueryOptions("codex", "");
		await expect(client.fetchQuery(options)).rejects.toThrow("daemon unavailable");
		expect(client.getQueryData(options.queryKey)).toEqual(catalog);
		client.clear();
	});

	it.each([
		{ agent: "codex", project: "another-project", host: undefined },
		{ agent: "codex", project: "project-one", host: "box-a" },
		{ agent: "claude-code", project: "project-one", host: undefined },
	])("does not borrow a catalog from a different scope: %j", async ({ agent, project, host }) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		cacheAgentModels({ ...catalog, agentId: agent }, agent, project, host);
		client.setQueryData(agentModelsQueryOptions(agent, project, host, true).queryKey, catalog);
		vi.mocked(apiClient.GET).mockRejectedValueOnce(new Error("DB read failed"));
		const options = agentModelsQueryOptions("codex", "project-one");
		await expect(client.fetchQuery({ ...options, retry: false })).rejects.toThrow("DB read failed");
		expect(client.getQueryData(options.queryKey)).toBeUndefined();
		client.clear();
	});
});

describe("modelCatalogAuthIssue", () => {
	it("reads the daemon's warning code", () => {
		expect(modelCatalogAuthIssue({ warning: "x", warningCode: "auth_required" })).toBe("required");
		expect(modelCatalogAuthIssue({ warning: "x", warningCode: "auth_expired" })).toBe("expired");
	});

	it("recognizes login wording from daemons that predate warning codes", () => {
		expect(modelCatalogAuthIssue({ warning: "claude-code: model discovery: Anthropic rejected the credential: OAuth access token has expired. Re-authenticate to continue." })).toBe("required");
		expect(modelCatalogAuthIssue({ warning: "Kiro is not signed in; sign in to load its models" })).toBe("required");
	});

	it("leaves other warnings alone", () => {
		expect(modelCatalogAuthIssue({ warning: "codex model discovery returned no models" })).toBeUndefined();
		expect(modelCatalogAuthIssue({ warning: "Models loaded, but AO could not update the model cache." })).toBeUndefined();
		expect(modelCatalogAuthIssue(undefined)).toBeUndefined();
	});
});

describe("invalidateAgentModelCatalogs", () => {
	function seeded() {
		const client = new QueryClient();
		const keys = {
			local: ["agent-models", "claude-code", ""],
			localProject: ["agent-models", "claude-code", "proj-1"],
			otherAgent: ["agent-models", "codex", ""],
			remote: ["agent-models", "box-a", "claude-code", ""],
			pickerRevalidation: ["agent-model-revalidation", "claude-code", "", "t1"],
			composerRevalidation: ["agent-model-revalidation", "", "claude-code", "", "t1"],
			settingsRevalidation: ["agent-model-revalidation", "local", "claude-code", "", "t1"],
			remoteRevalidation: ["agent-model-revalidation", "box-a", "claude-code", "", "t1"],
			otherRevalidation: ["agent-model-revalidation", "", "codex", "", "t1"],
		} as const;
		for (const key of Object.values(keys)) client.setQueryData(key, { models: [] });
		const invalidated = (key: readonly unknown[]) => client.getQueryState(key)?.isInvalidated === true;
		return { client, keys, invalidated };
	}

	it("drops one agent's local catalogs and revalidations only", async () => {
		const { client, keys, invalidated } = seeded();
		await invalidateAgentModelCatalogs(client, "claude-code");
		expect([keys.local, keys.localProject, keys.pickerRevalidation, keys.composerRevalidation, keys.settingsRevalidation].every(invalidated)).toBe(true);
		expect([keys.otherAgent, keys.remote, keys.remoteRevalidation, keys.otherRevalidation].some(invalidated)).toBe(false);
	});

	it("scopes a remote host's invalidation to that host", async () => {
		const { client, keys, invalidated } = seeded();
		await invalidateAgentModelCatalogs(client, "claude-code", "box-a");
		expect(invalidated(keys.remote) && invalidated(keys.remoteRevalidation)).toBe(true);
		expect([keys.local, keys.composerRevalidation, keys.otherRevalidation].some(invalidated)).toBe(false);
	});
});
