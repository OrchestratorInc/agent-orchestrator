import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { FILE_REFERENCE_RESOLVE_UNSUPPORTED, type ChatFileReference } from "../../shared/chat-file-reference";
import { WorkspaceRequestError } from "./useSessionWorkspaceFiles";
import { CloudCpError, withWorkspaceRepository, type CloudCpClient } from "../lib/cloud-cp";
import {
	cloudFilesReviewDataSource,
	cloudWorkspaceReviewRevisionQueryOptions,
	cloudWorkspaceView,
	resolveCloudFileReference,
	cloudWorkspaceReviewDiffsQueryOptions,
	cloudWorkspaceReviewFileQueryOptions,
	cloudWorkspaceReviewQueryKey,
	cloudWorkspaceReviewSearchQueryOptions,
	invalidateCloudWorkspaceReviewEvent,
} from "./useCloudWorkspaceReview";

describe("Cloud workspace review queries", () => {
	it("keys search by query and limit and forwards cancellation", async () => {
		const searchWorkspaceReview = vi.fn().mockResolvedValue({ results: [], truncated: false });
		const search = cloudWorkspaceReviewSearchQueryOptions({
			client: { searchWorkspaceReview } as unknown as CloudCpClient,
			baseUrl: "https://cloud.example",
			orgId: "org-1",
			sessionId: "session-1",
			query: { query: "app", limit: 20 },
		});
		const controller = new AbortController();
		expect(search.queryKey).toEqual([
			"cloud-workspace-review", "https://cloud.example", "org-1", "session-1", "search", "app", "", 20,
		]);
		await search.queryFn({ signal: controller.signal });
		expect(searchWorkspaceReview).toHaveBeenCalledWith(
			"org-1", "session-1", { query: "app", limit: 20 }, { signal: controller.signal },
		);
	});

	it("keys every review request by control plane, tenant, session, scope, commit, and path", () => {
		const client = {} as CloudCpClient;
		const file = cloudWorkspaceReviewFileQueryOptions({
			client, baseUrl: "https://cloud.example", orgId: "org-1", sessionId: "session-1",
			path: "src/App.tsx", scope: "committed", commitSha: "abc",
		});
		const diffs = cloudWorkspaceReviewDiffsQueryOptions({
			client, baseUrl: "https://cloud.example", orgId: "org-1", sessionId: "session-1",
			paths: ["src/App.tsx"], scope: "staged", contextLines: 5, ignoreWhitespace: true,
			workspaceVersion: "v1",
		});
		expect(file.queryKey).toEqual([
			"cloud-workspace-review", "https://cloud.example", "org-1", "session-1", "file", "committed", "abc", "src/App.tsx",
		]);
		expect(diffs.queryKey).toEqual([
			"cloud-workspace-review", "https://cloud.example", "org-1", "session-1", "diffs", "staged", "", ["src/App.tsx"], 5, true, "v1",
		]);
	});

	it("invalidates only the matching Cloud session on workspace.changed", async () => {
		const queryClient = new QueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
		const key = cloudWorkspaceReviewQueryKey("https://cloud.example", "org-1", "session-1");
		await invalidateCloudWorkspaceReviewEvent(queryClient, key, {
			sessionId: "session-1", sequence: 2, type: "workspace.changed", payload: { path: "README.md" }, createdAt: "2026-09-20T00:00:00Z",
		});
		expect(invalidate).toHaveBeenCalledWith({ queryKey: key });
		invalidate.mockClear();
		await invalidateCloudWorkspaceReviewEvent(queryClient, key, {
			sessionId: "session-1", sequence: 3, type: "chat.message", payload: {}, createdAt: "2026-09-20T00:00:01Z",
		});
		expect(invalidate).not.toHaveBeenCalled();
	});

	it("keys view reads by the view, its fence and its picked commits, and sends the view", async () => {
		const getWorkspaceReviewDiffs = vi.fn().mockResolvedValue({ workspaceVersion: "view-v", groups: [] });
		const client = { getWorkspaceReviewDiffs } as unknown as CloudCpClient;
		const view = { prNumber: 12, sourceUrl: "https://github.com/acme/app/pull/12", expectedTipSha: "t", expectedBaseSha: "b", commitShas: ["c1", "c2"] };
		const diffs = cloudWorkspaceReviewDiffsQueryOptions({ client, baseUrl: "https://cloud.example", orgId: "org-1", sessionId: "session-1", paths: ["a.ts"], scope: "committed", view });
		expect(diffs.queryKey.slice(-2)).toEqual(["view", "pr:https://github.com/acme/app/pull/12@t:b|c1,c2"]);
		await diffs.queryFn();
		expect(getWorkspaceReviewDiffs).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ scope: "committed", ...view }));
		const revision = cloudWorkspaceReviewRevisionQueryOptions({ client, baseUrl: "https://cloud.example", orgId: "org-1", sessionId: "session-1", query: { path: "a.ts", scope: "committed", side: "after", branch: "main", commitShas: ["c1"] } });
		expect(revision.queryKey.slice(-2)).toEqual(["view", "branch:main@:|c1"]);
		// A remote branch reads as origin/<name> on the Cloud routes.
		expect(cloudWorkspaceView({ branch: "refs/remotes/origin/feat/x", expectedTipSha: "t" }, [])).toEqual({ branch: "origin/feat/x", expectedTipSha: "t", expectedBaseSha: undefined, commitShas: [] });
		expect(cloudWorkspaceView(undefined)).toBeUndefined();
	});

	it("lists a Cloud session's branches, histories and commit selections as the Files review reads them", async () => {
		const getWorkspaceBranches = vi.fn().mockResolvedValue({ supported: true, truncated: false, defaultBranch: "main", branches: [{ kind: "remote", name: "origin/feat/x", tipSha: "t", checkedOut: false, isDefault: false }] });
		const getWorkspaceHistory = vi.fn().mockResolvedValue({ view: { kind: "branch", name: "origin/feat/x", tipSha: "t", baseSha: "", baseRef: "origin/main", rangeMode: "since_base" }, commits: [], commitsTruncated: false, rangeSummary: { files: 1, additions: 2, deletions: 0 } });
		const getWorkspaceCommitSelection = vi.fn().mockResolvedValue({ tipSha: "t", contiguous: false, combinable: false, conflict: "c".repeat(40), files: [], summary: { files: 0, additions: 0, deletions: 0 }, truncated: false });
		const client = withWorkspaceRepository({ getWorkspaceBranches, getWorkspaceHistory, getWorkspaceCommitSelection } as unknown as CloudCpClient, "docs");
		const source = cloudFilesReviewDataSource({ client, baseUrl: "https://cloud.example", orgId: "org-1", sessionId: "session-1" });
		const branches = source.branches("no branches");
		expect(branches.queryKey).toEqual(["cloud-workspace-review", "https://cloud.example", "org-1", "session-1", "repository:docs", "branches"]);
		await expect(branches.queryFn()).resolves.toMatchObject({ sessionId: "session-1", supported: true, branches: [{ name: "origin/feat/x" }] });
		const history = source.history("no history", { kind: "branch", name: "refs/remotes/origin/feat/x", tipSha: "t" });
		await expect(history.queryFn()).resolves.toMatchObject({ view: { kind: "branch", tipSha: "t", baseSha: undefined, rangeMode: "since_base" }, rangeSummary: { files: 1 } });
		expect(getWorkspaceHistory).toHaveBeenCalledWith("org-1", "session-1", { branch: "origin/feat/x" }, { repository: "docs" });
		const selection = source.commitSelection({ commitShas: ["c1", "c2"], errorMessage: "no selection", selectionKey: "k", view: { branch: "refs/remotes/origin/feat/x", expectedTipSha: "t" } });
		await expect(selection.queryFn()).resolves.toMatchObject({ combinable: false, conflict: { commitSha: "c".repeat(40), paths: [], reason: "conflict" } });
		expect(getWorkspaceCommitSelection).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ branch: "origin/feat/x", expectedTipSha: "t", commitShas: ["c1", "c2"] }), { repository: "docs" });
		expect(source.checkedOutHistory).toBe(false);
	});

	it("hides the branch menu for a worker or control plane without views, and keeps other failures' codes", async () => {
		for (const status of [501, 404]) {
			const client = { getWorkspaceBranches: vi.fn().mockRejectedValue(new CloudCpError("unsupported", { status, code: status === 501 ? "WORKSPACE_VIEW_UNSUPPORTED" : undefined })) } as unknown as CloudCpClient;
			const source = cloudFilesReviewDataSource({ client, baseUrl: "b", orgId: "o", sessionId: "s" });
			await expect(source.branches("e").queryFn()).resolves.toEqual({ sessionId: "s", supported: false, truncated: false, branches: [] });
		}
		const moved = { getWorkspaceHistory: vi.fn().mockRejectedValue(new CloudCpError("moved", { status: 404, code: "PR_NOT_FOUND" })) } as unknown as CloudCpClient;
		const error = await cloudFilesReviewDataSource({ client: moved, baseUrl: "b", orgId: "o", sessionId: "s" }).history("e", { kind: "pull_request", number: 4, url: "", branch: "x" }).queryFn().catch((caught: unknown) => caught);
		expect(error).toBeInstanceOf(WorkspaceRequestError);
		expect(error).toMatchObject({ code: "PR_NOT_FOUND", status: 404 });
	});

	it("resolves a chat reference through the control plane, naming a sibling repository, and stops asking once unsupported", async () => {
		const reference = { referenceFormat: "numbered-v1", id: "r1", order: 1, sessionId: "s", hostId: "local", path: "../docs/src/a.ts", side: "new", line: 2, endLine: 3, lines: [], source: { kind: "workspace" }, truncated: false } as unknown as ChatFileReference;
		const resolveFileReference = vi.fn().mockResolvedValue({ state: "moved", path: "src/b.ts", line: 4 });
		const resolve = resolveCloudFileReference({ resolveFileReference } as unknown as CloudCpClient, "org", { current: false });
		await expect(resolve(reference)).resolves.toEqual({ state: "moved", path: "../docs/src/b.ts", line: 4 });
		expect(resolveFileReference).toHaveBeenCalledWith("org", "s", expect.objectContaining({ path: "src/a.ts", repository: "docs", id: "r1" }), { signal: undefined });
		expect(resolveFileReference.mock.calls[0][2]).not.toHaveProperty("hostId");

		const unsupported = vi.fn().mockRejectedValue(new CloudCpError("no", { status: 501, code: "WORKSPACE_VIEW_UNSUPPORTED" }));
		const flag = { current: false };
		const old = resolveCloudFileReference({ resolveFileReference: unsupported } as unknown as CloudCpClient, "org", flag);
		await expect(old({ ...reference, path: "a.ts" })).rejects.toThrow(FILE_REFERENCE_RESOLVE_UNSUPPORTED);
		await expect(old({ ...reference, path: "a.ts" })).rejects.toThrow(FILE_REFERENCE_RESOLVE_UNSUPPORTED);
		expect(unsupported).toHaveBeenCalledTimes(1);
		const invalid = resolveCloudFileReference({ resolveFileReference: vi.fn().mockRejectedValue(new CloudCpError("bad", { status: 400, code: "CHAT_FILE_REFERENCE_INVALID" })) } as unknown as CloudCpClient, "org", { current: false });
		await expect(invalid(reference)).rejects.toThrow("CHAT_FILE_REFERENCE_INVALID");
	});
});
