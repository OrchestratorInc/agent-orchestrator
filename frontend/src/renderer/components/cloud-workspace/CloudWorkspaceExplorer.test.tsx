// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpWorkspaceReviewFileSummary, CloudCpWorkspaceReviewResponse } from "../../lib/cloud-cp";
import { useUiStore } from "../../stores/ui-store";
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

const changed = (path: string, status: CloudCpWorkspaceReviewFileSummary["status"] = "modified"): CloudCpWorkspaceReviewFileSummary => ({
	path, status, additions: 1, deletions: 0, size: 10, binary: false, editable: true, fileFingerprint: `fp:${path}`,
});

function mixedReview(): CloudCpWorkspaceReviewResponse {
	const shared = changed("src/App.tsx");
	const staged = changed("src/staged.ts");
	const untracked = changed("notes.txt", "untracked");
	const committed = changed("src/old.ts");
	return {
		workspaceVersion: "v1", files: [shared, staged, untracked, committed], truncated: false,
		sections: { unstaged: [shared], staged: [shared, staged], untracked: [untracked], committed: [committed] },
		commits: [{ sha: "abcdef123", subject: "Previous work", author: "Ada", timestamp: "2026-09-20T00:00:00Z", files: [committed] }],
		summary: { files: 4, additions: 4, deletions: 0 },
	};
}

function RemountingExplorer({ activeSession }: { activeSession: WorkspaceSession }) {
	const [maximized, setMaximized] = useState(false);
	return <div data-testid={maximized ? "portal-review" : "inline-review"}>
		<CloudWorkspaceExplorer key={`${activeSession.id}:${maximized ? "portal" : "inline"}`} isMaximized={maximized} onToggleMaximized={setMaximized} session={activeSession} />
	</div>;
}

function renderExplorer(activeSession: WorkspaceSession = session) {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<TooltipProvider><RemountingExplorer activeSession={activeSession} /></TooltipProvider>
		</QueryClientProvider>,
	);
}

describe("CloudWorkspaceExplorer", () => {
	beforeEach(() => {
		getWorkspaceReview.mockReset();
		getWorkspaceReviewDiffs.mockReset();
		getWorkspaceReviewTree.mockReset();
		window.localStorage.clear();
		useUiStore.setState({ inspectorSessions: {} });
	});

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

	it("keeps the combined review and its viewed records through maximize and minimize remounts", async () => {
		getWorkspaceReview.mockResolvedValue(mixedReview());
		getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [{ patch: "", truncated: false, includedPaths: [], deferred: [], errors: [] }] });
		renderExplorer();

		await userEvent.click(await screen.findByRole("button", { name: /^Changes/ }));
		await userEvent.click(screen.getByRole("checkbox", { name: "Mark src/App.tsx as viewed" }));
		expect(screen.getByText("1 of 3 viewed")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByTestId("portal-review")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Changes/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "false");
		expect(screen.getByText("1 of 3 viewed")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Minimize files" }));
		expect(await screen.findByTestId("inline-review")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Changes/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("1 of 3 viewed")).toBeInTheDocument();
	});

	it.each(["Unstaged", "Staged", "Untracked"])("keeps the %s review through a maximize remount", async (label) => {
		getWorkspaceReview.mockResolvedValue(mixedReview());
		getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [{ patch: "", truncated: false, includedPaths: [], deferred: [], errors: [] }] });
		renderExplorer();

		await userEvent.click(await screen.findByRole("button", { name: new RegExp(`^${label}`) }));
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByTestId("portal-review")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: new RegExp(`^${label}`) })).toHaveAttribute("aria-pressed", "true");
	});

	it("keeps a selected commit through a maximize remount", async () => {
		getWorkspaceReview.mockResolvedValue(mixedReview());
		getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [{ patch: "", truncated: false, includedPaths: [], deferred: [], errors: [] }] });
		renderExplorer();

		await userEvent.click(await screen.findByRole("button", { name: /^Commits/ }));
		await userEvent.click(screen.getByRole("button", { name: /Previous work/ }));
		await waitFor(() => expect(getWorkspaceReviewDiffs).toHaveBeenLastCalledWith("org-1", "session-1", expect.objectContaining({ scope: "committed", commitSha: "abcdef123" })));

		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByTestId("portal-review")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Commits/ })).toHaveAttribute("aria-pressed", "true");
		await waitFor(() => expect(getWorkspaceReviewDiffs).toHaveBeenLastCalledWith("org-1", "session-1", expect.objectContaining({ scope: "committed", commitSha: "abcdef123" })));
	});

	it("restores review targets per session without carrying them to another session", async () => {
		getWorkspaceReview.mockResolvedValue(mixedReview());
		getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [{ patch: "", truncated: false, includedPaths: [], deferred: [], errors: [] }] });
		const otherSession = { ...session, id: "session-2", title: "Other cloud session" };
		const view = renderExplorer();

		await userEvent.click(await screen.findByRole("button", { name: /^Staged/ }));
		expect(screen.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");

		view.rerender(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TooltipProvider><RemountingExplorer activeSession={otherSession} /></TooltipProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByRole("button", { name: /^Unstaged/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "false");

		view.rerender(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<TooltipProvider><RemountingExplorer activeSession={session} /></TooltipProvider>
			</QueryClientProvider>,
		);
		expect(await screen.findByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
	});
});
