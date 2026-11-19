import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { expect, test, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { WorkspaceSession } from "../types/workspace";
import { SessionArchiveDialog } from "./SessionArchiveDialog";

vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.test" } }) }));

function withQueries(ui: ReactNode) {
	return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider>;
}

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
	render(withQueries(<SessionArchiveDialog open onOpenChange={vi.fn()} onConfirm={onConfirm} session={cloudSession} trigger={<span />} />));

	const dialog = screen.getByRole("dialog", { name: "Are you sure you want to archive this session?" });
	await userEvent.click(within(dialog).getByRole("checkbox", { name: "Delete 1 artifact" }));
	await userEvent.click(within(dialog).getByRole("button", { name: "Confirm, archive session" }));

	expect(onConfirm).toHaveBeenCalledWith({ deleteArtifacts: true });
});

test("says how many of the artifacts are published, since their links stop working with them", () => {
	const local = {
		...cloudSession,
		cloud: undefined,
		artifactFiles: [
			{ kind: "markdown", name: "a.md", path: "a.md", size: 1, updatedAt: "2026-10-01T00:00:00Z", shareUrl: "https://l/a" },
			{ kind: "markdown", name: "b.md", path: "b.md", size: 1, updatedAt: "2026-10-01T00:00:00Z" },
		],
	} as WorkspaceSession;
	render(<SessionArchiveDialog open onOpenChange={vi.fn()} onConfirm={vi.fn()} session={local} trigger={<span />} />);

	const dialog = screen.getByRole("dialog", { name: "Are you sure you want to archive this session?" });
	expect(within(dialog).getByRole("checkbox", { name: "Delete 2 artifacts" })).toBeTruthy();
	expect(within(dialog).getByText(/1 is published; its link will stop working\./)).toBeTruthy();
});

test("looks a cloud session's links up on this computer, since its file list does not say which are published", async () => {
	const inspect = vi.spyOn(aoBridge.cloudShare, "inspect").mockResolvedValue({ name: "report.html", unsupported: false, fingerprint: "f", shareId: "share-1" });
	render(withQueries(<SessionArchiveDialog open onOpenChange={vi.fn()} onConfirm={vi.fn()} session={cloudSession} trigger={<span />} />));

	expect(await screen.findByText(/is published; its link will stop working/)).toBeInTheDocument();
	expect(inspect).toHaveBeenCalledWith(expect.objectContaining({ source: "cloud", orgId: "org-1", sessionId: "session-1", path: "report.html", baseUrl: "https://cp.test" }));
	inspect.mockRestore();
});

test("counts artifacts as the lists show them, and says the delete takes their other files too", () => {
	const file = (path: string, registered = false) => ({ kind: "generic", name: path.split("/").pop(), path, size: 1, updatedAt: "2026-10-01T00:00:00Z", ...(registered ? { registered: true } : {}) });
	const local = {
		...cloudSession,
		cloud: undefined,
		// One registered page, the images it uses, and an icon bundle of two files.
		artifactFiles: [file("report.html", true), file("img/a.png"), file("img/b.png"), file("App.icon/icon.json", true), file("App.icon/Assets/a.png", true)],
	} as WorkspaceSession;
	render(<SessionArchiveDialog open onOpenChange={vi.fn()} onConfirm={vi.fn()} session={local} trigger={<span />} />);

	expect(screen.getByRole("checkbox", { name: "Delete 2 artifacts" })).toBeInTheDocument();
	expect(screen.getByText(/The other files in their folder are deleted too\./)).toBeInTheDocument();
});
