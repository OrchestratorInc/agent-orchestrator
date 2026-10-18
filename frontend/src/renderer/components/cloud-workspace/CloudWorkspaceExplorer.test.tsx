// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
import { CloudWorkspaceExplorer } from "./CloudWorkspaceExplorer";

const { getWorkspaceReview, getWorkspaceReviewDiffs, getWorkspaceReviewTree } = vi.hoisted(() => ({
	getWorkspaceReview: vi.fn(),
	getWorkspaceReviewDiffs: vi.fn(),
	getWorkspaceReviewTree: vi.fn(),
}));

vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ baseUrl: "https://cloud.test", ready: true, client: { getWorkspaceReview, getWorkspaceReviewDiffs, getWorkspaceReviewTree } }),
}));
vi.mock("../../hooks/useCloudWorkspaceReview", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../../hooks/useCloudWorkspaceReview")>();
	return { ...actual, useCloudWorkspaceReviewEvents: () => undefined };
});
const session: WorkspaceSession = {
	branch: "ao/cloud", cloud: { orgId: "org-1", sandboxProvider: "nodeops" }, id: "session-1", prs: [],
	provider: "codex", status: "working", title: "Cloud", updatedAt: "2026-09-20T00:00:00Z",
	workspaceId: "workspace-1", workspaceName: "Cloud workspace",
};

describe("CloudWorkspaceExplorer", () => {
	it("switches from categorized changes to the complete repository tree", async () => {
		getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [{ patch: "", truncated: false, includedPaths: [], deferred: [], errors: [] }] });
		getWorkspaceReview.mockResolvedValue({
			workspaceVersion: "v1", files: [{ path: "README.md", status: "unmodified", additions: 0, deletions: 0, size: 5, binary: false, editable: true, fileFingerprint: "fp" }],
			truncated: false, sections: { staged: [], unstaged: [{ path: "src/App.tsx", status: "modified", additions: 1, deletions: 0, size: 10, binary: false, editable: true, fileFingerprint: "fp2" }], untracked: [], committed: [] }, commits: [],
			summary: { files: 1, additions: 1, deletions: 0 },
		});
		getWorkspaceReviewTree.mockResolvedValue({ path: "", truncated: false, entries: [{ name: "README.md", path: "README.md", type: "file", status: "unmodified", size: 5 }] });
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TooltipProvider><CloudWorkspaceExplorer session={session} /></TooltipProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByText("src/App.tsx")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("tab", { name: "Files" }));
		expect(await screen.findByRole("treeitem", { name: /README\.md/ })).toBeInTheDocument();
	});

	it("reviews one repository at a time in a multi-repository session", async () => {
		const file = (path: string) => ({ path, status: "modified", additions: 1, deletions: 0, size: 10, binary: false, editable: true, fileFingerprint: path });
		const summary = (path: string, withRepositories: boolean) => ({
			workspaceVersion: "v1", files: [], truncated: false, commits: [], summary: { files: 1, additions: 1, deletions: 0 },
			sections: { staged: [], unstaged: [file(path)], untracked: [], committed: [] },
			...(withRepositories ? { repositories: [
				{ name: "11x", repository: "acme/11x", present: true },
				{ name: "alice", repository: "acme/alice", main: true, present: true },
				{ name: "voice-calling-ai", present: false },
			] } : {}),
		});
		getWorkspaceReview.mockReset().mockImplementation((_org: string, _session: string, options?: { repository?: string }) =>
			Promise.resolve(options?.repository === "11x" ? summary("server/main.go", false) : summary("src/App.tsx", true)));
		getWorkspaceReviewDiffs.mockReset().mockResolvedValue({ workspaceVersion: "v1", groups: [] });
		const onOpenFile = vi.fn();
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TooltipProvider><CloudWorkspaceExplorer onOpenFile={onOpenFile} session={session} /></TooltipProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByText("src/App.tsx")).toBeInTheDocument();
		const tabs = screen.getByRole("tablist", { name: "Repositories" });
		// The main repository comes first; a missing checkout cannot be picked.
		expect(within(tabs).getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["alice", "11x", "voice-calling-ai"]);
		expect(within(tabs).getByRole("tab", { name: "voice-calling-ai" })).toBeDisabled();
		await userEvent.click(within(tabs).getByRole("tab", { name: "11x" }));
		expect(await screen.findByText("server/main.go")).toBeInTheDocument();
		expect(getWorkspaceReview).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ repository: "11x" }));
		expect(getWorkspaceReviewDiffs).toHaveBeenCalledWith("org-1", "session-1", expect.anything(), expect.objectContaining({ repository: "11x" }));
		// A sibling's file opens as ../<repository>/<path>.
		await userEvent.click(screen.getByRole("button", { name: "Open full file" }));
		expect(onOpenFile).toHaveBeenCalledWith("../11x/server/main.go", expect.anything());
	});
});
