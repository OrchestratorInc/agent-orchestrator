import { describe, expect, it } from "vitest";
import type { FilesBranchTarget, WorkspaceBranch, WorkspaceCommitSummary, WorkspaceFilesResponse, WorkspaceFileSummary, WorkspaceHistoryResponse } from "../hooks/useSessionWorkspaceFiles";
import {
	ALL_COMMITS,
	CHECKED_OUT_BRANCH,
	branchRowId,
	branchTargetOf,
	buildReviewTarget,
	commitSelectionRequest,
	defaultChanges,
	filesProjectKind,
	findBranchRow,
	followsDefaultSelection,
	isLegacyManifest,
	normalizeChanges,
	scopeFiles,
	shaSetHash,
	ticksIncludingBetween,
	toggleCommit,
	viewedKey,
	workspaceSelectionKey,
	workspaceViewParams,
} from "./files-review-selection";

const SHA_A = "a".repeat(40);
const SHA_B = "b".repeat(40);
const SHA_C = "c".repeat(40);
const BRANCH: FilesBranchTarget = { kind: "branch", name: "feature/login", tipSha: SHA_A };
const PR: FilesBranchTarget = { kind: "pull_request", number: 42, url: "https://github.com/acme/app/pull/42", branch: "fix/crash" };

// Complete enough for a commit's files, which always carry editable and a fingerprint.
function file(path: string, overrides: Partial<WorkspaceFileSummary> = {}): WorkspaceCommitSummary["files"][number] {
	return { path, status: "modified", additions: 2, deletions: 1, size: 10, binary: false, editable: false, fileFingerprint: `fp:${path}`, ...overrides };
}

function manifest(sections: Partial<WorkspaceFilesResponse["sections"]> = {}, extra: Partial<WorkspaceFilesResponse> = {}): WorkspaceFilesResponse {
	return {
		sessionId: "sess-1",
		workspaceVersion: "v1",
		files: [],
		commits: [],
		summary: { files: 0, additions: 0, deletions: 0 },
		truncated: false,
		sections: { uncommitted: [], committed: [], staged: [], unstaged: [], untracked: [], ...sections },
		...extra,
	};
}

function legacyManifest(sections: Partial<WorkspaceFilesResponse["sections"]> = {}, extra: Partial<WorkspaceFilesResponse> = {}): WorkspaceFilesResponse {
	const data = manifest(sections, extra);
	delete data.sections.uncommitted;
	return data;
}

function commit(sha: string, files: WorkspaceCommitSummary["files"] = [file(`${sha.slice(0, 1)}.ts`)]): WorkspaceCommitSummary {
	return { sha, subject: `Commit ${sha.slice(0, 1)}`, author: "Ada", timestamp: "2026-10-01T00:00:00Z", files };
}

function history(commits: WorkspaceCommitSummary[], extra: Partial<WorkspaceHistoryResponse> = {}): WorkspaceHistoryResponse {
	return { sessionId: "sess-1", commits, ...extra };
}

describe("defaults", () => {
	it("opens on uncommitted changes when the worktree is dirty", () => {
		expect(defaultChanges(CHECKED_OUT_BRANCH, manifest({ uncommitted: [file("a.ts")], committed: [file("b.ts")] }), false)).toEqual({ kind: "uncommitted" });
	});

	it("opens on All commits when the worktree is clean and the branch has commits", () => {
		expect(defaultChanges(CHECKED_OUT_BRANCH, manifest({ committed: [file("b.ts")] }), false)).toBe(ALL_COMMITS);
	});

	it("opens on the empty uncommitted changes when there is nothing else", () => {
		expect(defaultChanges(CHECKED_OUT_BRANCH, manifest(), false)).toEqual({ kind: "uncommitted" });
	});

	it("opens another branch or a PR on All commits", () => {
		expect(defaultChanges(BRANCH, manifest({ uncommitted: [file("a.ts")] }), false)).toBe(ALL_COMMITS);
		expect(defaultChanges(PR, undefined, false)).toBe(ALL_COMMITS);
	});

	it("keeps the combined Changes scope on an older daemon or a degraded manifest", () => {
		expect(isLegacyManifest(legacyManifest())).toBe(true);
		expect(isLegacyManifest(manifest({}, { degraded: true }))).toBe(true);
		expect(isLegacyManifest(manifest())).toBe(false);
		expect(isLegacyManifest(undefined)).toBe(false);
		expect(defaultChanges(CHECKED_OUT_BRANCH, legacyManifest({ committed: [file("b.ts")] }), true)).toEqual({ kind: "combined" });
	});

	it("prefetches only while nothing is picked", () => {
		expect(followsDefaultSelection(undefined)).toBe(true);
		expect(followsDefaultSelection({ branch: CHECKED_OUT_BRANCH, changes: null })).toBe(true);
		expect(followsDefaultSelection({ branch: CHECKED_OUT_BRANCH, changes: { kind: "staged" } })).toBe(false);
		expect(followsDefaultSelection({ branch: BRANCH, changes: null })).toBe(false);
	});
});

