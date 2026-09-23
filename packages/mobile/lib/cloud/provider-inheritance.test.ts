import type { CloudClient } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { spawnCloudOrchestrator } from "./orchestrator";
import { createCloudSessionSource } from "./source";

describe("mobile Cloud starts", () => {
	it("omits a provider for orchestrators so the account preference wins on the server", async () => {
		const createSession = vi.fn(async (_orgId: string, _input: Record<string, unknown>) => ({ session: { id: "s1" } }));
		const client = {
			listProviderConnections: vi.fn(async () => [{ provider: "codex", label: "default", validationState: "valid" }]),
			listUserProviderConnections: vi.fn(async () => []),
			listProjects: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
			createSession,
		} as unknown as CloudClient;
		await spawnCloudOrchestrator(client, "org-1", "project-1", "request-1");
		expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty("provider");
	});

	it("omits a provider for top-level workers", async () => {
		const createSession = vi.fn(async (_orgId: string, _input: Record<string, unknown>) => ({ session: { id: "s2" } }));
		const source = createCloudSessionSource({ client: { createSession } as unknown as CloudClient, orgId: "org-1" });
		await source.createSession({ projectId: "project-1", harness: "codex", prompt: "do work" });
		expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty("provider");
	});
});
