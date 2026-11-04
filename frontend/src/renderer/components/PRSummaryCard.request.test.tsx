import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpMergeRequest } from "../lib/cloud-cp";
import { sessionPRDisplaySummaries } from "../lib/pr-display";
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

const prUrl = "https://github.com/owner/repo/pull/7";
const session = {
	id: "s1", workspaceId: "project-1", workspaceName: "project", title: "Login", provider: "codex", kind: "worker",
	status: "working", updatedAt: "2026-10-26T00:00:00Z", cloud: { orgId: "org-1" },
	prs: [{ url: prUrl, number: 7, state: "open", ci: "passing", review: "approved", mergeability: "mergeable", reviewComments: false, updatedAt: "2026-10-26T00:00:00Z" }],
} as unknown as WorkspaceSession;
const [summary] = sessionPRDisplaySummaries(session, undefined);
const prWithHead = { ...summary, headSha: "head-1" };

function renderCard(guest: boolean) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<PRSummaryCard canOpenReviews={false} cloudOrgId="org-1" guest={guest} onOpenReviews={() => undefined} pr={prWithHead} projectId="project-1" sessionId="s1" />
		</QueryClientProvider>,
	);
}

const pending: CloudCpMergeRequest = {
	id: "request-1", projectId: "project-1", sessionId: "s1", sessionName: "Login",
	pullRequest: { id: "pr-1", number: 7, url: prUrl, title: "Login", repository: "owner/repo" },
	requestedBy: { userId: "u1", name: "Vaanya" }, status: "pending", createdAt: "2026-10-26T00:00:00Z",
};

describe("a shared project's pull request card", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		client.listProjectMergeRequests.mockResolvedValue({ mergeRequests: [] });
		client.requestPullRequestMerge.mockResolvedValue({ mergeRequest: pending });
		client.mergePullRequest.mockResolvedValue({ status: "merge_accepted" });
	});

	it("lets a guest ask the owner to merge instead of merging", async () => {
		renderCard(true);
		fireEvent.click(await screen.findByRole("button", { name: "Ask the owner to merge #7" }));
		await waitFor(() => expect(client.requestPullRequestMerge).toHaveBeenCalledWith("org-1", "s1", 7, prUrl));
		expect(client.mergePullRequest).not.toHaveBeenCalled();
	});

	it("shows a guest their pending request", async () => {
		client.listProjectMergeRequests.mockResolvedValue({ mergeRequests: [pending] });
		renderCard(true);
		await waitFor(() => expect(screen.getByRole("button", { name: "Ask the owner to merge #7" }).textContent).toContain("Merge requested"));
		expect((screen.getByRole("button", { name: "Ask the owner to merge #7" }) as HTMLButtonElement).disabled).toBe(true);
	});

	it("still merges directly for the project's own people", async () => {
		renderCard(false);
		fireEvent.click(await screen.findByRole("button", { name: /Merge/ }));
		await waitFor(() => expect(client.mergePullRequest).toHaveBeenCalledWith("org-1", "s1", 7, prUrl, "head-1"));
		expect(client.listProjectMergeRequests).not.toHaveBeenCalled();
	});
});