describe("normalizeChanges", () => {
	const listed = history([commit(SHA_A), commit(SHA_B), commit(SHA_C)]);

	it("follows the data until the user picks", () => {
		const selection = { branch: CHECKED_OUT_BRANCH, changes: null };
		expect(normalizeChanges(selection, manifest({ uncommitted: [file("a.ts")] }), false)).toEqual({ kind: "uncommitted" });
		expect(normalizeChanges(selection, manifest({ committed: [file("a.ts")] }), false)).toBe(ALL_COMMITS);
	});

	it("keeps an empty working scope picked", () => {
		const staged = { kind: "staged" } as const;
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: staged }, manifest({ uncommitted: [file("a.ts")] }), false)).toBe(staged);
	});

	it("drops ticked SHAs the history no longer lists, and falls back to All when none are left", () => {
		const picked = { kind: "commits", shas: [SHA_A, "d".repeat(40)] } as const;
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: picked }, manifest(), false, listed)).toEqual({ kind: "commits", shas: [SHA_A] });
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: { kind: "commits", shas: ["d".repeat(40)] } }, manifest(), false, listed)).toBe(ALL_COMMITS);
	});

	it("keeps ticks while history loads, and keeps an unchanged pick's identity", () => {
		const picked = { kind: "commits", shas: [SHA_B, SHA_A] } as const;
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: picked }, manifest(), false)).toBe(picked);
		const ordered = { kind: "commits", shas: [SHA_A, SHA_B] } as const;
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: ordered }, manifest(), false, listed)).toBe(ordered);
	});

	it("preserves explicit picks even when every listed commit is selected", () => {
		const all = { kind: "commits", shas: [SHA_A, SHA_B, SHA_C] } as const;
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: all }, manifest(), false, listed)).toBe(all);
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: all }, manifest(), false, { ...listed, commitsTruncated: true })).toBe(all);
	});

	it("selects the only commit without reverting to All commits", () => {
		const onlyCommit = { commits: [listed.commits[0]], commitsTruncated: false };
		const picked = toggleCommit(ALL_COMMITS, SHA_A, [SHA_A], false);
		expect(normalizeChanges({ branch: BRANCH, changes: picked }, manifest(), false, onlyCommit)).toEqual({ kind: "commits", shas: [SHA_A] });
		expect(toggleCommit(picked, SHA_A, [SHA_A], false)).toBe(ALL_COMMITS);
	});

	it("reads a working scope on another branch as All commits", () => {
		expect(normalizeChanges({ branch: BRANCH, changes: { kind: "uncommitted" } }, manifest(), false)).toBe(ALL_COMMITS);
	});

	it("maps Uncommitted and Changes onto what the daemon offers", () => {
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: { kind: "uncommitted" } }, legacyManifest(), true)).toEqual({ kind: "combined" });
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: { kind: "combined" } }, manifest(), false)).toEqual({ kind: "uncommitted" });
	});

	it("keeps one commit on an older daemon", () => {
		expect(normalizeChanges({ branch: CHECKED_OUT_BRANCH, changes: { kind: "commits", shas: [SHA_B, SHA_C] } }, legacyManifest(), true, listed)).toEqual({ kind: "commits", shas: [SHA_B] });
	});
});

