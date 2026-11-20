import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { useSessionIssueLink } from "./useSessionIssueLink";

const { getMock, clientForSessionHostMock } = vi.hoisted(() => ({ getMock: vi.fn(), clientForSessionHostMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: clientForSessionHostMock }));

beforeEach(() => {
	vi.clearAllMocks();
	clientForSessionHostMock.mockReturnValue({ GET: getMock });
});
afterEach(cleanup);

const session: WorkspaceSession = {
	id: "issue-session", workspaceId: "project-1", workspaceName: "Project", title: "Issue task",
	provider: "codex", status: "idle", updatedAt: "2026-11-17T00:00:00Z", prs: [], hostId: "host-1",
};

function renderIssueLink(overrides: Partial<WorkspaceSession>) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
	return renderHook(() => useSessionIssueLink({ ...session, ...overrides }), {
		wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
	});
}

describe("useSessionIssueLink", () => {
	it.each([
		["group/repo#7", "https://gitlab.com/group/repo.git", undefined, "https://gitlab.com/group/repo/-/issues/7"],
		["group/sub/repo#7", "git@gitlab.com:group/sub/repo.git", undefined, "https://gitlab.com/group/sub/repo/-/issues/7"],
		["team/app#5", "https://code.acme.test/team/app.git", "github", "https://code.acme.test/team/app/issues/5"],
		["team/app#5", "https://code.example.com/team/app.git", undefined, undefined],
		["team/app#5", undefined, undefined, undefined],
		["team/app#5", "https://github.com/team/app.git", undefined, "https://github.com/team/app/issues/5"],
		["#5", "https://github.com/team/app.git", undefined, "https://github.com/team/app/issues/5"],
	] as const)("looks up the project remote for %s in %s", async (issueId, repo, provider, url) => {
		getMock.mockResolvedValue({ data: { status: "ok", project: { repo, config: { trackerIntake: { provider } } } } });
		const { result } = renderIssueLink({ issueId });
		await waitFor(() => expect(getMock).toHaveBeenCalledWith("/api/v1/projects/{id}", { params: { path: { id: "project-1" } } }));
		await waitFor(() => expect(result.current?.url).toBe(url));
		expect(clientForSessionHostMock).toHaveBeenCalledWith("host-1");
	});

	it.each([
		{ issueId: "github:team/app#5" },
		{ issueId: "Fix the redirect" },
		{ issueId: "team/app#5", cloud: { orgId: "org-1" } },
		{ issueId: "team/app#5", workspaceId: STANDALONE_WORKSPACE_ID },
	])("does not query a project for %j", (overrides) => {
		renderIssueLink(overrides);
		expect(getMock).not.toHaveBeenCalled();
	});
});
