import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../lib/api-client";
import { cacheAgentModels, readCachedAgentModels } from "../lib/agent-model-cache";
import { agentModelsQueryOptions, discardPendingAgentModelReads, invalidateAgentModelCatalogs, modelCatalogAuthIssue, type AgentModelCatalog } from "./useAgentModelsQuery";

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
		const observer = new QueryObserver(client, agentModelsQueryOptions("codex", ""));
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(observer.getCurrentResult().data).toEqual(catalog));
			expect(observer.getCurrentResult().isFetching).toBe(true);
			expect(observer.getCurrentResult().isPending).toBe(false);
			expect(apiClient.GET).toHaveBeenNthCalledWith(1, "/api/v1/agents/{agent}/models", {
				params: { path: { agent: "codex" }, query: { projectId: undefined, catalogOnly: true } },
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
		cacheAgentModels({ ...catalog, refreshRecommended: true, refreshState: "refreshing" }, "codex", "");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let finishDB!: (value: { data: AgentModelCatalog }) => void;
		let finishRecency!: (value: { data: AgentModelCatalog }) => void;
		const dbResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishDB = resolve; });
		const recencyResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishRecency = resolve; });
		vi.mocked(apiClient.GET).mockReturnValueOnce(dbResponse as never).mockReturnValueOnce(recencyResponse as never);
		const observer = new QueryObserver(client, agentModelsQueryOptions("codex", ""));
		expect(observer.getCurrentResult().data?.models).toEqual(catalog.models);
		expect(observer.getCurrentResult().data?.refreshRecommended).toBe(false);
		const unsubscribe = observer.subscribe(() => {});
		const updated = { ...catalog, models: [...catalog.models, { id: "model-two", label: "Model two" }] };
		try {
			expect(observer.getCurrentResult().isPending).toBe(false);
			finishDB({ data: updated });
			await vi.waitFor(() => expect(observer.getCurrentResult().data?.models).toEqual(updated.models));
			expect(observer.getCurrentResult().isFetching).toBe(true);
			expect(readCachedAgentModels("codex", "")?.models).toEqual(updated.models);
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
		cacheAgentModels(catalog, "codex", "");
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		vi.mocked(apiClient.GET).mockRejectedValueOnce(new Error("DB unavailable"));
		const options = agentModelsQueryOptions("codex", "");
		await expect(client.fetchQuery(options)).rejects.toThrow("DB unavailable");
		expect(client.getQueryData<AgentModelCatalog>(options.queryKey)?.models).toEqual(catalog.models);
		client.clear();
	});

	it("reuses fresh in-memory models on remount and revalidates after ten minutes", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		const options = agentModelsQueryOptions("codex", "");
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
		const options = agentModelsQueryOptions("codex", "");
		const pending = client.fetchQuery(options);
		await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
		const fresh = { ...catalog, models: [{ id: "new-model", label: "New model" }] };
		client.setQueryData(options.queryKey, fresh);
		cacheAgentModels(fresh, "codex", "");
		finishDB({ data: catalog });
		expect(await pending).toEqual(fresh);
		expect(client.getQueryData(options.queryKey)).toEqual(fresh);
		expect(readCachedAgentModels("codex", "")?.models).toEqual(fresh.models);
		expect(apiClient.GET).toHaveBeenCalledTimes(1);
		client.clear();
	});

	it("does not let a slow recency response overwrite a newer background catalog", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		let finish!: (value: { data: AgentModelCatalog }) => void;
		const recency = new Promise<{ data: AgentModelCatalog }>((resolve) => { finish = resolve; });
		vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: catalog } as never).mockReturnValueOnce(recency as never);
		const options = agentModelsQueryOptions("codex", "");
		const pending = client.fetchQuery(options);
		await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));
		const fresh = { ...catalog, models: [{ id: "new-model", label: "New model" }] };
		client.setQueryData(options.queryKey, fresh);
		finish({ data: catalog });
		expect(await pending).toEqual(fresh);
		expect(readCachedAgentModels("codex", "")?.models).toEqual(fresh.models);
		client.clear();
	});

	it("preserves recency for surviving models while refreshing DB facts, even if the history read fails", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions("codex", "");
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
			expect(readCachedAgentModels("codex", "")?.models).toEqual(expectedModels);
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
		cacheAgentModels(catalog, "codex", "");
		window.localStorage.setItem(window.localStorage.key(0)!, "not JSON");
		expect(readCachedAgentModels("codex", "")).toBeUndefined();
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		const client = new QueryClient();
		expect((await client.fetchQuery(agentModelsQueryOptions("codex", ""))).models).toEqual(catalog.models);
		client.clear();
	});

	it("continues using the DB when browser storage cannot be written", async () => {
		vi.spyOn(window.localStorage, "setItem").mockImplementation(() => { throw new Error("quota"); });
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		const client = new QueryClient();
		expect((await client.fetchQuery(agentModelsQueryOptions("codex", ""))).models).toEqual(catalog.models);
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
		cacheAgentModels(beforeLogin, "codex", "");
		let finishOld!: (value: { data: AgentModelCatalog }) => void;
		const oldResponse = new Promise<{ data: AgentModelCatalog }>((resolve) => { finishOld = resolve; });
		if (phase === "history") vi.mocked(apiClient.GET).mockResolvedValueOnce({ data: beforeLogin } as never);
		vi.mocked(apiClient.GET).mockReturnValueOnce(oldResponse as never).mockResolvedValue({ data: afterLogin } as never);
		const options = agentModelsQueryOptions("codex", "");
		const observer = new QueryObserver(client, options);
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(phase === "catalog" ? 1 : 2));
			await invalidateAgentModelCatalogs(client, "codex");
			expect(observer.getCurrentResult().data).toEqual(afterLogin);
			finishOld({ data: beforeLogin });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(observer.getCurrentResult().data).toEqual(afterLogin);
			expect(readCachedAgentModels("codex", "")?.models).toEqual(afterLogin.models);
			expect(readCachedAgentModels("codex", "")?.warning).toBeUndefined();
			expect(apiClient.GET).toHaveBeenCalledTimes(phase === "catalog" ? 3 : 4);
		} finally {
			finishOld({ data: beforeLogin });
			unsubscribe();
			client.clear();
		}
	});

	it("clears all saved project catalogs for the changed login without touching another host", async () => {
		cacheAgentModels(catalog, "codex", "");
		cacheAgentModels(catalog, "codex", "project-two");
		cacheAgentModels(catalog, "codex", "", "box-a");
		const client = new QueryClient();
		await invalidateAgentModelCatalogs(client, "codex");
		expect(readCachedAgentModels("codex", "")).toBeUndefined();
		expect(readCachedAgentModels("codex", "project-two")).toBeUndefined();
		expect(readCachedAgentModels("codex", "", "box-a")?.models).toEqual(catalog.models);
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
		{ agent: "codex", project: "@cred:openai_api_key", host: undefined },
		{ agent: "codex", project: "", host: "box-a" },
		{ agent: "claude-code", project: "", host: undefined },
	])("does not borrow a catalog from a different scope: %j", async ({ agent, project, host }) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		cacheAgentModels({ ...catalog, agentId: agent }, agent, project, host);
		client.setQueryData(agentModelsQueryOptions(agent, project, host, true).queryKey, catalog);
		vi.mocked(apiClient.GET).mockRejectedValueOnce(new Error("DB read failed"));
		const options = agentModelsQueryOptions("codex", "");
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


describe("OpenCode single-read catalogs", () => {
	it("shows an already-loaded Settings catalog during its single OpenCode read", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const catalog: AgentModelCatalog = { agentId: "opencode", models: [{ id: "provider/model", label: "Model" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		client.setQueryData(agentModelsQueryOptions("opencode", "", undefined, true).queryKey, catalog);
		let finish!: (value: { data: AgentModelCatalog }) => void;
		vi.mocked(apiClient.GET).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }) as never);
		const observer = new QueryObserver(client, agentModelsQueryOptions("opencode", ""));
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(observer.getCurrentResult().data).toEqual(catalog));
			expect(apiClient.GET).toHaveBeenCalledTimes(1);
			finish({ data: catalog });
			await vi.waitFor(() => expect(observer.getCurrentResult().isFetching).toBe(false));
		} finally { finish({ data: catalog }); unsubscribe(); client.clear(); }
	});

	it.each(["opencode", "opencode-v2"])("reuses the pending %s device read after an observer remount", async (agentId) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions(agentId, "");
		const catalog: AgentModelCatalog = { agentId, models: [{ id: "provider/model", label: "Model" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		let finish!: (value: { data: AgentModelCatalog }) => void;
		vi.mocked(apiClient.GET).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }) as never);
		const first = new QueryObserver(client, options);
		const unsubscribe = first.subscribe(() => {});
		await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
		unsubscribe();
		const second = new QueryObserver(client, options);
		const unsubscribeSecond = second.subscribe(() => {});
		try {
			finish({ data: catalog });
			await vi.waitFor(() => expect(second.getCurrentResult().data).toEqual(catalog));
			expect(apiClient.GET).toHaveBeenCalledTimes(1);
		} finally { finish({ data: catalog }); unsubscribeSecond(); client.clear(); }
	});

	it.each(["opencode", "opencode-v2"])("discards a detached %s read on CDC invalidation before remount", async (agentId) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions(agentId, "");
		const old: AgentModelCatalog = { agentId, models: [{ id: "provider/old", label: "Old" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		const fresh = { ...old, models: [{ id: "provider/new", label: "New", efforts: ["low", "high"] }] };
		let finishOld!: (value: { data: AgentModelCatalog }) => void;
		let finishNew!: (value: { data: AgentModelCatalog }) => void;
		vi.mocked(apiClient.GET)
			.mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; }) as never)
			.mockReturnValueOnce(new Promise((resolve) => { finishNew = resolve; }) as never);
		const first = new QueryObserver(client, options);
		const unsubscribeFirst = first.subscribe(() => {});
		let unsubscribeSecond = () => {};
		let unsubscribeThird = () => {};
		try {
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
			unsubscribeFirst();
			expect(client.getQueryState(options.queryKey)?.fetchStatus).toBe("idle");
			// Match CDC's ordinary query invalidation, without the login helper
			// or any explicit pending-map clearing.
			await client.invalidateQueries({ queryKey: ["agent-models", agentId] }, { cancelRefetch: false });
			const second = new QueryObserver(client, options);
			unsubscribeSecond = second.subscribe(() => {});
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));
			finishOld({ data: old });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(client.getQueryData(options.queryKey)).toBeUndefined();
			expect(readCachedAgentModels(agentId, "")).toBeUndefined();
			// Settling the old GET must not remove the new pending entry.
			unsubscribeSecond();
			const third = new QueryObserver(client, options);
			unsubscribeThird = third.subscribe(() => {});
			finishNew({ data: fresh });
			await vi.waitFor(() => expect(third.getCurrentResult().data).toEqual(fresh));
			expect(readCachedAgentModels(agentId, "")?.models).toEqual(fresh.models);
			expect(apiClient.GET).toHaveBeenCalledTimes(2);
			expect(third.getCurrentResult().isStale).toBe(false);
		} finally {
			finishOld({ data: old });
			finishNew?.({ data: fresh });
			unsubscribeFirst(); unsubscribeSecond(); unsubscribeThird(); client.clear();
		}
	});

	// What event-transport does for every catalog event.
	const catalogEvent = (client: QueryClient, agentId: string) => {
		discardPendingAgentModelReads(["agent-models", agentId]);
		return client.invalidateQueries({ queryKey: ["agent-models", agentId] }, { cancelRefetch: false });
	};

	it.each(["opencode", "opencode-v2"])("discards detached %s reads across two inactive catalog event cycles", async (agentId) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions(agentId, "");
		const first: AgentModelCatalog = { agentId, models: [{ id: "provider/first", label: "First" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		const second = { ...first, models: [{ id: "provider/second", label: "Second" }] };
		const fresh = { ...first, models: [{ id: "provider/new", label: "New", efforts: ["low", "high"] }] };
		const finish: Array<(value: { data: AgentModelCatalog }) => void> = [];
		vi.mocked(apiClient.GET).mockImplementation((() => new Promise((resolve) => { finish.push(resolve); })) as never);
		const unsubscribes: Array<() => void> = [];
		const mount = () => {
			const observer = new QueryObserver(client, options);
			unsubscribes.push(observer.subscribe(() => {}));
			return observer;
		};
		try {
			mount();
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
			unsubscribes.pop()?.();
			await catalogEvent(client, agentId);
			mount();
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));
			unsubscribes.pop()?.();
			// TanStack now skips the next invalidate event for this query.
			expect(client.getQueryState(options.queryKey)?.isInvalidated).toBe(true);
			await catalogEvent(client, agentId);
			mount();
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(3));
			finish[0]({ data: first });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(client.getQueryData(options.queryKey)).toBeUndefined();
			expect(readCachedAgentModels(agentId, "")).toBeUndefined();
			// An older read's cleanup must keep the replacement shared.
			unsubscribes.pop()?.();
			const last = mount();
			finish[2]({ data: fresh });
			await vi.waitFor(() => expect(last.getCurrentResult().data).toEqual(fresh));
			// Nor may the read from between the events overwrite it later.
			finish[1]({ data: second });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(last.getCurrentResult().data).toEqual(fresh);
			expect(readCachedAgentModels(agentId, "")?.models).toEqual(fresh.models);
			expect(apiClient.GET).toHaveBeenCalledTimes(3);
			expect(last.getCurrentResult().isStale).toBe(false);
		} finally {
			for (const resolve of finish) resolve({ data: first });
			for (const unsubscribe of unsubscribes) unsubscribe();
			client.clear();
		}
	});

	it.each(["opencode", "opencode-v2"])("discards a detached %s read on one catalog event after a failed read", async (agentId) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions(agentId, "");
		const old: AgentModelCatalog = { agentId, models: [{ id: "provider/old", label: "Old" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		const fresh = { ...old, models: [{ id: "provider/new", label: "New" }] };
		let finishOld!: (value: { data: AgentModelCatalog }) => void;
		let finishNew!: (value: { data: AgentModelCatalog }) => void;
		vi.mocked(apiClient.GET)
			.mockRejectedValueOnce(new Error("daemon unavailable"))
			.mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; }) as never)
			.mockReturnValueOnce(new Promise((resolve) => { finishNew = resolve; }) as never);
		const failed = new QueryObserver(client, options);
		let unsubscribe = failed.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(failed.getCurrentResult().status).toBe("error"));
			unsubscribe();
			// A failed read marks the query invalidated without an invalidate event.
			expect(client.getQueryState(options.queryKey)?.isInvalidated).toBe(true);
			unsubscribe = new QueryObserver(client, options).subscribe(() => {});
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));
			unsubscribe();
			await catalogEvent(client, agentId);
			const remounted = new QueryObserver(client, options);
			unsubscribe = remounted.subscribe(() => {});
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(3));
			finishOld({ data: old });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(remounted.getCurrentResult().data).toBeUndefined();
			expect(readCachedAgentModels(agentId, "")).toBeUndefined();
			finishNew({ data: fresh });
			await vi.waitFor(() => expect(remounted.getCurrentResult().data).toEqual(fresh));
			expect(readCachedAgentModels(agentId, "")?.models).toEqual(fresh.models);
		} finally {
			finishOld?.({ data: old });
			finishNew?.({ data: fresh });
			unsubscribe();
			client.clear();
		}
	});

	it("does not reuse a pending OpenCode read after login invalidation", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions("opencode", "");
		const before: AgentModelCatalog = { agentId: "opencode", models: [], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, warning: "Not signed in", fetchedAt: "2026-10-18T00:00:00Z" };
		const after = { ...before, warning: undefined, models: [{ id: "provider/new", label: "New" }] };
		let finish!: (value: { data: AgentModelCatalog }) => void;
		vi.mocked(apiClient.GET).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }) as never).mockResolvedValue({ data: after } as never);
		const observer = new QueryObserver(client, options);
		const unsubscribe = observer.subscribe(() => {});
		try {
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
			await invalidateAgentModelCatalogs(client, "opencode");
			expect(observer.getCurrentResult().data).toEqual(after);
			finish({ data: before });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(observer.getCurrentResult().data).toEqual(after);
			expect(readCachedAgentModels("opencode", "")?.models).toEqual(after.models);
			expect(readCachedAgentModels("opencode", "")?.warning).toBeUndefined();
			expect(apiClient.GET).toHaveBeenCalledTimes(2);
		} finally { finish({ data: before }); unsubscribe(); client.clear(); }
	});

	it("keeps a manual refresh that arrives during an OpenCode read", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const options = agentModelsQueryOptions("opencode", "");
		const old: AgentModelCatalog = { agentId: "opencode", models: [{ id: "provider/old", label: "Old" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		const fresh = { ...old, models: [{ id: "provider/new", label: "New" }] };
		let finish!: (value: { data: AgentModelCatalog }) => void;
		vi.mocked(apiClient.GET).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }) as never);
		const pending = client.fetchQuery(options);
		try {
			await vi.waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));
			client.setQueryData(options.queryKey, fresh);
			finish({ data: old });
			expect(await pending).toEqual(fresh);
			expect(readCachedAgentModels("opencode", "")?.models).toEqual(fresh.models);
		} finally { finish({ data: old }); client.clear(); }
	});

	it.each(["opencode", "opencode-v2"])("deduplicates concurrent %s opens across projects and caches remounts", async (agentId) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const catalog: AgentModelCatalog = { agentId, models: [{ id: "provider/model", label: "Model", efforts: ["low", "high"], lastUsedAt: "2026-10-18T01:00:00Z" }], selectionMode: "catalog", customModelEntry: "direct", allowCustom: true, source: "cli", stale: false, fetchedAt: "2026-10-18T00:00:00Z" };
		vi.mocked(apiClient.GET).mockResolvedValue({ data: catalog } as never);
		try {
			const [first, second] = await Promise.all([
				client.fetchQuery(agentModelsQueryOptions(agentId, "project-a")),
				client.fetchQuery(agentModelsQueryOptions(agentId, "project-b")),
			]);
			expect(first).toEqual(catalog);
			expect(second).toEqual(catalog);
			await client.fetchQuery(agentModelsQueryOptions(agentId, "project-c"));
			expect(apiClient.GET).toHaveBeenCalledTimes(1);
			expect(apiClient.GET).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", { params: { path: { agent: agentId }, query: { projectId: undefined } } });
			expect(readCachedAgentModels(agentId, "")?.models).toEqual(catalog.models);
			// Settings still explicitly requests every provider's browsing metadata.
			await client.fetchQuery(agentModelsQueryOptions(agentId, "", undefined, true));
			expect(apiClient.GET).toHaveBeenCalledTimes(2);
			expect(apiClient.GET).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/models", { params: { path: { agent: agentId }, query: { projectId: undefined, catalogOnly: true } } });
		} finally { client.clear(); }
	});
});
