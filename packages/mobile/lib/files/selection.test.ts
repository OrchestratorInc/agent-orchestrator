import { describe, expect, it } from "vitest";
import type { WorkspaceBranch, WorkspaceFileSummary, WorkspaceManifest } from "./api";
import { formatFileAnnotationMessage } from "../../../shared/files/file-annotations";
import {
	annotationContext,
	branchLabel,
	branchOfRow,
	changesLabel,
	compareBase,
	DEFAULT_SELECTION,
	fileRead,
	imageComparable,
	inPick,
	scopesAvailable,
	selectionForBranch,
	selectionFromParams,
	selectionGroups,
	selectionParams,
	viewOf,
	type FilesSelection,
} from "./selection";

const file = (path: string, extra: Partial<WorkspaceFileSummary> = {}): WorkspaceFileSummary => ({
	path, status: "modified", additions: 1, deletions: 1, binary: false, size: 10, ...extra,
});

const manifest = (sections: Partial<WorkspaceManifest["sections"]>, files: WorkspaceFileSummary[] = []): Pick<WorkspaceManifest, "files" | "sections"> => ({
	files,
	sections: { committed: [], staged: [], unstaged: [], untracked: [], ...sections },
});

const row = (extra: Partial<WorkspaceBranch>): WorkspaceBranch => ({ kind: "local", name: "", checkedOut: false, isDefault: false, ...extra });

const paths = (groups: ReturnType<typeof selectionGroups>) => groups?.map((group) => [group.title, group.files.map((entry) => entry.path)]);

describe("route params", () => {
	it("carry nothing for the default, so a file opened from chat reads as before", () => {
		expect(selectionParams(DEFAULT_SELECTION)).toEqual({});
		expect(selectionFromParams({})).toEqual(DEFAULT_SELECTION);
	});

	it("round-trip a branch, a pull request, a scope and a commit", () => {
		const picks: FilesSelection[] = [
			{ branch: { kind: "checked_out" }, changes: { kind: "staged" } },
			{ branch: { kind: "checked_out" }, changes: { kind: "commits", sha: "abc123" } },
			{ branch: { kind: "branch", name: "refs/remotes/origin/feat" }, changes: { kind: "commits" } },
			{ branch: { kind: "pull_request", number: 12, url: "https://github.com/o/r/pull/12", branch: "fix" }, changes: { kind: "commits", sha: "def456" } },
		];
		for (const pick of picks) expect(selectionFromParams(selectionParams(pick))).toEqual(pick);
	});

	it("give another branch every commit when a worktree scope is asked of it", () => {
		expect(selectionFromParams({ branch: "main", changes: "staged" })).toEqual({ branch: { kind: "branch", name: "main" }, changes: { kind: "commits" } });
		expect(selectionFromParams({ changes: "nonsense" })).toEqual(DEFAULT_SELECTION);
	});
});

describe("fileRead", () => {
	const checkedOut = (changes: FilesSelection["changes"]): FilesSelection => ({ branch: { kind: "checked_out" }, changes });

	it("reads the worktree's sections, and an untracked file from its own", () => {
		expect(fileRead(checkedOut({ kind: "all" }))).toEqual({});
		expect(fileRead(checkedOut({ kind: "uncommitted" }))).toEqual({ section: "uncommitted" });
		expect(fileRead(checkedOut({ kind: "staged" }))).toEqual({ section: "staged" });
		expect(fileRead(checkedOut({ kind: "unstaged" }), { sections: ["unstaged"] })).toEqual({ section: "unstaged" });
		expect(fileRead(checkedOut({ kind: "unstaged" }), { sections: ["untracked"] })).toEqual({ section: "untracked" });
		expect(fileRead(checkedOut({ kind: "commits" }))).toEqual({ section: "committed" });
	});

	it("reads one commit by its SHA, on any branch", () => {
		expect(fileRead(checkedOut({ kind: "commits", sha: "abc" }))).toEqual({ commitSha: "abc" });
		expect(fileRead({ branch: { kind: "branch", name: "main" }, changes: { kind: "commits", sha: "abc" } })).toEqual({ commitSha: "abc" });
		expect(fileRead({ branch: { kind: "branch", name: "main" }, changes: { kind: "commits" } })).toEqual({});
	});
});

