import type { CloudClient } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { spawnCloudOrchestrator } from "./orchestrator";

function connection(provider: string, validationState = "valid") {
	return { provider, label: "default", validationState };
}

function fakeClient(config: Record<string, unknown> = { orchestrator: { agent: "claude-code" } }) {
	const createSession = vi.fn(async () => ({ session: { id: "session-1" } }));
	const client = {
		listProviderConnections: vi.fn(async () => [connection("codex")]),
		listUserProviderConnections: vi.fn(async () => [connection("claude-code")]),
		listProjects: vi.fn(async () => ({ items: [{ id: "project-1", config }], page: { hasMore: false } })),
		createSession,
	} as unknown as CloudClient;
	return { client, createSession };
}

describe("Cloud project orchestrator", () => {
	it("creates one orchestrator with the project's chosen ready agent and an idempotency key", async () => {
		const { client, createSession } = fakeClient();
		const id = await spawnCloudOrchestrator(client, "org-1", "project-1", "request-1");
		expect(id).toBe("session-1");
		expect(createSession).toHaveBeenCalledWith("org-1", {
			projectId: "project-1",
			kind: "orchestrator",
			harness: "claude-code",
			displayName: "Orchestrator",
			prompt: "",
			mode: "trusted",
			deniedCommands: [],
		}, { idempotencyKey: "request-1" });
	});

	it("falls back to a ready agent when the saved project choice is unavailable", async () => {
		const { client, createSession } = fakeClient({ orchestrator: { agent: "cursor" } });
		await spawnCloudOrchestrator(client, "org-1", "project-1", "request-2");
		expect(createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ harness: "codex" }), { idempotencyKey: "request-2" });
	});

	it("does not create a session when no coding-agent credential is ready", async () => {
		const { client, createSession } = fakeClient();
		vi.mocked(client.listProviderConnections).mockResolvedValue([]);
		vi.mocked(client.listUserProviderConnections).mockResolvedValue([]);
		await expect(spawnCloudOrchestrator(client, "org-1", "project-1", "request-3"))
			.rejects.toThrow("Connect a Cloud coding agent");
		expect(createSession).not.toHaveBeenCalled();
	});
});
