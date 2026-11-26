import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render as renderBase, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { OrchestratorSpawnError } from "../lib/spawn-orchestrator";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";

const { spawnMock, remoteSpawnMock, workspaceQueryMock } = vi.hoisted(() => ({
	spawnMock: vi.fn(),
	remoteSpawnMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
}));

function render(ui: ReactElement) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return renderBase(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceScope: (projectId: string, sessionId?: string, hostId?: string) => workspaceQueryMock(projectId, sessionId, hostId),
}));

vi.mock("../lib/spawn-orchestrator", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/spawn-orchestrator")>(),
	spawnOrchestrator: spawnMock,
}));
vi.mock("../lib/remote-orchestrator", () => ({ openRemoteOrchestrator: remoteSpawnMock }));

const session: WorkspaceSession = {
	id: "orch-old",
	workspaceId: "proj-1",
	workspaceName: "Project One",
	title: "orchestrator",
	provider: "codex",
	kind: "orchestrator",
	status: "terminated",
	updatedAt: "2026-07-26T00:00:00Z",
	prs: [],
};

const workspace: WorkspaceSummary = {
	id: "proj-1",
	name: "Project One",
	path: "/repo/project-one",
	orchestratorAgent: "codex",
	sessions: [session],
};

beforeEach(() => {
	vi.clearAllMocks();
	useUiStore.setState({ settingsModal: null });
	workspaceQueryMock.mockReturnValue({ data: { project: workspace }, isLoading: false });
});

describe("RestoreUnavailableDialog", () => {
	it("recreates a local orchestrator with no orchestrator agent instead of opening settings", async () => {
		const onOpenChange = vi.fn();
		const onRecreated = vi.fn();
		spawnMock.mockResolvedValue("orch-new");
		workspaceQueryMock.mockReturnValue({
			data: { project: { ...workspace, orchestratorAgent: undefined } },
			isLoading: false,
		});
		render(
			<RestoreUnavailableDialog
				open
				session={session}
				onOpenChange={onOpenChange}
				onRecreated={onRecreated}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Create new orchestrator" }));

		await waitFor(() => expect(onRecreated).toHaveBeenCalledWith("orch-new"));
		expect(spawnMock).toHaveBeenCalledWith("proj-1", "restore_dialog", false);
		expect(useUiStore.getState().settingsModal).toBeNull();
	});

	it("preserves clean recreation when an orchestrator agent is configured", async () => {
		const onOpenChange = vi.fn();
		const onRecreated = vi.fn();
		spawnMock.mockResolvedValue("orch-new");
		render(
			<RestoreUnavailableDialog
				open
				session={session}
				onOpenChange={onOpenChange}
				onRecreated={onRecreated}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Create new orchestrator" }));

		await waitFor(() => expect(onRecreated).toHaveBeenCalledWith("orch-new"));
		expect(spawnMock).toHaveBeenCalledWith("proj-1", "restore_dialog", true);
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("recreates a remote orchestrator on its own host, not the local daemon", async () => {
		const onRecreated = vi.fn();
		remoteSpawnMock.mockResolvedValue("remote-new");
		render(<RestoreUnavailableDialog open hostId="box-b" session={session} onOpenChange={vi.fn()} onRecreated={onRecreated} />);

		await userEvent.click(screen.getByRole("button", { name: "Create new orchestrator" }));

		await waitFor(() => expect(onRecreated).toHaveBeenCalledWith("remote-new"));
		expect(workspaceQueryMock).toHaveBeenCalledWith("proj-1", undefined, "box-b");
		expect(remoteSpawnMock).toHaveBeenCalledWith("box-b", "proj-1", undefined, undefined, true, "restore_dialog");
		expect(spawnMock).not.toHaveBeenCalled();
	});

	it("lets the remote daemon recreate an orchestrator when the project has no agent", async () => {
		remoteSpawnMock.mockResolvedValue("remote-new");
		workspaceQueryMock.mockReturnValue({ data: { project: { ...workspace, orchestratorAgent: undefined } }, isLoading: false });
		const onRecreated = vi.fn();
		render(<RestoreUnavailableDialog open hostId="box-b" session={session} onOpenChange={vi.fn()} onRecreated={onRecreated} />);

		await userEvent.click(screen.getByRole("button", { name: "Create new orchestrator" }));

		await waitFor(() => expect(onRecreated).toHaveBeenCalledWith("remote-new"));
		expect(remoteSpawnMock).toHaveBeenCalledWith("box-b", "proj-1", undefined, undefined, false, "restore_dialog");
		expect(spawnMock).not.toHaveBeenCalled();
		expect(useUiStore.getState().settingsModal).toBeNull();
	});

	it("closes restore and opens the owning host's agent settings when the daemon requires an agent", async () => {
		const onOpenChange = vi.fn();
		const onRecreated = vi.fn();
		workspaceQueryMock.mockReturnValue({ data: { project: { ...workspace, orchestratorAgent: undefined } }, isLoading: false });
		remoteSpawnMock.mockRejectedValueOnce(new OrchestratorSpawnError("Configure an orchestrator agent", "AGENT_REQUIRED"));
		render(<RestoreUnavailableDialog open hostId="box-b" session={session} onOpenChange={onOpenChange} onRecreated={onRecreated} />);

		await userEvent.click(screen.getByRole("button", { name: "Create new orchestrator" }));

		expect(onOpenChange).toHaveBeenCalledWith(false);
		expect(onRecreated).not.toHaveBeenCalled();
		expect(useUiStore.getState().settingsModal).toEqual({
			scope: "project", projectId: "proj-1", hostId: "box-b", section: "agents",
		});
	});
});