describe("selectionGroups", () => {
	const worktree = manifest({
		committed: [file("src/a.ts")],
		staged: [file("src/b.ts")],
		unstaged: [file("src/b.ts"), file("src/c.ts")],
		untracked: [file("notes.md", { status: "added" })],
		uncommitted: [file("notes.md", { status: "added" }), file("src/b.ts", { additions: 9 }), file("src/c.ts")],
	}, [file("src/a.ts"), file("src/b.ts"), file("src/c.ts"), file("notes.md")]);

	it("keeps the long-standing two groups for all changes", () => {
		expect(paths(selectionGroups(DEFAULT_SELECTION, worktree))).toEqual([
			["Uncommitted", ["notes.md", "src/b.ts", "src/c.ts"]],
			["Committed on branch", ["src/a.ts"]],
		]);
	});

	it("lists each worktree scope on its own, with the counts of its own diff", () => {
		const pick = (kind: "uncommitted" | "staged" | "unstaged" | "commits") => selectionGroups({ branch: { kind: "checked_out" }, changes: { kind } }, worktree);
		const uncommitted = pick("uncommitted");
		expect(paths(uncommitted)).toEqual([["Uncommitted", ["notes.md", "src/b.ts", "src/c.ts"]]]);
		// Against HEAD, both halves of the partly staged file at once.
		expect(uncommitted?.[0].files.find((entry) => entry.path === "src/b.ts")).toMatchObject({ additions: 9, sections: ["staged", "unstaged"] });
		expect(paths(pick("staged"))).toEqual([["Staged", ["src/b.ts"]]]);
		expect(paths(pick("unstaged"))).toEqual([["Unstaged", ["notes.md", "src/b.ts", "src/c.ts"]]]);
		expect(paths(pick("commits"))).toEqual([["Committed on branch", ["src/a.ts"]]]);
	});

	it("waits for the history to list one commit, and for the range on another branch", () => {
		const commit = { branch: { kind: "checked_out" as const }, changes: { kind: "commits" as const, sha: "abcdef1234" } };
		expect(selectionGroups(commit, worktree)).toBeUndefined();
		// Outside the listed history it stays unresolved, never an empty commit.
		expect(selectionGroups(commit, worktree, { commits: [] })).toBeUndefined();
		expect(paths(selectionGroups(commit, worktree, { commits: [{ sha: "abcdef1234", subject: "x", author: "a", timestamp: "", files: [file("z.ts"), file("y.ts")] }] })))
			.toEqual([["Changed in abcdef1", ["y.ts", "z.ts"]]]);
		const other = selectionForBranch({ kind: "branch", name: "main" });
		expect(selectionGroups(other, worktree)).toBeUndefined();
		expect(paths(selectionGroups(other, worktree, undefined, [file("m.ts")]))).toEqual([["Changed on this branch", ["m.ts"]]]);
	});

	it("has no group for an empty pick", () => {
		expect(selectionGroups({ branch: { kind: "checked_out" }, changes: { kind: "staged" } }, manifest({}))).toEqual([]);
	});
});

describe("branches", () => {
	it("reads a local branch, a remote-only branch and a pull request each as the daemon names it", () => {
		expect(branchOfRow(row({ checkedOut: true, name: "feat" }))).toEqual({ kind: "checked_out" });
		expect(branchOfRow(row({ name: "main" }))).toEqual({ kind: "branch", name: "main" });
		const remote = branchOfRow(row({ kind: "remote", name: "origin/release" }));
		expect(remote).toEqual({ kind: "branch", name: "refs/remotes/origin/release" });
		expect(viewOf(remote)).toEqual({ branch: "refs/remotes/origin/release" });
		expect(branchLabel(remote)).toBe("origin/release");
		const pr = branchOfRow(row({ kind: "pull_request", name: "fix", prNumber: 7, prUrl: "https://x/pull/7" }));
		expect(viewOf(pr)).toEqual({ prNumber: 7, sourceUrl: "https://x/pull/7" });
		expect(branchLabel(pr)).toBe("PR #7");
		expect(viewOf({ kind: "checked_out" })).toBeUndefined();
		expect(branchLabel({ kind: "checked_out" }, "feat")).toBe("feat");
	});
});

