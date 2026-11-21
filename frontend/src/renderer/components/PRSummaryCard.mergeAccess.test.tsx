import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionPRDisplaySummaries } from "../lib/pr-display";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { PRSummaryCard } from "./SessionInspector";

const client = vi.hoisted(() => ({
	mergePullRequest: vi.fn(),
	requestPullRequestMerge: vi.fn(),
	listProjectMergeRequests: vi.fn(),
	approveMergeRequest: vi.fn(),
	rejectMergeRequest: vi.fn(),
}));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client, baseUrl: "https://cp.test", ready: true }) }));
vi.mock("../hooks/useCloudSessionLive", () => ({ useCloudSessionLive: () => undefined }));

const prUrl = "https://github.com/upstream/repo/pull/7";
const session = {
	id: "s1", workspaceId: "project-1", workspaceName: "project", title: "Login", provider: "codex", kind: "worker",
	status: "working", updatedAt: "2026-10-26T00:00:00Z", cloud: { orgId: "org-1" },
	prs: [{ url: prUrl, number: 7, state: "open", ci: "passing", review: "approved", mergeability: "mergeable", reviewComments: false, updatedAt: "2026-10-26T00:00:00Z" }],
} as unknown as WorkspaceSession;
const [summary] = sessionPRDisplaySummaries(session, undefined);

function renderCard(pr: SessionPRSummary, cloudOrgId: string | undefined = "org-1") {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<PRSummaryCard canOpenReviews={false} cloudOrgId={cloudOrgId} onOpenReviews={() => undefined} pr={pr} projectId="project-1" sessionId="s1" />
		</QueryClientProvider>,
	);
}

const ready: SessionPRSummary = { ...summary, headSha: "head-1", repo: "upstream/repo" };

describe.each([
	["classic card", { developerMode: false, prView: false }],
	["PR view card", { developerMode: true, prView: true }],
])("a Cloud pull request's merge control (%s)", (_name, uiState) => {
	beforeEach(() => {
		vi.clearAllMocks();
		useUiStore.setState(uiState);
		client.listProjectMergeRequests.mockResolvedValue({ mergeRequests: [] });
	});

	it("merges in AO when the person can write and AO can merge as them", () => {
		renderCard({ ...ready, mergeAccess: { permission: "write", via: "ao" } });
		expect(screen.getByRole("button", { name: "Merge PR #7" })).toBeTruthy();
		expect(screen.queryByText("Merge on GitHub")).toBeNull();
	});

	it("sends a person with write access to GitHub when AO can't merge there", () => {
		renderCard({ ...ready, mergeAccess: { permission: "write", via: "github" } });
		expect(screen.queryByRole("button", { name: "Merge PR #7" })).toBeNull();
		const link = screen.getByRole("link", { name: "Merge PR #7 on GitHub" });
		expect(link.getAttribute("href")).toBe(prUrl);
		expect(link.textContent).toContain("Merge on GitHub");
	});

	it("offers Merge on GitHub only once the PR is ready", () => {
		renderCard({ ...ready, ci: { ...ready.ci, state: "failing" }, mergeAccess: { permission: "write", via: "github" } });
		expect(screen.queryByRole("link", { name: "Merge PR #7 on GitHub" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Merge PR #7" })).toBeNull();
	});

	it("tells a person without write access a maintainer has to merge it, checks and reviews still shown", () => {
		renderCard({ ...ready, ci: { ...ready.ci, state: "failing" }, mergeAccess: { permission: "read", via: "none" } });
		expect(screen.queryByRole("button", { name: "Merge PR #7" })).toBeNull();
		expect(screen.getByText("Waiting for a maintainer to merge")).toBeTruthy();
		const notice = screen.getByTestId("pr-merge-maintainer");
		const open = within(notice).getByRole("link", { name: "Open PR #7" });
		expect(open.textContent).toContain("Open on GitHub");
		expect(open.getAttribute("href")).toBe(prUrl);
		expect(screen.getAllByText(/check/i).length).toBeGreaterThan(0);
	});

	it("keeps today's Merge when the permission is unknown but AO can merge, or the control plane doesn't say", () => {
		const { unmount } = renderCard({ ...ready, mergeAccess: { permission: "unknown", via: "ao" } });
		expect(screen.getByRole("button", { name: "Merge PR #7" })).toBeTruthy();
		unmount();
		renderCard(ready);
		expect(screen.getByRole("button", { name: "Merge PR #7" })).toBeTruthy();
	});

	// The control plane could not read the person's permission (no token of
	// theirs) on a repository AO can't write to: a Merge from AO would only be
	// refused, so the card sends them to GitHub, where their own rights decide.
	it("sends an unknown permission to GitHub when AO can't merge there", () => {
		renderCard({ ...ready, mergeAccess: { permission: "unknown", via: "github" } });
		expect(screen.queryByRole("button", { name: "Merge PR #7" })).toBeNull();
		expect(screen.getByRole("link", { name: /merge.*on github/i }).getAttribute("href")).toBe(prUrl);
		expect(client.mergePullRequest).not.toHaveBeenCalled();
	});
});
