import { describe, expect, it, vi } from "vitest";
import { CloudError } from "./errors";
import { forgetCloudAccountCaches } from "./accountCaches";
import { agentModelCatalog, cachedAgentCatalog, cachedSessionCatalog, catalogAccount, modelsRetryDelay, releaseCatalogRefreshMs, rememberSessionCatalog, sessionCatalogKey } from "./modelCatalog";

const error = (code: string) => new CloudError("failed", { status: 503, code });

describe("cloud model catalogs", () => {
	it("asks the control plane once per agent and answers from memory while fresh", async () => {
		const listAgentModels = vi.fn(async () => ({ models: [{ id: "gpt-5.5", displayName: "GPT-5.5", default: true }] }));
		const client = { listAgentModels } as never;
		const [first, second] = await Promise.all([agentModelCatalog(client, "o1", "codex"), agentModelCatalog(client, "o1", "codex")]);
		expect(first).toBe(second);
		await agentModelCatalog(client, "o1", "codex");
		expect(listAgentModels).toHaveBeenCalledTimes(1);
		expect(cachedAgentCatalog("o1", "codex")?.models[0]?.id).toBe("gpt-5.5");
		expect(cachedAgentCatalog("o1", "codex", Date.now() + 6 * 60_000)).toBeUndefined();
	});

	it("keeps a worker's answer for that worker only", () => {
		rememberSessionCatalog(sessionCatalogKey("o1", "s1", 2), { models: [] });
		expect(cachedSessionCatalog(sessionCatalogKey("o1", "s1", 2))).toEqual({ models: [] });
		expect(cachedSessionCatalog(sessionCatalogKey("o1", "s1", 3))).toBeUndefined();
	});

	it("asks a connecting worker again quickly, and gives up on what will not clear", () => {
		expect([1, 2, 3, 4].map((failures) => modelsRetryDelay(failures, error("WORKER_UNAVAILABLE")))).toEqual([1_000, 2_000, 4_000, 8_000]);
		expect(modelsRetryDelay(20, error("WORKER_UNAVAILABLE"))).toBe(30_000);
		expect(modelsRetryDelay(1, error("CHAT_AGENT_INSTALLING"))).toBe(5_000);
		expect(modelsRetryDelay(36, error("CHAT_AGENT_INSTALLING"))).toBeUndefined();
		expect(modelsRetryDelay(3, error("WORKER_TIMEOUT"))).toBeUndefined();
		expect(modelsRetryDelay(1, error("FORBIDDEN"))).toBeUndefined();
		expect(modelsRetryDelay(1, new Error("offline"))).toBeUndefined();
	});
});

describe("a release catalog", () => {
	const release = { models: [{ id: "opus", displayName: "Opus", default: true }], source: "release" };
	const account = { models: [{ id: "opus", displayName: "Opus", description: "Opus 5.5 · Most capable", default: true }], source: "worker" };

	it("is fresh for 15 seconds, where the account's own list is fresh for five minutes", async () => {
		await agentModelCatalog({ listAgentModels: vi.fn(async () => release) } as never, "r1", "claude-code");
		expect(cachedAgentCatalog("r1", "claude-code")).toBe(release);
		expect(cachedAgentCatalog("r1", "claude-code", Date.now() + 16_000)).toBeUndefined();
		await agentModelCatalog({ listAgentModels: vi.fn(async () => account) } as never, "r2", "claude-code");
		expect(cachedAgentCatalog("r2", "claude-code", Date.now() + 16_000)).toBe(account);
	});

	it("is asked again on a scheduled re-read, and the account's own list is not", async () => {
		const listAgentModels = vi.fn(async () => release);
		const client = { listAgentModels } as never;
		await agentModelCatalog(client, "r3", "claude-code");
		await agentModelCatalog(client, "r3", "claude-code");
		expect(listAgentModels).toHaveBeenCalledTimes(1);
		listAgentModels.mockResolvedValueOnce(account as never);
		await expect(agentModelCatalog(client, "r3", "claude-code", { rereadRelease: true })).resolves.toBe(account);
		await agentModelCatalog(client, "r3", "claude-code", { rereadRelease: true });
		expect(listAgentModels).toHaveBeenCalledTimes(2);
	});

	it("is read again on desktop's schedule: 15s, doubling, at most five minutes", () => {
		expect([1, 2, 3, 4, 5, 6, 9].map(releaseCatalogRefreshMs)).toEqual([15_000, 30_000, 60_000, 120_000, 240_000, 300_000, 300_000]);
	});
});

describe("on an account change", () => {
	it("forgets every catalog, and drops an answer to a request made before it", async () => {
		const codex = { models: [{ id: "gpt-5.5", displayName: "GPT-5.5", default: true }] };
		await agentModelCatalog({ listAgentModels: vi.fn(async () => codex) } as never, "a1", "codex");
		rememberSessionCatalog(sessionCatalogKey("a1", "s1", 1), codex);
		let answer!: (value: unknown) => void;
		const late = agentModelCatalog({ listAgentModels: () => new Promise((resolve) => { answer = resolve; }) } as never, "a1", "claude-code");
		forgetCloudAccountCaches();
		expect(cachedAgentCatalog("a1", "codex")).toBeUndefined();
		expect(cachedSessionCatalog(sessionCatalogKey("a1", "s1", 1))).toBeUndefined();
		answer(codex);
		await late;
		expect(cachedAgentCatalog("a1", "claude-code")).toBeUndefined();
	});

	it("drops a session's list asked for before the account changed", () => {
		const asked = catalogAccount();
		forgetCloudAccountCaches();
		rememberSessionCatalog(sessionCatalogKey("a2", "s1", 1), { models: [] }, asked);
		expect(cachedSessionCatalog(sessionCatalogKey("a2", "s1", 1))).toBeUndefined();
		rememberSessionCatalog(sessionCatalogKey("a2", "s1", 1), { models: [] }, catalogAccount());
		expect(cachedSessionCatalog(sessionCatalogKey("a2", "s1", 1))).toBeDefined();
	});
});
