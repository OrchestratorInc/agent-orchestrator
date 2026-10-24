import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TurnSettings } from "../chat/types";
import { turnSettingsRows } from "../chat/turnSettingsModel";
import { CloudApiError } from "@aoagents/cloud-client";
const stored = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => stored.get(key) ?? null,
	setItem: async (key: string, value: string) => { stored.set(key, value); },
} }));
import { createCloudSessionSource } from "./source";

beforeEach(() => stored.clear());
function fixture({ mode = "trusted", modes = ["default", "plan"], harness = "claude-code" } = {}) {
	const session = { id: "s1", orgId: "o1", projectId: "p1", kind: "worker", harness, mode, interfaceMode: "chat", desiredState: "running", activityState: "idle", runtimeConnected: true, isTerminated: false };
	const catalog = { models: [{ id: "claude-sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" }], modes };
	const client = {
		getSession: vi.fn(async () => ({ session })),
		listChatModels: vi.fn(async () => catalog),
		replayEvents: vi.fn(async () => ({ events: [], nextAfter: 0, hasMore: false })),
		sendMessage: vi.fn(async () => ({})),
	};
	const create = () => createCloudSessionSource({ client: client as never, orgId: "o1", scopeKey: "turn-options", isCurrent: () => true });
	return { client, catalog, source: create(), create };
}

