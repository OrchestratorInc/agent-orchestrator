import { describe, expect, it, vi } from "vitest";
import { CloudError } from "./errors";
import { agentModelCatalog, cachedAgentCatalog, cachedSessionCatalog, modelsRetryDelay, rememberSessionCatalog, sessionCatalogKey } from "./modelCatalog";

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
