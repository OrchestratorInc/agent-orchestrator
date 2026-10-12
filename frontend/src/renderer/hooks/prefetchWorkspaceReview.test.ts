import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceFilesResponse, WorkspaceFileSummary } from "./useSessionWorkspaceFiles";
import { prefetchDefaultWorkspaceReviewDiffs, sessionWorkspaceDiffsQueryKey } from "./useSessionWorkspaceFiles";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock, GET: getMock },
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

function file(path: string, overrides: Partial<WorkspaceFileSummary> = {}): WorkspaceFileSummary {
	return { path, status: "modified", additions: 1, deletions: 1, size: 40, binary: false, fileFingerprint: `fp:${path}`, ...overrides };
}

function workspace(unstaged: WorkspaceFileSummary[], extra: Partial<WorkspaceFilesResponse> = {}): WorkspaceFilesResponse {
	return {
		sessionId: "sess-1",
		workspaceVersion: "workspace-1",
		files: unstaged,
		sections: { committed: [], staged: [], unstaged, untracked: [] },
		commits: [],
		summary: { additions: 1, deletions: 1, files: unstaged.length },
		truncated: false,
		...extra,
	};
}

const eofPatch = [
	"diff --git a/README.md b/README.md",
	"index 1111111..2222222 100644",
	"--- a/README.md",
	"+++ b/README.md",
	"@@ -3,3 +3,5 @@",
	" line3",
	" line4",
	" line5",
	"+added6",
	"+added7",
	"",
].join("\n");

