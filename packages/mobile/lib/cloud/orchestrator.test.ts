import type { CloudClient } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { spawnCloudOrchestrator } from "./orchestrator";

function connection(provider: string, validationState = "valid") {
	return { provider, label: "default", validationState };
}

function fakeClient(config: Record<string, unknown> = { orchestrator: { agent: "claude-code" } }) {
	const createSession = vi.fn(async (_orgId: string, _input: import("@aoagents/cloud-client").CreateSessionInput, _options: { idempotencyKey: string }) => ({ session: { id: "session-1" } }));
	const client = {
		listProviderConnections: vi.fn(async () => [connection("codex")]),
		listUserProviderConnections: vi.fn(async () => [connection("claude-code"), connection("codex")]),
		listSessions: vi.fn(async () => ({ items: [], page: { hasMore: false } })),
		getProject: vi.fn(async () => ({ project: { id: "project-1", config } })),
		listProjects: vi.fn(async () => ({ items: [{ id: "project-1", config }], page: { hasMore: false } })),
		createSession,
	} as unknown as CloudClient;
	return { client, createSession };
}

describe("Cloud project orchestrator", () => {
	it("allows the configured OpenCode agent without pretending create selects Chat", async () => {
		const { client, createSession } = fakeClient({ orchestrator: { agent: "opencode" } });
		vi.mocked(client.listUserProviderConnections).mockResolvedValue([connection("opencode")] as never);
		await spawnCloudOrchestrator(client, "org-1", "project-1", "request-open");
		expect(createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ harness: "opencode" }), { idempotencyKey: "request-open" });
		expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty("interfaceMode");
	});
	it("opens the existing project orchestrator without creating or requiring new credentials", async () => {
		const { client, createSession } = fakeClient();
		vi.mocked(client.listSessions).mockResolvedValue({ items: [{ id: "existing", projectId: "project-1", kind: "orchestrator", isTerminated: false }], page: { hasMore: false } } as never);
		vi.mocked(client.listUserProviderConnections).mockResolvedValue([]);
		expect(await spawnCloudOrchestrator(client, "org-1", "project-1", "request-1")).toBe("existing");
		expect(createSession).not.toHaveBeenCalled();
	});
	it("applies model and effort only to the matching orchestrator agent", async () => {
		const { client, createSession } = fakeClient({ orchestrator: { agent: "claude-code", agentConfig: { model: "configured", reasoningEffort: "high" } } });
		await spawnCloudOrchestrator(client, "org-1", "project-1", "request-1");
		expect(createSession).toHaveBeenCalledWith("org-1", expect.objectContaining({ model: "configured", reasoningEffort: "high" }), { idempotencyKey: "request-1" });
	});
	it("does not treat organization-only agent credentials as personal readiness", async () => {
		const { client, createSession } = fakeClient();
		vi.mocked(client.listUserProviderConnections).mockResolvedValue([]);
		await expect(spawnCloudOrchestrator(client, "org-1", "project-1", "request-4")).rejects.toThrow("Connect a Cloud coding agent");
		expect(createSession).not.toHaveBeenCalled();
	});
	it("loads the exact project and surfaces its configuration failure", async () => {
		const { client, createSession } = fakeClient();
		vi.mocked(client.getProject).mockRejectedValueOnce(new Error("Project could not be loaded"));
		await expect(spawnCloudOrchestrator(client, "org-1", "project-1", "request-5")).rejects.toThrow("Project could not be loaded");
		expect(createSession).not.toHaveBeenCalled();
	});
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