describe("toggleCommit", () => {
	const listed = [SHA_A, SHA_B, SHA_C];

	it("ticks All by clearing the other ticks, and leaves All alone when it is the only tick", () => {
		expect(toggleCommit({ kind: "commits", shas: [SHA_A, SHA_B] }, undefined, listed, false)).toBe(ALL_COMMITS);
		expect(toggleCommit(ALL_COMMITS, undefined, listed, false)).toBe(ALL_COMMITS);
		expect(toggleCommit({ kind: "uncommitted" }, undefined, listed, false)).toBe(ALL_COMMITS);
	});

	it("picks only the ticked commit while All or a scope is on", () => {
		expect(toggleCommit(ALL_COMMITS, SHA_B, listed, false)).toEqual({ kind: "commits", shas: [SHA_B] });
		expect(toggleCommit({ kind: "staged" }, SHA_B, listed, false)).toEqual({ kind: "commits", shas: [SHA_B] });
	});

	it("adds ticks in history order and returns to All when the last is unticked", () => {
		const two = toggleCommit({ kind: "commits", shas: [SHA_C] }, SHA_A, listed, false);
		expect(two).toEqual({ kind: "commits", shas: [SHA_A, SHA_C] });
		const one = toggleCommit(two, SHA_A, listed, false);
		expect(one).toEqual({ kind: "commits", shas: [SHA_C] });
		expect(toggleCommit(one, SHA_C, listed, false)).toBe(ALL_COMMITS);
	});

	it("replaces the tick on an older daemon", () => {
		expect(toggleCommit({ kind: "commits", shas: [SHA_A] }, SHA_C, listed, true)).toEqual({ kind: "commits", shas: [SHA_C] });
	});

	it("includes every listed commit between the oldest and newest tick", () => {
		expect(ticksIncludingBetween([SHA_C, SHA_A], [SHA_A, SHA_B, SHA_C, "d".repeat(40)])).toEqual([SHA_A, SHA_B, SHA_C]);
		expect(ticksIncludingBetween([SHA_B], listed)).toEqual([SHA_B]);
	});
});

describe("keys", () => {
	it("stores viewed marks under the keys used before branches, and per branch or PR", () => {
		expect(viewedKey(CHECKED_OUT_BRANCH, { kind: "uncommitted" })).toBe("uncommitted");
		expect(viewedKey(CHECKED_OUT_BRANCH, { kind: "staged" })).toBe("staged");
		expect(viewedKey(CHECKED_OUT_BRANCH, { kind: "combined" })).toBe("combined");
		expect(viewedKey(CHECKED_OUT_BRANCH, ALL_COMMITS)).toBe("committed");
		expect(viewedKey(CHECKED_OUT_BRANCH, { kind: "commits", shas: [SHA_A] })).toBe(`commit:${SHA_A}`);
		expect(viewedKey(CHECKED_OUT_BRANCH, { kind: "commits", shas: [SHA_A, SHA_B] })).toBe(`commits:${shaSetHash([SHA_A, SHA_B])}`);
		expect(viewedKey(BRANCH, ALL_COMMITS)).toBe("branch:feature/login:all");
		expect(viewedKey(BRANCH, { kind: "commits", shas: [SHA_A] })).toBe(`branch:feature/login:commit:${SHA_A}`);
		expect(viewedKey(PR, { kind: "commits", shas: [SHA_A, SHA_B] })).toBe(`pr:42:commits:${shaSetHash([SHA_A, SHA_B])}`);
	});

	it("names a set of commits regardless of order", () => {
		expect(shaSetHash([SHA_A, SHA_B])).toBe(shaSetHash([SHA_B, SHA_A]));
		expect(shaSetHash([SHA_A, SHA_B])).not.toBe(shaSetHash([SHA_A, SHA_C]));
		expect(shaSetHash([SHA_A])).toMatch(/^[0-9a-f]{8}$/);
	});

	it("leaves the worktree's own selection keys empty and names views with their fence", () => {
		expect(workspaceSelectionKey(undefined)).toBe("");
		expect(workspaceSelectionKey(undefined, [SHA_A])).toBe(`|${SHA_A}`);
		const view = workspaceViewParams(BRANCH, { kind: "branch", rangeMode: "since_base", tipSha: SHA_A, baseSha: SHA_C });
		expect(view).toEqual({ branch: "feature/login", expectedTipSha: SHA_A, expectedBaseSha: SHA_C });
		expect(workspaceSelectionKey(view)).toBe(`branch:feature/login@${SHA_A}:${SHA_C}|`);
		expect(workspaceSelectionKey(workspaceViewParams(PR, { kind: "pull_request", rangeMode: "since_base", tipSha: SHA_B }), [SHA_A, SHA_B]))
			.toBe(`pr:https://github.com/acme/app/pull/42@${SHA_B}:|${SHA_A},${SHA_B}`);
		expect(workspaceViewParams(CHECKED_OUT_BRANCH)).toBeUndefined();
	});
});