describe("prefetchDefaultWorkspaceReviewDiffs", () => {
	beforeEach(() => {
		postMock.mockReset();
		getMock.mockReset();
		postMock.mockResolvedValue({
			data: {
				sessionId: "sess-1",
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch: eofPatch, truncated: false, includedPaths: ["README.md"], deferred: [], errors: [] }],
			},
		});
		getMock.mockImplementation(async (_path: string, init: { params: { query: { side: "before" | "after" } } }) => ({
			data: {
				binary: false,
				content: init.params.query.side === "before" ? "line3\nline4\nline5\n" : "line3\nline4\nline5\nadded6\nadded7\n",
				exists: true,
				path: "README.md",
				revision: init.params.query.side,
				sessionId: "sess-1",
				side: init.params.query.side,
				size: 20,
				truncated: false,
				workspaceVersion: "workspace-1",
			},
		}));
	});

	it("skips a files summary that has no review sections", async () => {
		const queryClient = new QueryClient();
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-partial", { sessionId: "sess-partial", files: [file("README.md")] } as WorkspaceFilesResponse);
		expect(postMock).not.toHaveBeenCalled();
	});

	it("warms the default combined diff and the end-of-file contents the review pane waits on", async () => {
		const queryClient = new QueryClient();
		const sessionId = "sess-prefetch-unstaged";
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, sessionId, workspace([file("README.md")]));

		expect(postMock).toHaveBeenCalledTimes(1);
		const body = postMock.mock.calls[0]?.[1]?.body;
		expect(body).toMatchObject({ scope: "combined", paths: ["README.md"], contextLines: 3, workspaceVersion: "workspace-1" });
		expect(queryClient.getQueryData(sessionWorkspaceDiffsQueryKey(sessionId, "combined", ["README.md"], 3, false, "workspace-1"))).toBeTruthy();
		const endOfFile = queryClient.getQueryCache().findAll({ queryKey: ["files-review-end-of-file", sessionId] });
		expect(endOfFile).toHaveLength(1);
		expect(endOfFile[0]?.state.data).toMatchObject({
			oldFile: { contents: "line3\nline4\nline5\n" },
			newFile: { contents: "line3\nline4\nline5\nadded6\nadded7\n" },
		});

		await prefetchDefaultWorkspaceReviewDiffs(queryClient, sessionId, workspace([file("README.md")]));
		expect(postMock).toHaveBeenCalledTimes(1);
	});

	it("warms all combined changes including staged, committed, and untracked paths", async () => {
		const files = [file("unstaged.ts"), file("staged.ts"), file("committed.ts"), file("new.ts", { status: "added" }), file("unchanged.ts", { status: "unmodified" })];
		const data = workspace([files[0]!], {
			files,
			sections: { unstaged: [files[0]!], staged: [files[1]!], committed: [files[2]!], untracked: [files[3]!] },
		});

		await prefetchDefaultWorkspaceReviewDiffs(new QueryClient(), "sess-prefetch-mixed", data);

		expect(postMock.mock.calls[0]?.[1]?.body).toMatchObject({
			scope: "combined",
			paths: files.slice(0, 4).map((entry) => entry.path),
		});
	});

	it("warms the latest commit when there are no combined changes", async () => {
		const committed = { ...file("committed.ts"), editable: false, fileFingerprint: "commit:committed.ts" };
		const data = workspace([], {
			files: [],
			commits: [{ author: "Ada", files: [committed], sha: "commit-1", subject: "Commit", timestamp: "2026-10-03T00:00:00Z" }],
		});

		await prefetchDefaultWorkspaceReviewDiffs(new QueryClient(), "sess-prefetch-commit", data);

		expect(postMock.mock.calls[0]?.[1]?.body).toMatchObject({ scope: "committed", commitSha: "commit-1", paths: ["committed.ts"] });
	});

	it("warms matching 24-file batches and refills an evicted later batch", async () => {
		const files = Array.from({ length: 57 }, (_, index) => file(`src/file-${index}.ts`));
		const data = workspace(files);
		const queryClient = new QueryClient();

		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-prefetch-batches", data);
		expect(postMock.mock.calls.map((call) => call[1]?.body.paths.length)).toEqual([24, 24, 9]);

		queryClient.removeQueries({
			queryKey: sessionWorkspaceDiffsQueryKey("sess-prefetch-batches", "combined", files.slice(24, 48).map((entry) => entry.path), 3, false, "workspace-1"),
			exact: true,
		});
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-prefetch-batches", data);

		expect(postMock).toHaveBeenCalledTimes(4);
		expect(postMock.mock.calls[3]?.[1]?.body.paths).toEqual(files.slice(24, 48).map((entry) => entry.path));
	});

	it("refetches invalidated diffs and full contents when the summary version is unchanged", async () => {
		const queryClient = new QueryClient();
		const sessionId = "sess-prefetch-invalidated";
		const data = workspace([file("README.md")]);
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, sessionId, data);

		await queryClient.invalidateQueries({ queryKey: ["session-workspace-diffs", sessionId], refetchType: "none" });
		await queryClient.invalidateQueries({ queryKey: ["files-review-end-of-file", sessionId], refetchType: "none" });
		getMock.mockImplementation(async (_path: string, init: { params: { query: { side: "before" | "after" } } }) => ({
			data: {
				binary: false,
				content: init.params.query.side === "before" ? "new before\n" : "new after\n",
				exists: true,
				path: "README.md",
				revision: `new-${init.params.query.side}`,
				sessionId,
				side: init.params.query.side,
				size: 10,
				truncated: false,
				workspaceVersion: "workspace-1",
			},
		}));

		await prefetchDefaultWorkspaceReviewDiffs(queryClient, sessionId, data);

		expect(postMock).toHaveBeenCalledTimes(2);
		expect(getMock).toHaveBeenCalledTimes(4);
		const endOfFile = queryClient.getQueryCache().findAll({ queryKey: ["files-review-end-of-file", sessionId] });
		expect(endOfFile[0]?.state.data).toMatchObject({
			oldFile: { contents: "new before\n" },
			newFile: { contents: "new after\n" },
		});
	});

	it("skips lockfiles the review pane defers", async () => {
		const queryClient = new QueryClient();
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-prefetch-lock", workspace([file("package-lock.json", { size: 600_000 })]));
		expect(postMock).not.toHaveBeenCalled();
	});

	it("retries after a failed diff fetch", async () => {
		postMock.mockResolvedValueOnce({ error: { message: "unavailable" } });
		const queryClient = new QueryClient();
		const data = workspace([file("README.md")]);
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-prefetch-retry", data);
		await prefetchDefaultWorkspaceReviewDiffs(queryClient, "sess-prefetch-retry", data);
		expect(postMock).toHaveBeenCalledTimes(2);
	});
});
