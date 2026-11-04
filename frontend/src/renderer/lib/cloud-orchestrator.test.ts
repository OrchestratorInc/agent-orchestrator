import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { settingsQueryKey, type Settings } from "../hooks/useSettings";
import type { CloudCpProviderConnection } from "./cloud-cp";
import { selectCloudOrchestratorHarness, spawnCloudOrchestrator } from "./cloud-orchestrator";

const cloudMocks = vi.hoisted(() => ({
	me: vi.fn(),
	listUserProviderConnections: vi.fn(),
	createSession: vi.fn(),
}));

vi.mock("../hooks/useCloudCp", () => ({
	createRendererCloudCpClient: () => ({
		me: cloudMocks.me,
		listUserProviderConnections: cloudMocks.listUserProviderConnections,
		createSession: cloudMocks.createSession,
	}),
}));

function connection(provider: string, validationState = "valid"): CloudCpProviderConnection {
	return {
		id: provider,
		provider,
		label: "default",
		config: {},
		validationState,
		createdAt: "2026-01-01T00:00:00Z",
		updatedAt: "2026-01-01T00:00:00Z",
	};
}

describe("selectCloudOrchestratorHarness", () => {
	it("uses the connected harness when it is the only Cloud option", () => {
		expect(selectCloudOrchestratorHarness([connection("claude-code")])).toBe("claude-code");
	});

	it("prefers claude-code, then codex, then cursor, then opencode", () => {
		expect(selectCloudOrchestratorHarness([connection("cursor"), connection("codex")])).toBe("codex");
		expect(selectCloudOrchestratorHarness([connection("codex"), connection("claude-code")])).toBe("claude-code");
		expect(selectCloudOrchestratorHarness([connection("opencode"), connection("cursor")])).toBe("cursor");
	});

	it("ignores invalid, non-default, and non-agent provider connections", () => {
		const invalid = connection("codex", "invalid");
		const nonDefault = { ...connection("claude-code"), label: "secondary" };
		expect(selectCloudOrchestratorHarness([invalid, nonDefault, connection("github")])).toBeUndefined();
	});
});

describe("spawnCloudOrchestrator", () => {
	beforeEach(() => {
		cloudMocks.me.mockReset();
		cloudMocks.listUserProviderConnections.mockReset();
		cloudMocks.createSession.mockReset();
	});

	function primeClient(project?: { id: string; config?: Record<string, unknown> }) {
		const queryClient = new QueryClient();
		queryClient.setQueryData<Settings>(settingsQueryKey, {
			cloudControlPlaneUrl: "https://cloud.example.com",
		} as Settings);
		cloudMocks.me.mockResolvedValue({ organizations: [{ id: "org-1" }], sandboxProviders: { available: ["docker"], default: "docker" } });
		cloudMocks.listUserProviderConnections.mockResolvedValue({ providerConnections: [] });
		cloudMocks.createSession.mockResolvedValue({ session: { id: "session-1" } });
		return queryClient;
	}

	it("starts without a user kickoff prompt so the role comes only from the system prompt", async () => {
		const queryClient = primeClient({ id: "project-1" });
		cloudMocks.listUserProviderConnections.mockResolvedValue({
			providerConnections: [connection("claude-code")],
		});

		await expect(spawnCloudOrchestrator(queryClient, "project-1")).resolves.toBe("session-1");
		expect(cloudMocks.createSession).toHaveBeenCalledWith("org-1", {
			projectId: "project-1",
			kind: "orchestrator",
			harness: "claude-code",
			displayName: "Orchestrator",
			prompt: "",
			defaultInterfaceMode: "tui",
		});
	});

	// The control plane starts the orchestrator in Chat as the local daemon does
	// and keeps the default for the workers it spawns. "tui" is sent too: an
	// orchestrator created without a default starts in the terminal.
	it("is created with the user's default session interface", async () => {
		for (const [defaultSessionMode, interfaceMode] of [["chat", "chat"], ["tui", "tui"]] as const) {
			const queryClient = primeClient({ id: "project-1" });
			queryClient.setQueryData<Settings>(settingsQueryKey, {
				cloudControlPlaneUrl: "https://cloud.example.com",
				defaultSessionMode,
			} as Settings);
			cloudMocks.listUserProviderConnections.mockResolvedValue({ providerConnections: [connection("codex")] });
			cloudMocks.createSession.mockClear();

			await spawnCloudOrchestrator(queryClient, "project-1");
			expect(cloudMocks.createSession.mock.calls[0]?.[1]?.defaultInterfaceMode).toBe(interfaceMode);
		}
	});

	it("leaves the provider to the project and org default on the control plane", async () => {
		const queryClient = primeClient({ id: "project-1", config: { sandboxProvider: "coder" } });
		cloudMocks.listUserProviderConnections.mockResolvedValue({ providerConnections: [connection("codex")] });

		await spawnCloudOrchestrator(queryClient, "project-1");
		expect(cloudMocks.createSession.mock.calls[0]?.[1]).not.toHaveProperty("provider");
	});

	it("ignores an agent pinned in an old project config", async () => {
		const queryClient = primeClient({
			id: "project-1",
			config: { orchestrator: { agent: "cursor" } },
		});
		cloudMocks.listUserProviderConnections.mockResolvedValue({
			providerConnections: [connection("cursor"), connection("codex")],
		});

		await expect(spawnCloudOrchestrator(queryClient, "project-1")).resolves.toBe("session-1");
		expect(cloudMocks.createSession).toHaveBeenCalledWith(
			"org-1",
			expect.objectContaining({ harness: "codex", kind: "orchestrator" }),
		);
	});
});
