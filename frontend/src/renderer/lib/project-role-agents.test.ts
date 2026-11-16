import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fillMissingProjectRoleAgents } from "./project-role-agents";

const { getMock, putMock, remoteGetMock, remotePutMock, loadDefaultsMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	remoteGetMock: vi.fn(),
	remotePutMock: vi.fn(),
	loadDefaultsMock: vi.fn(),
}));

vi.mock("./api-client", () => ({ apiClient: { GET: getMock, PUT: putMock } }));
vi.mock("./host-clients", () => ({ clientForHost: () => ({ GET: remoteGetMock, PUT: remotePutMock }) }));
vi.mock("../hooks/useAgentReadinessQuery", () => ({ loadProjectAgentDefaults: loadDefaultsMock }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKeyForHost: (hostId?: string) => (hostId ? ["remote-workspaces", hostId] : ["workspaces"]),
}));

function projectResponse(config: Record<string, unknown>) {
	return { data: { status: "ok", project: { id: "proj-1", name: "Project One", config } }, error: undefined };
}

beforeEach(() => {
	for (const mock of [getMock, putMock, remoteGetMock, remotePutMock, loadDefaultsMock]) mock.mockReset();
	putMock.mockResolvedValue({ error: undefined });
	remotePutMock.mockResolvedValue({ error: undefined });
	loadDefaultsMock.mockResolvedValue({ workerAgent: "claude-code", orchestratorAgent: "claude-code", reviewerAgent: "claude-code" });
});

describe("fillMissingProjectRoleAgents", () => {
	it("fills only the empty role and keeps the rest of the config", async () => {
		getMock.mockResolvedValue(projectResponse({ defaultBranch: "develop", worker: { agent: "codex", agentConfig: { model: "gpt" } } }));

		await fillMissingProjectRoleAgents(new QueryClient(), "proj-1");

		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "proj-1" } },
			body: {
				displayName: "Project One",
				config: {
					defaultBranch: "develop",
					worker: { agent: "codex", agentConfig: { model: "gpt" } },
					orchestrator: { agent: "claude-code" },
				},
			},
		});
	});

	it("does nothing when both roles already name an agent", async () => {
		getMock.mockResolvedValue(projectResponse({ worker: { agent: "codex" }, orchestrator: { agent: "cursor" } }));

		await fillMissingProjectRoleAgents(new QueryClient(), "proj-1");

		expect(loadDefaultsMock).not.toHaveBeenCalled();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("saves to the remote host that owns the project", async () => {
		remoteGetMock.mockResolvedValue(projectResponse({}));

		await fillMissingProjectRoleAgents(new QueryClient(), "proj-1", "host-a");

		expect(loadDefaultsMock).toHaveBeenCalledWith(expect.any(QueryClient), "host-a");
		expect(remotePutMock).toHaveBeenCalledWith("/api/v1/projects/{id}", expect.objectContaining({
			body: expect.objectContaining({ config: { worker: { agent: "claude-code" }, orchestrator: { agent: "claude-code" } } }),
		}));
		expect(putMock).not.toHaveBeenCalled();
	});

	it("throws when the save fails so the caller can show the error", async () => {
		getMock.mockResolvedValue(projectResponse({}));
		putMock.mockResolvedValue({ error: { message: "nope" } });

		await expect(fillMissingProjectRoleAgents(new QueryClient(), "proj-1")).rejects.toThrow();
	});
});
