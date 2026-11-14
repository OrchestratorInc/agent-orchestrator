import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionArchiveDialog } from "./SessionArchiveDialog";

const cloudSession = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "Project",
	title: "Cloud worker",
	provider: "claude-code",
	status: "working",
	updatedAt: "2026-10-01T00:00:00Z",
	prs: [],
	cloud: { orgId: "org-1" },
	outputType: "artifact",
	artifactFiles: [{ kind: "html", name: "report.html", path: "report.html", size: 1, updatedAt: "2026-10-01T00:00:00Z" }],
} as WorkspaceSession;

test("offers to delete a cloud session's artifacts", async () => {
	const onConfirm = vi.fn();
	render(<SessionArchiveDialog open onOpenChange={vi.fn()} onConfirm={onConfirm} session={cloudSession} trigger={<span />} />);

	const dialog = screen.getByRole("dialog", { name: "Are you sure you want to archive this session?" });
	await userEvent.click(within(dialog).getByRole("checkbox", { name: "Delete 1 artifact" }));
	await userEvent.click(within(dialog).getByRole("button", { name: "Confirm, archive session" }));

	expect(onConfirm).toHaveBeenCalledWith({ deleteArtifacts: true });
});
