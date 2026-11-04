import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";

const { spawnMock, remoteSpawnMock } = vi.hoisted(() => ({
	spawnMock: vi.fn(),
	remoteSpawnMock: vi.fn(),
}));

vi.mock("../lib/spawn-orchestrator", () => ({
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

beforeEach(() => {
	vi.clearAllMocks();
});

describe("RestoreUnavailableDialog", () => {
	it("recreates cleanly even when the project has no configured orchestrator agent", async () => {
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
		expect(remoteSpawnMock).toHaveBeenCalledWith("box-b", "proj-1", undefined, undefined, true, "restore_dialog");
		expect(spawnMock).not.toHaveBeenCalled();
	});
});