describe("scopeFiles", () => {
	it("lists untracked files under Unstaged, sorted with the tracked ones", () => {
		const data = manifest({ unstaged: [file("b.ts"), file("d.ts")], untracked: [file("a.ts", { status: "added" }), file("c.ts", { status: "added" })] });
		expect(scopeFiles(data, "unstaged", false).map((entry) => entry.path)).toEqual(["a.ts", "b.ts", "c.ts", "d.ts"]);
	});

	it("leaves untracked files to the combined scope on an older daemon, whose Unstaged diff skips them", () => {
		const data = legacyManifest({ unstaged: [file("b.ts")], untracked: [file("a.ts", { status: "added" })] }, { files: [file("a.ts", { status: "added" }), file("b.ts"), file("same.ts", { status: "unmodified" })] });
		expect(scopeFiles(data, "unstaged", true).map((entry) => entry.path)).toEqual(["b.ts"]);
		expect(scopeFiles(data, "combined", true).map((entry) => entry.path)).toEqual(["a.ts", "b.ts"]);
	});
});

describe("buildReviewTarget", () => {
	it("reads Uncommitted from the manifest and fences it with the manifest version", () => {
		const data = manifest({ uncommitted: [file("a.ts")] });
		expect(buildReviewTarget({ branch: CHECKED_OUT_BRANCH, changes: { kind: "uncommitted" }, manifest: data, legacy: false })).toMatchObject({
			key: "uncommitted",
			scope: "uncommitted",
			commitShas: undefined,
			view: undefined,
			workspaceVersion: "v1",
			selectionKey: "",
			files: [file("a.ts")],
			openable: true,
			state: "ready",
		});
	});

	it("reads one commit's files from its history and stays openable", () => {
		const target = buildReviewTarget({ branch: CHECKED_OUT_BRANCH, changes: { kind: "commits", shas: [SHA_B] }, manifest: manifest(), legacy: false, history: history([commit(SHA_A), commit(SHA_B)]) });
		expect(target).toMatchObject({ scope: "committed", commitShas: [SHA_B], sha: SHA_B, subject: "Commit b", files: [file("b.ts")], openable: true, state: "ready", selectionKey: `|${SHA_B}` });
	});

	it("waits for the selection route for several commits, and reports commits that cannot combine", () => {
		const changes = { kind: "commits", shas: [SHA_A, SHA_C] } as const;
		const listed = history([commit(SHA_A), commit(SHA_B), commit(SHA_C)]);
		const input = { branch: CHECKED_OUT_BRANCH, changes, manifest: manifest(), legacy: false, history: listed };
		const pending = buildReviewTarget({ ...input, selection: { pending: true } });
		expect(pending).toMatchObject({ state: "pending", openable: true });
		expect(pending.files).toBeUndefined();
		const conflict = { commitSha: SHA_A, paths: ["a.ts"], reason: "conflict" as const };
		expect(buildReviewTarget({ ...input, selection: { pending: false, data: { sessionId: "sess-1", tipSha: SHA_A, contiguous: false, combinable: false, conflict, files: [], summary: { files: 0, additions: 0, deletions: 0 }, truncated: false } } }))
			.toMatchObject({ state: "not_combinable", conflict: { ...conflict, subject: "Commit a" } });
		const error = new Error("boom");
		expect(buildReviewTarget({ ...input, selection: { pending: false, error } })).toMatchObject({ state: "error", error });
	});

	it("reads another branch through its view, fenced by its history, and preserves it when opening in the centre", () => {
		const viewHistory = history([commit(SHA_A)], { view: { kind: "branch", name: "feature/login", rangeMode: "since_base", tipSha: SHA_A, baseSha: SHA_C } });
		expect(buildReviewTarget({ branch: BRANCH, changes: ALL_COMMITS, manifest: manifest(), legacy: false })).toMatchObject({ state: "pending" });
		const target = buildReviewTarget({
			branch: BRANCH,
			changes: ALL_COMMITS,
			manifest: manifest(),
			legacy: false,
			history: viewHistory,
			selection: { pending: false, data: { sessionId: "sess-1", tipSha: SHA_A, contiguous: true, combinable: true, files: [file("x.ts")], summary: { files: 1, additions: 2, deletions: 1 }, truncated: false } },
		});
		expect(target).toMatchObject({
			key: "branch:feature/login:all",
			scope: "committed",
			view: { branch: "feature/login", expectedTipSha: SHA_A, expectedBaseSha: SHA_C },
			workspaceVersion: undefined,
			selectionKey: `branch:feature/login@${SHA_A}:${SHA_C}|`,
			files: [file("x.ts")],
			openable: true,
			state: "ready",
		});
		const historyError = new Error("gone");
		expect(buildReviewTarget({ branch: BRANCH, changes: ALL_COMMITS, manifest: manifest(), legacy: false, historyError })).toMatchObject({ state: "error", error: historyError });
	});

	it("asks the selection route only for what one listed commit cannot answer", () => {
		const data = manifest();
		const viewHistory = history([commit(SHA_A)], { view: { kind: "branch", rangeMode: "recent", tipSha: SHA_A } });
		expect(commitSelectionRequest(CHECKED_OUT_BRANCH, ALL_COMMITS, data, undefined)).toBeUndefined();
		expect(commitSelectionRequest(CHECKED_OUT_BRANCH, { kind: "commits", shas: [SHA_A] }, data, undefined)).toBeUndefined();
		expect(commitSelectionRequest(CHECKED_OUT_BRANCH, { kind: "commits", shas: [SHA_A, SHA_B] }, data, undefined))
			.toEqual({ commitShas: [SHA_A, SHA_B], workspaceVersion: "v1", selectionKey: `|${SHA_A},${SHA_B}` });
		expect(commitSelectionRequest(BRANCH, ALL_COMMITS, data, undefined)).toBeUndefined();
		expect(commitSelectionRequest(BRANCH, { kind: "commits", shas: [SHA_A] }, data, viewHistory)).toBeUndefined();
		expect(commitSelectionRequest(BRANCH, ALL_COMMITS, data, viewHistory))
			.toEqual({ view: { branch: "feature/login", expectedTipSha: SHA_A, expectedBaseSha: undefined }, commitShas: [], selectionKey: `branch:feature/login@${SHA_A}:|` });
	});
});