describe("labels", () => {
	it("names the base all changes compare with, unless the daemon fell back to the last commit", () => {
		expect(changesLabel({ kind: "all" }, compareBase({ compareBaseRef: "refs/remotes/origin/main", compareMode: "base" }))).toBe("All since main");
		expect(changesLabel({ kind: "all" }, compareBase({ compareBaseRef: "refs/remotes/origin/main", compareMode: "head_fallback" }))).toBe("All changes");
		expect(changesLabel({ kind: "commits", sha: "0123456789" })).toBe("0123456");
		expect(changesLabel({ kind: "commits" })).toBe("All commits");
	});

	it("offers the worktree's scopes only where the daemon lists Uncommitted", () => {
		expect(scopesAvailable({ sections: { committed: [], staged: [], unstaged: [], untracked: [], uncommitted: [] }, degraded: false })).toBe(true);
		expect(scopesAvailable({ sections: { committed: [], staged: [], unstaged: [], untracked: [] }, degraded: false })).toBe(false);
		expect(scopesAvailable({ sections: { committed: [], staged: [], unstaged: [], untracked: [], uncommitted: [] }, degraded: true })).toBe(false);
	});
});

describe("comments", () => {
	it("say what they were made on, in desktop's words", () => {
		expect(annotationContext(DEFAULT_SELECTION)).toEqual({ source: "Workspace", scope: "combined" });
		expect(annotationContext({ branch: { kind: "checked_out" }, changes: { kind: "staged" } })).toEqual({ source: "Workspace", scope: "staged" });
		expect(annotationContext({ branch: { kind: "branch", name: "refs/remotes/origin/main" }, changes: { kind: "commits", sha: "abcdef1234" } })).toEqual({ source: "Branch origin/main", scope: "commit abcdef1" });
		expect(annotationContext({ branch: { kind: "pull_request", number: 7, url: "https://x/pull/7", branch: "fix" }, changes: { kind: "commits" } })).toEqual({ source: "PR #7 (https://x/pull/7)", scope: "committed" });
	});

	it("reach the agent with that source and scope", () => {
		const context = annotationContext({ branch: { kind: "branch", name: "main" }, changes: { kind: "commits", sha: "abcdef1234" } });
		const message = formatFileAnnotationMessage({ path: "a.ts", side: "new", line: 42, ...context }, "why?");
		expect(message).toContain("- Source: Branch main");
		expect(message).toContain("- Comparison scope: commit abcdef1");
	});

	it("are drawn only in the pick they were made in; older ones belong to the default view", () => {
		const staged = annotationContext({ branch: { kind: "checked_out" }, changes: { kind: "staged" } });
		const all = annotationContext(DEFAULT_SELECTION);
		expect(inPick(staged, staged)).toBe(true);
		expect(inPick(staged, all)).toBe(false);
		expect(inPick({}, all)).toBe(true);
		expect(inPick({}, staged)).toBe(false);
	});
});

describe("image previews", () => {
	const checkedOut = (changes: FilesSelection["changes"]): FilesSelection => ({ branch: { kind: "checked_out" }, changes });
	const tracked = { sections: ["staged", "unstaged"] as const };

	// The blob route reads base → worktree, a commit or a view; never HEAD,
	// the index, or a scope's sides. Offer an image only where it matches the diff.
	it("compare both sides for all changes, a commit and another branch", () => {
		expect(imageComparable(fileRead(DEFAULT_SELECTION, { sections: [...tracked.sections] }))).toBe(true);
		expect(imageComparable(fileRead(checkedOut({ kind: "commits", sha: "abc" })))).toBe(true);
		expect(imageComparable(fileRead({ branch: { kind: "branch", name: "main" }, changes: { kind: "commits" } }))).toBe(true);
		expect(imageComparable(fileRead({ branch: { kind: "pull_request", number: 3, url: "", branch: "x" }, changes: { kind: "commits", sha: "abc" } }))).toBe(true);
	});

	it("are not offered for a worktree scope, whose sides the route cannot read", () => {
		for (const kind of ["staged", "unstaged", "uncommitted"] as const) {
			expect(imageComparable(fileRead(checkedOut({ kind }), { sections: [...tracked.sections] }))).toBe(false);
		}
		expect(imageComparable(fileRead(checkedOut({ kind: "commits" })))).toBe(false);
	});

	it("show an untracked file in Unstaged, which has no before and the worktree after", () => {
		expect(imageComparable(fileRead(checkedOut({ kind: "unstaged" }), { sections: ["untracked"] }))).toBe(true);
	});
});