describe("Cloud Claude turn controls", () => {
	it("persists and sends an explicit agent-default effort after replacing a higher override", async () => {
		const { source, client, create } = fixture();
		await source.setTurnSettings!("s1", { reasoningEffort: "high" });
		await source.setTurnSettings!("s1", { reasoningEffort: "default" });
		const remounted = create();
		expect((await remounted.getConversationPage("s1")).settings).toMatchObject({ reasoningEffort: "default" });
		await remounted.sendMessage("s1", { text: "Inspect", clientMessageId: "default-effort" });
		expect(client.sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", reasoningEffort: "default" }, { idempotencyKey: "default-effort" });
		await remounted.sendMessage("s1", { text: "Override", clientMessageId: "per-turn", settings: { reasoningEffort: "default" } });
		expect(client.sendMessage).toHaveBeenLastCalledWith("o1", "s1", { text: "Override", reasoningEffort: "default" }, { idempotencyKey: "per-turn" });
	});
	it("adopts native default effort without accepting invalid effort values", async () => {
		const { source, client, catalog } = fixture();
		client.listChatModels.mockResolvedValue({ ...catalog, reasoningEffort: "default" } as never);
		expect((await source.getChatTurnOptions!("s1")).settings).toMatchObject({ reasoningEffort: "default" });
		await source.setTurnSettings!("s1", { reasoningEffort: "invalid" } as TurnSettings);
		expect((await source.getConversationPage("s1")).settings.reasoningEffort).toBeUndefined();
	});
	it("offers the worker's Model, Effort, and Plan/Agent choices through the existing settings rows", async () => {
		const { source } = fixture();
		expect(source.getChatTurnOptions).toBeTypeOf("function");
		const options = await source.getChatTurnOptions!("s1");
		const rows = turnSettingsRows(await source.getConversationPage("s1"), options.models, options.configOptions);
		expect(rows.map((row) => [row.label, row.choices.map((choice) => choice.label)])).toContainEqual(["Model", ["Sonnet"]]);
		expect(rows.map((row) => [row.label, row.choices.map((choice) => choice.label)])).toContainEqual(["Effort", ["Low", "High"]]);
		expect(rows.find((row) => row.label === "Mode")).toMatchObject({ value: "Agent", choices: [{ value: "agent", label: "Agent", selected: true }, { value: "plan", label: "Plan", selected: false }] });
	});

	it("persists Plan across source remounts and sends read-only without the saved approval override", async () => {
		const { source, client, create } = fixture();
		await source.setTurnSettings!("s1", { model: "claude-sonnet", reasoningEffort: "high", approvalMode: "bypass-permissions", executionMode: "plan" } as TurnSettings);
		await create().sendMessage("s1", { text: "Inspect", clientMessageId: "plan-one" });
		expect(client.sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", model: "claude-sonnet", reasoningEffort: "high", mode: "read-only" }, { idempotencyKey: "plan-one" });
	});

	it("saves an advertised mode choice and reflects it when the settings reopen", async () => {
		const { source } = fixture();
		expect(source.getChatTurnOptions).toBeTypeOf("function");
		const { configOptions } = await source.getChatTurnOptions!("s1");
		expect(source.setChatConfigOption).toBeTypeOf("function");
		const updated = await source.setChatConfigOption!("s1", configOptions[0].id, { value: "plan" });
		expect(updated[0]).toMatchObject({ currentValue: "plan" });
		expect((await source.getConversationPage("s1")).settings).toMatchObject({ executionMode: "plan" });
	});
	it("keeps Plan selected when the open settings sheet subsequently chooses a model", async () => {
		const { source, client } = fixture();
		await source.setTurnSettings!("s1", { executionMode: "plan" });
		await source.setTurnSettings!("s1", { model: "claude-sonnet", reasoningEffort: "high" });
		await source.sendMessage("s1", { text: "Inspect", clientMessageId: "plan-model" });
		expect(client.sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", model: "claude-sonnet", reasoningEffort: "high", mode: "read-only" }, { idempotencyKey: "plan-model" });
	});
	it("retains Plan and the model when native controls save both settings concurrently", async () => {
		const { source } = fixture();
		await Promise.all([
			source.setTurnSettings!("s1", { executionMode: "plan" }),
			source.setTurnSettings!("s1", { model: "claude-sonnet", reasoningEffort: "high" }),
		]);
		expect((await source.getConversationPage("s1")).settings).toMatchObject({ executionMode: "plan", model: "claude-sonnet", reasoningEffort: "high" });
	});
	it("adopts the worker's current model and effort when opening a remounted conversation", async () => {
		const { source, client, create } = fixture();
		await source.setTurnSettings!("s1", { model: "old-model", reasoningEffort: "low" });
		client.listChatModels.mockResolvedValue({ models: [{ id: "claude-sonnet", displayName: "Sonnet", default: true, efforts: ["low", "high"], defaultEffort: "high" }], modes: ["default", "plan"], model: "claude-sonnet", reasoningEffort: "high" } as never);
		const remounted = create();
		expect(remounted.getChatTurnOptions).toBeTypeOf("function");
		await remounted.getChatTurnOptions!("s1");
		expect((await remounted.getConversationPage("s1")).settings).toMatchObject({ model: "claude-sonnet", reasoningEffort: "high" });
	});
	it("rediscovers native model changes through the same cached source while keeping the saved Plan choice", async () => {
		const { source, client, catalog } = fixture();
		client.listChatModels.mockResolvedValue({ ...catalog, model: "native-a", reasoningEffort: "low" } as never);
		await source.getChatTurnOptions!("s1");
		await source.setTurnSettings!("s1", { model: "manual", reasoningEffort: "medium", executionMode: "plan" });
		client.listChatModels.mockResolvedValue({ ...catalog, model: "native-b", reasoningEffort: "high" } as never);
		expect((await source.getChatTurnOptions!("s1")).settings).toMatchObject({ model: "native-b", reasoningEffort: "high", executionMode: "plan" });
	});
	it("validates Plan without replacing a model selected on this phone with the worker's current model", async () => {
		const { source, client, catalog } = fixture();
		client.listChatModels.mockResolvedValue({ ...catalog, model: "native-a", reasoningEffort: "low" } as never);
		await source.getChatTurnOptions!("s1");
		await source.setTurnSettings!("s1", { model: "manual", reasoningEffort: "high" });
		client.listChatModels.mockResolvedValue({ ...catalog, model: "native-b", reasoningEffort: "low" } as never);
		await source.setChatConfigOption!("s1", "cloud-execution-mode", { value: "plan" });
		expect((await source.getConversationPage("s1")).settings).toMatchObject({ model: "manual", reasoningEffort: "high", executionMode: "plan" });
	});
	it.each(["older-first", "newer-first"])("adopts the newest overlapping discovery when responses arrive %s", async (order) => {
		const { source, client, catalog } = fixture();
		const finish: Array<(value: typeof catalog) => void> = [];
		client.listChatModels.mockImplementation(() => new Promise((resolve) => { finish.push(resolve); }));
		const older = source.getChatTurnOptions!("s1").then((value) => ({ value }), (error: unknown) => ({ error }));
		await vi.waitFor(() => expect(finish).toHaveLength(1));
		const newer = source.getChatTurnOptions!("s1").then((value) => ({ value }), (error: unknown) => ({ error }));
		await vi.waitFor(() => expect(finish).toHaveLength(2));
		const oldCatalog = { ...catalog, model: "native-a", reasoningEffort: "low" };
		const newCatalog = { ...catalog, model: "native-b", reasoningEffort: "high" };
		if (order === "older-first") { finish[0](oldCatalog); await older; finish[1](newCatalog); }
		else { finish[1](newCatalog); await newer; finish[0](oldCatalog); }
		expect(await older).toMatchObject({ error: { name: "AbortError" } });
		expect(await newer).toMatchObject({ value: { settings: { model: "native-b", reasoningEffort: "high" } } });
		expect((await source.getConversationPage("s1")).settings).toMatchObject({ model: "native-b", reasoningEffort: "high" });
	});
	it("does not supersede an active native discovery while validating and saving Plan", async () => {
		const { source, client, catalog } = fixture();
		let finish!: (value: typeof catalog) => void;
		client.listChatModels.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		const loading = source.getChatTurnOptions!("s1");
		await vi.waitFor(() => expect(finish).toBeTypeOf("function"));
		await source.setChatConfigOption!("s1", "cloud-execution-mode", { value: "plan" });
		finish({ ...catalog, model: "native-b", reasoningEffort: "high" } as typeof catalog);
		expect((await loading).settings).toMatchObject({ model: "native-b", reasoningEffort: "high", executionMode: "plan" });
	});
	it("retains a manual model selection made while the worker catalog was loading", async () => {
		const { source, client, catalog } = fixture();
		let finish!: (value: unknown) => void;
		client.listChatModels.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }) as never);
		expect(source.getChatTurnOptions).toBeTypeOf("function");
		const loading = source.getChatTurnOptions!("s1");
		await vi.waitFor(() => expect(finish).toBeTypeOf("function"));
		await source.setTurnSettings!("s1", { model: "manual-model", reasoningEffort: "low" });
		finish({ ...catalog, model: "claude-sonnet", reasoningEffort: "high" });
		await loading;
		expect((await source.getConversationPage("s1")).settings).toMatchObject({ model: "manual-model", reasoningEffort: "low" });
	});
	it("resumes a paused worker to obtain its advertised controls", async () => {
		vi.useFakeTimers();
		try {
			const { source, client } = fixture();
			const resumeSession = vi.fn(async () => ({}));
			Object.assign(client, { resumeSession });
			client.listChatModels.mockRejectedValueOnce(new CloudApiError(503, { error: "unavailable", code: "WORKER_UNAVAILABLE", message: "Worker is paused", requestId: "catalog-wake" }));
			expect(source.getChatTurnOptions).toBeTypeOf("function");
			const loading = source.getChatTurnOptions!("s1").then((value) => value, (error: unknown) => error);
			await vi.advanceTimersByTimeAsync(500);
			expect(await loading).toMatchObject({ models: [{ id: "claude-sonnet" }] });
			expect(resumeSession).toHaveBeenCalledWith("o1", "s1");
		} finally { vi.useRealTimers(); }
	});
	it("surfaces a catalog failure without posting a saved Plan as an Agent message", async () => {
		const { source, client } = fixture();
		await source.setTurnSettings!("s1", { executionMode: "plan" });
		client.listChatModels.mockRejectedValue(new Error("Catalog unavailable"));
		await expect(source.sendMessage("s1", { text: "Inspect", clientMessageId: "catalog-failed" })).rejects.toThrow("Catalog unavailable");
		expect(client.sendMessage).not.toHaveBeenCalled();
	});

	it("does not expose or apply saved Plan when the worker has no Plan capability", async () => {
		const { source, client } = fixture({ modes: [] });
		await source.setTurnSettings!("s1", { executionMode: "plan" } as TurnSettings);
		await expect(source.sendMessage("s1", { text: "Inspect", clientMessageId: "unsupported" })).rejects.toThrow(/plan.*unavailable/i);
		expect(client.sendMessage).not.toHaveBeenCalled();
	});

	it("keeps a read-only session in Plan even with a saved Agent choice", async () => {
		const { source, client } = fixture({ mode: "read-only" });
		await source.setTurnSettings!("s1", { executionMode: "agent", approvalMode: "bypass-permissions" } as TurnSettings);
		expect(source.getChatTurnOptions).toBeTypeOf("function");
		const { configOptions } = await source.getChatTurnOptions!("s1");
		expect(configOptions[0]).toMatchObject({ currentValue: "plan", choices: [{ value: "plan", name: "Plan" }] });
		await expect(source.setChatConfigOption!("s1", configOptions[0].id, { value: "agent" })).rejects.toThrow(/unavailable/i);
		await source.sendMessage("s1", { text: "Inspect", clientMessageId: "read-only" });
		expect(client.sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", mode: "read-only" }, { idempotencyKey: "read-only" });
	});
	it("suppresses a saved bypass override in a read-only session before a mode was selected", async () => {
		const { source, client } = fixture({ mode: "read-only" });
		await source.setTurnSettings!("s1", { approvalMode: "bypass-permissions" });
		expect((await source.getConversationPage("s1")).approvalModes).toEqual([]);
		await source.sendMessage("s1", { text: "Inspect", clientMessageId: "read-only-approval" });
		expect(client.sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", mode: "read-only" }, { idempotencyKey: "read-only-approval" });
	});
	it.each(["default", "bypass-permissions"] as const)("clamps saved %s permissions to the standard Codex ceiling", async (approvalMode) => {
		const { source, client } = fixture({ mode: "standard", harness: "codex" });
		await source.setTurnSettings!("s1", { approvalMode });
		const snapshot = await source.getConversationPage("s1");
		expect(snapshot.approvalModes).toEqual(["accept-edits", "auto"]);
		expect(snapshot.settings.approvalMode).toBe("accept-edits");
		await source.sendMessage("s1", { text: "Inspect", clientMessageId: "standard-approval" });
		expect(client.sendMessage).toHaveBeenCalledWith("o1", "s1", { text: "Inspect", approvalMode: "accept-edits" }, { idempotencyKey: "standard-approval" });
	});
});
