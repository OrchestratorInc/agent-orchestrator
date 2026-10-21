// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { cloudWorkspaceReviewQueryKey } from "../../hooks/useCloudWorkspaceReview";
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

function renderExplorer(activeSession: WorkspaceSession = session, queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider><RemountingExplorer activeSession={activeSession} /></TooltipProvider>
		</QueryClientProvider>,
	);
}

function mockRepositories() {
	const main = mixedReview();
	main.repositories = [
		{ name: "repository", label: "alice", main: true, present: true },
		{ name: "11x", present: true },
	];
	const sibling = mixedReview();
	sibling.sections = { unstaged: [], staged: [], untracked: [changed("src/App.tsx", "untracked")], committed: [changed("src/old.ts")] };
	sibling.commits = [{ ...sibling.commits[0], sha: "987654321", subject: "Sibling work" }];
	getWorkspaceReview.mockImplementation((_org: string, _session: string, options?: { repository?: string }) =>
		Promise.resolve(options?.repository === "11x" ? sibling : main));
	getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [] });
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

	it("retains each repository's scope and viewed records across switching and maximize/minimize remounts", async () => {
		mockRepositories();
		renderExplorer();
		await userEvent.click(await screen.findByRole("button", { name: /^Staged/ }));
		await userEvent.click(screen.getByRole("checkbox", { name: "Mark src/App.tsx as viewed" }));
		expect(screen.getByText("1 of 2 viewed")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("tab", { name: "11x" }));
		expect(await screen.findByRole("button", { name: /^Untracked/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("0 of 1 viewed")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("checkbox", { name: "Mark src/App.tsx as viewed" }));
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Untracked/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("1 of 1 viewed")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Minimize files" }));
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByText("1 of 1 viewed")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("tab", { name: "alice" }));
		expect(await screen.findByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("1 of 2 viewed")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("tab", { name: "alice" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("1 of 2 viewed")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("tab", { name: "11x" }));
		await userEvent.click(screen.getByRole("button", { name: "Minimize files" }));
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Untracked/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("1 of 1 viewed")).toBeInTheDocument();
	});

	it("restores separate commit selections for the main and sibling repository after remounts", async () => {
		mockRepositories();
		renderExplorer();
		await userEvent.click(await screen.findByRole("button", { name: /^Commits/ }));
		await userEvent.click(screen.getByRole("button", { name: /Previous work/ }));
		await userEvent.click(screen.getByRole("tab", { name: "11x" }));
		expect(await screen.findByRole("button", { name: /^Untracked/ })).toHaveAttribute("aria-pressed", "true");
		await userEvent.click(screen.getByRole("button", { name: /^Commits/ }));
		await userEvent.click(screen.getByRole("button", { name: /Sibling work/ }));
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Commits/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("9876543")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("tab", { name: "alice" }));
		await userEvent.click(screen.getByRole("button", { name: "Minimize files" }));
		expect(await screen.findByRole("tab", { name: "alice" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Commits/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("abcdef1")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("tab", { name: "11x" }));
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByText("9876543")).toBeInTheDocument();
		expect(useUiStore.getState().inspectorSessions[session.id].cloudReviewSelection).toEqual({
			"": { scope: "committed", commitSha: "abcdef123" },
			"11x": { scope: "committed", commitSha: "987654321" },
		});
	});

	it("isolates the active repository and review target between sessions through further remounts", async () => {
		mockRepositories();
		const otherSession = { ...session, id: "session-2" };
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = renderExplorer(session, queryClient);
		await userEvent.click(await screen.findByRole("tab", { name: "11x" }));
		await userEvent.click(await screen.findByRole("button", { name: /^Commits/ }));
		await userEvent.click(screen.getByRole("button", { name: /Sibling work/ }));
		const switchSession = (activeSession: WorkspaceSession) => view.rerender(
			<QueryClientProvider client={queryClient}><TooltipProvider><RemountingExplorer activeSession={activeSession} /></TooltipProvider></QueryClientProvider>,
		);
		switchSession(otherSession);
		expect(await screen.findByRole("tab", { name: "alice" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Unstaged/ })).toHaveAttribute("aria-pressed", "true");
		await userEvent.click(screen.getByRole("button", { name: /^Staged/ }));
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("tab", { name: "alice" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");

		switchSession(session);
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(await screen.findByText("9876543")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Minimize files" }));
		expect(await screen.findByRole("tab", { name: "11x" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Commits/ })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByText("9876543")).toBeInTheDocument();

		switchSession(otherSession);
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("tab", { name: "alice" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
	});

	it.each(["empty scope", "missing commit"])("persists the fallback for a saved %s through another maximize/minimize remount", async (scenario) => {
		getWorkspaceReview.mockResolvedValue(mixedReview());
		getWorkspaceReviewDiffs.mockResolvedValue({ workspaceVersion: "v1", groups: [] });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderExplorer(session, queryClient);
		if (scenario === "empty scope") {
			await userEvent.click(await screen.findByRole("button", { name: /^Staged/ }));
		} else {
			await userEvent.click(await screen.findByRole("button", { name: /^Commits/ }));
			await userEvent.click(screen.getByRole("button", { name: /Previous work/ }));
		}
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		const updated = mixedReview();
		updated.workspaceVersion = "v2";
		if (scenario === "empty scope") updated.sections.staged = [];
		else updated.commits = [];
		getWorkspaceReview.mockResolvedValue(updated);
		queryClient.setQueryData([...cloudWorkspaceReviewQueryKey("https://cloud.test", "org-1", session.id), "summary"], updated);
		await waitFor(() => expect(screen.getByRole("button", { name: /^Unstaged/ })).toHaveAttribute("aria-pressed", "true"));
		expect(useUiStore.getState().inspectorSessions[session.id].cloudReviewSelection?.[""]).toEqual({ scope: "unstaged" });
		await userEvent.click(screen.getByRole("button", { name: "Minimize files" }));
		expect(await screen.findByRole("button", { name: /^Unstaged/ })).toHaveAttribute("aria-pressed", "true");
		await userEvent.click(screen.getByRole("button", { name: "Maximize files" }));
		expect(await screen.findByRole("button", { name: /^Unstaged/ })).toHaveAttribute("aria-pressed", "true");
		expect(useUiStore.getState().inspectorSessions[session.id].cloudReviewSelection?.[""]).toEqual({ scope: "unstaged" });
	});

	it("reviews one repository at a time in a multi-repository session", async () => {
		const file = (path: string) => ({ path, status: "modified", additions: 1, deletions: 0, size: 10, binary: false, editable: true, fileFingerprint: path });
		const summary = (path: string, withRepositories: boolean) => ({
			workspaceVersion: "v1", files: [], truncated: false, commits: [], summary: { files: 1, additions: 1, deletions: 0 },
			sections: { staged: [], unstaged: [file(path)], untracked: [], committed: [] },
			...(withRepositories ? { repositories: [
				{ name: "11x", repository: "acme/11x", present: true },
				// Freestyle and Coder check the main repository out into a fixed folder.
				{ name: "repository", label: "alice", repository: "acme/alice", main: true, present: true },
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