describe("branch rows", () => {
	function row(overrides: Partial<WorkspaceBranch>): WorkspaceBranch {
		return { kind: "local", name: "main", checkedOut: false, isDefault: false, tipSha: SHA_A, ...overrides };
	}

	it("reads the checked-out row as the worktree, a local row as a branch, and a PR row as a PR", () => {
		const checkedOut = row({ name: "feature/login", checkedOut: true });
		const local = row({ name: "main", tipSha: SHA_B });
		const pr = row({ kind: "pull_request", name: "fix/crash", prNumber: 42, prUrl: "https://github.com/acme/app/pull/42", tipSha: SHA_C });
		expect([checkedOut, local, pr].map(branchRowId)).toEqual(["checked_out", "branch:main", "pr:https://github.com/acme/app/pull/42"]);
		expect(branchTargetOf(checkedOut)).toBe(CHECKED_OUT_BRANCH);
		expect(branchTargetOf(local)).toEqual({ kind: "branch", name: "main", tipSha: SHA_B });
		expect(branchTargetOf(pr)).toEqual({ kind: "pull_request", number: 42, url: "https://github.com/acme/app/pull/42", branch: "fix/crash", tipSha: SHA_C });
		const rows = [checkedOut, local, pr];
		expect(findBranchRow(rows, CHECKED_OUT_BRANCH)).toBe(checkedOut);
		expect(findBranchRow(rows, { kind: "branch", name: "main" })).toBe(local);
		// A picked branch that is checked out since is still there to read.
		expect(findBranchRow(rows, { kind: "branch", name: "feature/login" })).toBe(checkedOut);
		expect(findBranchRow(rows, { kind: "branch", name: "deleted" })).toBeUndefined();
		expect(findBranchRow(rows, PR)).toBe(pr);
		expect(findBranchRow(rows, { ...PR, url: "https://github.com/acme/app/pull/43" })).toBeUndefined();
	});

	it("finds a PR whose branch became local in that branch's row, which carries the PR", () => {
		const local = row({ name: "fix/crash", prNumber: 42, prUrl: "https://github.com/acme/app/pull/42" });
		const checkedOut = row({ name: "feature/login", checkedOut: true, prNumber: 43, prUrl: "https://github.com/acme/app/pull/43" });
		expect(findBranchRow([checkedOut, local], PR)).toBe(local);
		expect(findBranchRow([checkedOut, local], { ...PR, url: "https://github.com/acme/app/pull/43" })).toBe(checkedOut);
		expect(findBranchRow([row({ name: "fix/crash" })], PR)).toBeUndefined();
	});
});

describe("filesProjectKind", () => {
	it("reads a standalone worker as scratch and a standalone orchestrator as one repository, as the daemon does", () => {
		expect(filesProjectKind({ workspaceId: "__standalone__", kind: "worker" }, "standalone")).toBe("scratch");
		expect(filesProjectKind({ workspaceId: "__standalone__", kind: "orchestrator" }, undefined)).toBe("single_repo");
		expect(filesProjectKind({ workspaceId: "proj", kind: "worker" }, "workspace")).toBe("workspace");
		expect(filesProjectKind({ workspaceId: "proj", kind: "worker" }, undefined)).toBeUndefined();
	});
});
