import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { ProjectKind } from "../types/workspace";
import { useFilesReviewSelection } from "./useFilesReviewSelection";
import type { WorkspaceBranch, WorkspaceBranches, WorkspaceCommitSummary, WorkspaceFilesResponse, WorkspaceHistoryResponse } from "./useSessionWorkspaceFiles";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
}));

const SESSION = "sess-review";
const SHA = (letter: string) => letter.repeat(40);
const PR_URL = "https://github.com/acme/app/pull/42";
const CHECKED_OUT = { kind: "checked_out" } as const;

// Complete enough for a commit's files, which always carry editable and a fingerprint.
function file(path: string, additions = 1, deletions = 0): WorkspaceCommitSummary["files"][number] {
	return { path, status: "modified", additions, deletions, size: 10, binary: false, editable: false, fileFingerprint: `fp:${path}` };
}

function commit(letter: string): WorkspaceCommitSummary {
	return { sha: SHA(letter), subject: `Commit ${letter}`, author: "Ada", timestamp: "2026-10-01T00:00:00Z", files: [file(`${letter}.ts`, 2, 1)] };
}

function manifest(extra: Partial<WorkspaceFilesResponse["sections"]> = {}): WorkspaceFilesResponse {
	return {
		sessionId: SESSION,
		workspaceVersion: "v1",
		files: [],
		commits: [],
		summary: { files: 0, additions: 0, deletions: 0 },
		truncated: false,
		sections: { uncommitted: [file("a.ts", 3, 1)], committed: [file("c.ts", 5, 2)], staged: [], unstaged: [file("a.ts", 3, 1)], untracked: [], ...extra },
	};
}

const branches: WorkspaceBranches = {
	sessionId: SESSION,
	supported: true,
	truncated: false,
	branches: [
		{ kind: "local", name: "feature/login", checkedOut: true, isDefault: false, tipSha: SHA("1"), updatedAt: "2026-10-02T00:00:00Z" },
		{ kind: "local", name: "main", checkedOut: false, isDefault: true, tipSha: SHA("2"), updatedAt: "2026-10-01T00:00:00Z" },
		{ kind: "pull_request", name: "fix/crash", checkedOut: false, isDefault: false, tipSha: SHA("3"), prNumber: 42, prUrl: PR_URL },
	],
};

const checkedOutHistory: WorkspaceHistoryResponse = {
	sessionId: SESSION,
	commits: [commit("a"), commit("b"), commit("c"), commit("d")],
	view: { kind: "checked_out", rangeMode: "since_base", tipSha: SHA("1"), baseSha: SHA("9") },
};

const mainHistory: WorkspaceHistoryResponse = {
	sessionId: SESSION,
	commits: [commit("e"), commit("f")],
	rangeSummary: { files: 2, additions: 4, deletions: 2 },
	view: { kind: "branch", name: "main", rangeMode: "recent", tipSha: SHA("2") },
};

function respond({ branchList = branches }: { branchList?: WorkspaceBranches } = {}) {
	getMock.mockImplementation(async (path: string, init: { params: { query?: { branch?: string } } }) => {
		if (path.endsWith("/workspace/branches")) return { data: branchList };
		if (path.endsWith("/workspace/history")) return { data: init.params.query?.branch === "main" ? mainHistory : checkedOutHistory };
		throw new Error(`Unexpected GET ${path}`);
	});
	postMock.mockResolvedValue({
		data: {
			sessionId: SESSION,
			tipSha: SHA("2"),
			contiguous: true,
			combinable: true,
			files: [file("x.ts", 4, 2)],
			summary: { files: 1, additions: 4, deletions: 2 },
			truncated: false,
		},
	});
}

// The workspace queries set their own retry, so the client's default does not reach them.
function renderSelection(data: WorkspaceFilesResponse | undefined, { onBranchChange, projectKind }: { onBranchChange?: () => void; projectKind?: ProjectKind } = {}) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	return renderHook(
		({ sessionId, manifest: current }: { sessionId: string; manifest: WorkspaceFilesResponse | undefined }) => useFilesReviewSelection({ sessionId, manifest: current, projectKind, onBranchChange }),
		{ wrapper, initialProps: { sessionId: SESSION, manifest: data } },
	);
}

