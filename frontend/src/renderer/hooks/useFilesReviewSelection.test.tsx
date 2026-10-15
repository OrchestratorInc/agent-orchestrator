import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useFilesReviewSelection } from "./useFilesReviewSelection";
import type { WorkspaceBranches, WorkspaceCommitSummary, WorkspaceFilesResponse, WorkspaceHistoryResponse } from "./useSessionWorkspaceFiles";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: () => ({ GET: getMock, POST: postMock }),
}));

const SESSION = "sess-review";
const SHA = (letter: string) => letter.repeat(40);

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
		{ kind: "pull_request", name: "fix/crash", checkedOut: false, isDefault: false, tipSha: SHA("3"), prNumber: 42, prUrl: "https://github.com/acme/app/pull/42" },
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

function renderSelection(data: WorkspaceFilesResponse | undefined, onBranchChange?: () => void) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	return renderHook(() => useFilesReviewSelection({ sessionId: SESSION, manifest: data, onBranchChange }), { wrapper });
}

const selectionRequests = () => postMock.mock.calls.filter((call) => String(call[0]).endsWith("/workspace/history/selection"));

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
			{ scope: "uncommitted", stats: { additions: 3, deletions: 1 } },
			{ scope: "staged", stats: { additions: 0, deletions: 0 } },
			{ scope: "unstaged", stats: { additions: 3, deletions: 1 } },
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
		expect(result.current.legacy).toBe(false);
	});

	it("reads another branch only through its history and the selection route, fenced by the tip it listed", async () => {
		const onBranchChange = vi.fn();
		const { result } = renderSelection(manifest(), onBranchChange);
		await waitFor(() => expect(result.current.branchMenu?.state).toBe("ready"));

		act(() => result.current.branchMenu?.onSelect("branch:main"));
		expect(useUiStore.getState().inspectorSessions[SESSION]?.filesSelection).toEqual({ branch: { kind: "branch", name: "main", tipSha: SHA("2") }, changes: null });
		expect(onBranchChange).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(result.current.target.state).toBe("ready"));

		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/workspace/history", { params: { path: { sessionId: SESSION }, query: { branch: "main" } } });
		expect(selectionRequests()).toHaveLength(1);
		expect(selectionRequests()[0]?.[1]?.body).toEqual({ branch: "main", expectedTipSha: SHA("2"), commitShas: [] });
		expect(result.current.target).toMatchObject({ key: "branch:main:all", scope: "committed", view: { branch: "main", expectedTipSha: SHA("2") }, openable: false, files: [file("x.ts", 4, 2)] });
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
		expect(result.current.target).toMatchObject({ commitShas: [SHA("a"), SHA("b"), SHA("c")], openable: false, files: [file("x.ts", 4, 2)] });
		expect(result.current.changesMenu.stats).toEqual({ additions: 4, deletions: 2 });
	});

	it("falls back to the checked-out branch with a notice when the picked branch leaves the list", async () => {
		const onBranchChange = vi.fn();
		useUiStore.getState().setFilesSelection(SESSION, { branch: { kind: "branch", name: "deleted" }, changes: null });
		const { result } = renderSelection(manifest(), onBranchChange);

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

		expect(result.current.legacy).toBe(true);
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
});
