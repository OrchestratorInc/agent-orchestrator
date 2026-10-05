import type { CloudClient } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { createCloudSessionSource } from "./source";

describe("mobile Cloud starts", () => {
	it("omits a provider for top-level workers", async () => {
		const createSession = vi.fn(async (_orgId: string, _input: Record<string, unknown>) => ({ session: { id: "s2" } }));
		const source = createCloudSessionSource({ client: { createSession } as unknown as CloudClient, orgId: "org-1" });
		await source.createSession({ projectId: "project-1", harness: "codex", prompt: "do work" });
		expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty("provider");
	});
});