function errorResponse(code: string, status: number) {
	return { error: { code, message: code }, response: { status } };
}

const selectionRequests = () => postMock.mock.calls.filter((call) => String(call[0]).endsWith("/workspace/history/selection"));
const requests = (suffix: string) => getMock.mock.calls.filter((call) => String(call[0]).endsWith(suffix));

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	useUiStore.setState({ inspectorSessions: {} });
	respond();
});

describe("useFilesReviewSelection", () => {
	it("opens the checked-out branch on its uncommitted changes with both menus filled", async () => {
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(result.current.changesMenu.commits.state).toBe("ready"));
		await waitFor(() => expect(result.current.branchMenu?.state).toBe("ready"));

		const { changesMenu, branchMenu, target } = result.current;
		expect(changesMenu).toMatchObject({ mode: "checked_out", value: { kind: "uncommitted" }, stats: { additions: 3, deletions: 1 } });
		expect(changesMenu.scopes).toEqual([
			{ scope: "uncommitted", stats: { additions: 3, deletions: 1 }, empty: false },
			// Nothing staged: the menu leaves this row out.
			{ scope: "staged", stats: { additions: 0, deletions: 0 }, empty: true },
			{ scope: "unstaged", stats: { additions: 3, deletions: 1 }, empty: false },
		]);
		expect(changesMenu.commits).toMatchObject({ allStats: { additions: 5, deletions: 2 }, truncated: false, rangeMode: "since_base" });
		expect(changesMenu.commits.rows[0]).toEqual({ sha: SHA("a"), subject: "Commit a", timestamp: "2026-10-01T00:00:00Z", additions: 2, deletions: 1 });
		expect(target).toMatchObject({ scope: "uncommitted", state: "ready", openable: true, workspaceVersion: "v1", selectionKey: "" });
		expect(branchMenu?.trigger).toEqual({ kind: "checked_out", name: "feature/login", detached: undefined });
		expect(branchMenu?.rows.map((row) => [row.id, row.selected])).toEqual([
			["checked_out", true],
			["branch:main", false],
			["pr:https://github.com/acme/app/pull/42", false],
		]);
	});

	it("keeps the sole commit selected when its checkbox is clicked", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path.endsWith("/workspace/branches")) return { data: branches };
			if (path.endsWith("/workspace/history")) return { data: { ...mainHistory, commits: [commit("e")] } };
			throw new Error(`Unexpected GET ${path}`);
		});
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "branch", name: "main" }, changes: null });
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(result.current.changesMenu.commits.state).toBe("ready"));
		act(() => result.current.changesMenu.commits.onToggle(SHA("e")));
		expect(result.current.changesMenu.value).toEqual({ kind: "commits", shas: [SHA("e")] });
		await waitFor(() => expect(result.current.target).toMatchObject({ state: "ready", commitShas: [SHA("e")] }));
		expect(result.current.changesMenu.value).toEqual({ kind: "commits", shas: [SHA("e")] });
		act(() => result.current.changesMenu.commits.onToggleAll());
		expect(result.current.changesMenu.value).toEqual({ kind: "commits", shas: [] });
	});

	it("reads another branch only through its history and the selection route, fenced by the tip it listed", async () => {
		const onBranchChange = vi.fn();
		const { result } = renderSelection(manifest(), { onBranchChange });
		await waitFor(() => expect(result.current.branchMenu?.state).toBe("ready"));

		act(() => result.current.branchMenu?.onSelect("branch:main"));
		expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection).toEqual({ branch: { kind: "branch", name: "main", tipSha: SHA("2") }, changes: null });
		expect(onBranchChange).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(result.current.target.state).toBe("ready"));

		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/history", { params: { path: { sessionId: SESSION }, query: { branch: "main" } } });
		expect(selectionRequests()).toHaveLength(1);
		expect(selectionRequests()[0]?.[1]?.body).toEqual({ branch: "main", expectedTipSha: SHA("2"), commitShas: [] });
		expect(result.current.target).toMatchObject({ key: "branch:main:all", scope: "committed", view: { branch: "main", expectedTipSha: SHA("2") }, openable: true, files: [file("x.ts", 4, 2)] });
		expect(result.current.changesMenu).toMatchObject({ mode: "view", value: { kind: "commits", shas: [] }, stats: { additions: 4, deletions: 2 } });
		expect(result.current.changesMenu.commits.rangeMode).toBe("recent");
		expect(result.current.branchMenu?.trigger).toEqual({ kind: "branch", name: "main" });
		// Picking a branch never writes: only reads leave the renderer.
		expect(postMock.mock.calls.every((call) => String(call[0]).endsWith("/workspace/history/selection"))).toBe(true);
	});

	it("sends a run of ticks to the selection route once", async () => {
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "checked_out" }, changes: { kind: "commits", shas: [] } });
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(result.current.changesMenu.commits.state).toBe("ready"));

		act(() => result.current.changesMenu.commits.onToggle(SHA("c")));
		act(() => result.current.changesMenu.commits.onToggle(SHA("a")));
		act(() => result.current.changesMenu.commits.onToggle(SHA("b")));
		// The menu shows the ticks at once; the review reads them once ticking pauses.
		expect(result.current.changesMenu.value).toEqual({ kind: "commits", shas: [SHA("a"), SHA("b"), SHA("c")] });
		expect(result.current.target.commitShas).toBeUndefined();
		await waitFor(() => expect(result.current.target).toMatchObject({ state: "ready", commitShas: [SHA("a"), SHA("b"), SHA("c")] }));

		expect(selectionRequests()).toHaveLength(1);
		expect(selectionRequests()[0]?.[1]?.body).toEqual({ commitShas: [SHA("a"), SHA("b"), SHA("c")], workspaceVersion: "v1" });
		expect(result.current.target).toMatchObject({ commitShas: [SHA("a"), SHA("b"), SHA("c")], openable: true, files: [file("x.ts", 4, 2)] });
		expect(result.current.changesMenu.stats).toEqual({ additions: 4, deletions: 2 });
	});

	it("shows no totals for ticked commits until they combine, and none when they can't", async () => {
		postMock.mockResolvedValue({
			data: { sessionId: SESSION, tipSha: SHA("2"), contiguous: false, combinable: false, conflict: { commitSha: SHA("a"), paths: ["x.ts"], reason: "conflict" }, files: [], summary: { files: 0, additions: 0, deletions: 0 }, truncated: false },
		});
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "checked_out" }, changes: { kind: "commits", shas: [] } });
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(result.current.changesMenu.commits.state).toBe("ready"));

		act(() => result.current.changesMenu.commits.onToggle(SHA("a")));
		act(() => result.current.changesMenu.commits.onToggle(SHA("c")));
		// Still settling: the rows' sum would count a line changed twice, so nothing shows.
		expect(result.current.changesMenu.stats).toBeUndefined();
		await waitFor(() => expect(result.current.target.state).toBe("not_combinable"));
		expect(result.current.changesMenu.stats).toBeUndefined();
	});

	it("keeps a picked branch a capped list leaves out, until its history says it is gone", async () => {
		const onBranchChange = vi.fn();
		respond({ branchList: { ...branches, truncated: true } });
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "branch", name: "old-branch" }, changes: null });
		const { result } = renderSelection(manifest(), { onBranchChange });

		await waitFor(() => expect(result.current.branchMenu?.state).toBe("ready"));
		await waitFor(() => expect(requests("/workspace/history").some((call) => call[1]?.params?.query?.branch === "old-branch")).toBe(true));
		expect(result.current.notice).toBe("");
		expect(onBranchChange).not.toHaveBeenCalled();
		expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection?.branch).toEqual({ kind: "branch", name: "old-branch" });
		expect(result.current.changesMenu.mode).toBe("view");

		// Deleted after all: its own history read finds out, and the pick falls back.
		getMock.mockImplementation(async (path: string, init: { params: { query?: { branch?: string } } }) => {
			if (path.endsWith("/workspace/branches")) return { data: { ...branches, truncated: true } };
			if (path.endsWith("/workspace/history") && init.params.query?.branch === "gone") return errorResponse("WORKSPACE_BRANCH_NOT_FOUND", 404);
			if (path.endsWith("/workspace/history")) return { data: checkedOutHistory };
			throw new Error(`Unexpected GET ${path}`);
		});
		act(() => useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "branch", name: "gone" }, changes: null }));
		await waitFor(() => expect(result.current.notice).toBe("gone is no longer available; showing the checked-out branch."));
		expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection).toBeUndefined();
		expect(onBranchChange).toHaveBeenCalledTimes(1);
	});

	it("falls back to the checked-out branch with a notice when the picked branch leaves the list", async () => {
		const onBranchChange = vi.fn();
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "branch", name: "deleted" }, changes: null });
		const { result } = renderSelection(manifest(), { onBranchChange });

		await waitFor(() => expect(result.current.notice).toBe("deleted is no longer available; showing the checked-out branch."));
		expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection).toBeUndefined();
		expect(result.current.changesMenu.mode).toBe("checked_out");
		expect(onBranchChange).toHaveBeenCalled();

		act(() => result.current.changesMenu.onSelectScope("staged"));
		expect(result.current.notice).toBe("");
	});

	it("keeps an older daemon on combined Changes without asking for branches", async () => {
		const legacy = manifest();
		delete legacy.sections.uncommitted;
		const { result } = renderSelection(legacy);
		await waitFor(() => expect(result.current.changesMenu.commits.state).toBe("ready"));

		expect(result.current.branchMenu).toBeNull();
		expect(result.current.changesMenu.value).toEqual({ kind: "combined" });
		expect(result.current.changesMenu.scopes.map((option) => option.scope)).toEqual(["combined", "staged", "unstaged"]);
		expect(getMock.mock.calls.some((call) => String(call[0]).endsWith("/workspace/branches"))).toBe(false);
	});

	it("hides the branch menu for a scratch session", async () => {
		respond({ branchList: { sessionId: SESSION, supported: false, truncated: false, branches: [] } });
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(getMock.mock.calls.some((call) => String(call[0]).endsWith("/workspace/branches"))).toBe(true));
		await waitFor(() => expect(result.current.branchMenu).toBeNull());
	});

	it("follows a viewed PR to its branch, quietly, once that branch is local", async () => {
		const onBranchChange = vi.fn();
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "pull_request", number: 42, url: PR_URL, branch: "fix/crash", tipSha: SHA("3") }, changes: { kind: "commits", shas: [SHA("a")] } });
		const local: WorkspaceBranch = { kind: "local", name: "fix/crash", checkedOut: false, isDefault: false, tipSha: SHA("3"), prNumber: 42, prUrl: PR_URL };
		respond({ branchList: { ...branches, branches: [branches.branches[0], branches.branches[1], local] } });
		const { result } = renderSelection(manifest(), { onBranchChange });

		await waitFor(() => expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection).toEqual({ branch: { kind: "branch", name: "fix/crash", tipSha: SHA("3") }, changes: { kind: "commits", shas: [] } }));
		expect(result.current.notice).toBe("");
		expect(onBranchChange).toHaveBeenCalledTimes(1);
		expect(result.current.branchMenu?.trigger).toEqual({ kind: "branch", name: "fix/crash" });
	});

	it("never sends one session's ticks to the next session", async () => {
		const other = "sess-other";
		useUiStore.getState().setFilesSelection(SESSION, { branch: CHECKED_OUT, changes: { kind: "commits", shas: [SHA("a"), SHA("b")] } });
		useUiStore.getState().setFilesSelection(other, { branch: CHECKED_OUT, changes: { kind: "commits", shas: [] } });
		const { result, rerender } = renderSelection(manifest());
		await waitFor(() => expect(result.current.target).toMatchObject({ state: "ready", commitShas: [SHA("a"), SHA("b")] }));

		rerender({ sessionId: other, manifest: manifest() });
		// Never reuse the old session's ticks while resolving the new snapshot tip.
  expect(result.current.target).toMatchObject({state:"pending",commitShas:undefined});
  await waitFor(()=>expect(result.current.target).toMatchObject({state:"ready",commitShas:undefined,files:[file("c.ts",5,2)]}));
		await act(() => new Promise((resolve) => setTimeout(resolve, 300)));
		expect(selectionRequests().map((call) => call[1]?.params.path.sessionId)).toEqual([SESSION]);
	});

	it("offers a workspace project only Uncommitted, the one scope its manifest fills", async () => {
		useUiStore.getState().setFilesSelection(SESSION, { branch: CHECKED_OUT, changes: { kind: "staged" } });
		const { result } = renderSelection(manifest(), { projectKind: "workspace" });
		await waitFor(() => expect(result.current.branchMenu?.state).toBe("ready"));

		expect(result.current.changesMenu.scopes).toEqual([{ scope: "uncommitted", stats: { additions: 3, deletions: 1 }, empty: false }]);
		expect(result.current.changesMenu).toMatchObject({ commitsAvailable: false, value: { kind: "uncommitted" } });
		expect(result.current.target).toMatchObject({ scope: "uncommitted", state: "ready", files: [file("a.ts", 3, 1)] });
		// Its pull requests are still listed to view.
		expect(result.current.branchMenu?.rows.map((row) => row.id)).toContain(`pr:${PR_URL}`);
		expect(requests("/workspace/history")).toHaveLength(0);
	});

	it("offers a scratch session only its uncommitted files, with no commits or branches", async () => {
		useUiStore.getState().setFilesSelection(SESSION, { branch: CHECKED_OUT, changes: { kind: "commits", shas: [SHA("a")] } });
		const { result } = renderSelection(manifest({ committed: [] }), { projectKind: "scratch" });

		expect(result.current.branchMenu).toBeNull();
		expect(result.current.changesMenu.scopes.map((option) => option.scope)).toEqual(["uncommitted"]);
		expect(result.current.changesMenu).toMatchObject({ commitsAvailable: false, value: { kind: "uncommitted" } });
		expect(result.current.target).toMatchObject({ scope: "uncommitted", state: "ready" });
		await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("shows no branch menu before the manifest, nor before an unknown session says it has branches", async () => {
		let resolveBranches: (value: { data: WorkspaceBranches }) => void = () => {};
		getMock.mockImplementation(async (path: string) => {
			if (path.endsWith("/workspace/branches")) return new Promise((resolve) => { resolveBranches = resolve; });
			return { data: checkedOutHistory };
		});
		const { result, rerender } = renderSelection(undefined);
		expect(result.current.branchMenu).toBeNull();

		rerender({ sessionId: SESSION, manifest: manifest() });
		await waitFor(() => expect(requests("/workspace/branches")).toHaveLength(1));
		// A scratch session would answer that it has none, so nothing shows meanwhile.
		expect(result.current.branchMenu).toBeNull();
		act(() => resolveBranches({ data: branches }));
		await waitFor(() => expect(result.current.branchMenu?.state).toBe("ready"));
	});

	it("shows a single repository's branch menu as soon as its manifest arrives", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path.endsWith("/workspace/branches")) return new Promise(() => {});
			return { data: checkedOutHistory };
		});
		const { result } = renderSelection(manifest(), { projectKind: "single_repo" });
		expect(result.current.branchMenu).toMatchObject({ state: "loading", trigger: { kind: "checked_out" } });
	});

	it("reads combined commits again on Retry", async () => {
		useUiStore.getState().setFilesSelection(SESSION, { branch: CHECKED_OUT, changes: { kind: "commits", shas: [SHA("a"), SHA("b")] } });
		const ok = await postMock.getMockImplementation()?.();
		postMock.mockReset().mockResolvedValueOnce(errorResponse("WORKSPACE_SNAPSHOT_STALE", 409)).mockResolvedValue(ok);
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(result.current.target.state).toBe("error"));
		expect(selectionRequests()).toHaveLength(1);

		act(() => result.current.retryTarget());
		await waitFor(() => expect(result.current.target).toMatchObject({ state: "ready", files: [file("x.ts", 4, 2)] }));
		expect(selectionRequests()).toHaveLength(2);
	});

	it("reads a branch whose tip moved again at the new tip, without retrying the fenced read", async () => {
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "branch", name: "main", tipSha: SHA("2") }, changes: null });
		let moved = false;
		const movedBranches: WorkspaceBranches = { ...branches, branches: branches.branches.map((row) => (row.name === "main" ? { ...row, tipSha: SHA("5") } : row)) };
		const movedMainHistory: WorkspaceHistoryResponse = { ...mainHistory, view: { kind: "branch", name: "main", rangeMode: "recent", tipSha: SHA("5") } };
		getMock.mockImplementation(async (path: string, init: { params: { query?: { branch?: string } } }) => {
			if (path.endsWith("/workspace/branches")) return { data: moved ? movedBranches : branches };
			return { data: init.params.query?.branch === "main" ? (moved ? movedMainHistory : mainHistory) : checkedOutHistory };
		});
		const ok = await postMock.getMockImplementation()?.();
		postMock.mockReset().mockImplementationOnce(async () => {
			moved = true;
			return errorResponse("WORKSPACE_REF_MOVED", 409);
		}).mockResolvedValue(ok);
		const { result } = renderSelection(manifest());

		// A retry of the 409 would hold the pane for the whole backoff before this.
		await waitFor(() => expect(result.current.target).toMatchObject({ state: "ready", view: { branch: "main", expectedTipSha: SHA("5") } }));
		expect(selectionRequests().map((call) => call[1]?.body.expectedTipSha)).toEqual([SHA("2"), SHA("5")]);
		expect(requests("/workspace/branches").length).toBeGreaterThan(1);
	});

	it.each([
		{ name: "a branch", branch: { kind: "branch" as const, name: "main", tipSha: SHA("2") }, code: "WORKSPACE_BRANCH_NOT_FOUND", notice: "main is no longer available; showing the checked-out branch." },
		{ name: "a PR", branch: { kind: "pull_request" as const, number: 42, url: PR_URL, branch: "fix/crash", tipSha: SHA("3") }, code: "PR_SOURCE_UNAVAILABLE", notice: "#42 · fix/crash is no longer available; showing the checked-out branch." },
	])("falls back when the history of $name says it is gone, though it is still listed", async ({ branch, code, notice }) => {
		const onBranchChange = vi.fn();
		useUiStore.getState().setFilesSelection(SESSION, { branch, changes: null });
		getMock.mockImplementation(async (path: string, init: { params: { query?: Record<string, unknown> } }) => {
			if (path.endsWith("/workspace/branches")) return { data: branches };
			return init.params.query ? errorResponse(code, 404) : { data: checkedOutHistory };
		});
		const { result } = renderSelection(manifest(), { onBranchChange });

		await waitFor(() => expect(result.current.notice).toBe(notice));
		expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection).toBeUndefined();
		expect(onBranchChange).toHaveBeenCalled();
	});

	it("hides the branch menu for a daemon without the branches route, but not for a session 404", async () => {
		getMock.mockImplementation(async (path: string) => (path.endsWith("/workspace/branches") ? errorResponse("NOT_FOUND", 404) : { data: checkedOutHistory }));
		const missing = renderSelection(manifest());
		await waitFor(() => expect(requests("/workspace/branches")).toHaveLength(1));
		await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
		expect(missing.result.current.branchMenu).toBeNull();
		missing.unmount();

		getMock.mockImplementation(async (path: string) => (path.endsWith("/workspace/branches") ? errorResponse("SESSION_NOT_FOUND", 404) : { data: checkedOutHistory }));
		const { result } = renderSelection(manifest());
		await waitFor(() => expect(result.current.branchMenu?.state).toBe("error"));
	});
});
