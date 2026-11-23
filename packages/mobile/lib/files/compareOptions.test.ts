import { describe, expect, it } from "vitest";
import type { WorkspaceBranch, WorkspaceFileSummary, WorkspaceManifest } from "./api";
import { branchOptions, changesId, changesOfId, changesOptions } from "./compareOptions";

const NOW = Date.parse("2026-11-12T12:00:00Z");
const file = (path: string, extra: Partial<WorkspaceFileSummary> = {}): WorkspaceFileSummary => ({
	path, status: "modified", additions: 2, deletions: 1, binary: false, size: 10, ...extra,
});
const row = (extra: Partial<WorkspaceBranch>): WorkspaceBranch => ({ kind: "local", name: "", checkedOut: false, isDefault: false, ...extra });

const manifest = (sections: Partial<WorkspaceManifest["sections"]>, extra: Partial<WorkspaceManifest> = {}): Pick<WorkspaceManifest, "summary" | "sections" | "degraded" | "files" | "compareMode"> => ({
	summary: { files: 2, additions: 4, deletions: 2 },
	files: [file("a.ts"), file("b.ts")],
	degraded: false,
	sections: { committed: [], staged: [], unstaged: [], untracked: [], uncommitted: [], ...sections },
	...extra,
});

describe("branchOptions", () => {
	const branches = [
		row({ name: "feat/files", checkedOut: true, updatedAt: "2026-11-12T11:00:00Z" }),
		row({ kind: "pull_request", name: "fix/login", prNumber: 42, prTitle: "Fix login", prUrl: "https://x/pull/42" }),
		row({ name: "main", isDefault: true, updatedAt: "2026-11-10T12:00:00Z" }),
		row({ kind: "remote", name: "origin/release" }),
	];

	it("names each row and what it is, newest first as the daemon lists them", () => {
		expect(branchOptions(branches, "", NOW)).toEqual([
			{ id: "checked_out", title: "feat/files", detail: "Checked out · 1h" },
			{ id: "pr:https://x/pull/42", title: "PR #42 · Fix login", detail: "fix/login" },
			{ id: "branch:main", title: "main", detail: "Default branch · 2d" },
			{ id: "branch:refs/remotes/origin/release", title: "origin/release", detail: "Remote" },
		]);
	});

	it("finds a branch by name, a pull request by title or number", () => {
		expect(branchOptions(branches, "REL", NOW).map((option) => option.title)).toEqual(["origin/release"]);
		expect(branchOptions(branches, "login", NOW).map((option) => option.id)).toEqual(["pr:https://x/pull/42"]);
		expect(branchOptions(branches, "#42", NOW)).toHaveLength(1);
	});
});

describe("changesOptions", () => {
	const history = {
		view: { kind: "checked_out" as const, rangeMode: "since_base" as const },
		commits: [{ sha: "abcdef1234", subject: "Add files", author: "a", timestamp: "2026-11-12T10:00:00Z", files: [file("a.ts")] }],
	};

	it("lists the worktree's scopes with what each holds, leaving out an empty one, then the commits", () => {
		const options = changesOptions({
			selection: { branch: { kind: "checked_out" }, changes: { kind: "all" } },
			manifest: manifest({ uncommitted: [file("b.ts")], unstaged: [file("b.ts")], committed: [file("a.ts")] }),
			history,
			base: "main",
			now: NOW,
		});
		expect(options.map((option) => [option.section, option.id, option.title, option.detail])).toEqual([
			["Working tree", "all", "All since main", "2 files · +4 −2"],
			["Working tree", "uncommitted", "Uncommitted", "1 file · +2 −1"],
			["Working tree", "unstaged", "Unstaged", "1 file · +2 −1"],
			["Commits", "commits", "All commits", "1 file · +2 −1"],
			["Commits", "commit:abcdef1234", "Add files", "abcdef1 · 2h · 1 file · +2 −1"],
		]);
	});

	it("keeps an empty scope that is the pick, so the sheet always shows it", () => {
		const options = changesOptions({ selection: { branch: { kind: "checked_out" }, changes: { kind: "staged" } }, manifest: manifest({}), history: undefined });
		expect(options.map((option) => option.id)).toEqual(["all", "staged"]);
	});

	it("gives another branch only its commits, and says a default branch's are its latest", () => {
		const options = changesOptions({
			selection: { branch: { kind: "branch", name: "main" }, changes: { kind: "commits" } },
			manifest: manifest({ uncommitted: [file("b.ts")] }),
			history: { ...history, view: { kind: "branch", rangeMode: "recent" } },
			now: NOW,
		});
		expect(options.map((option) => [option.id, option.title])).toEqual([["commits", "Latest 1 commit"], ["commit:abcdef1234", "Add files"]]);
	});

	it("leaves out a side of the counts with nothing on it", () => {
		const options = changesOptions({ selection: { branch: { kind: "checked_out" }, changes: { kind: "all" } }, manifest: manifest({ uncommitted: [file("n.ts", { deletions: 0 })], staged: [file("b.png", { additions: 0, deletions: 0, binary: true })] }), history: undefined });
		expect(options.map((option) => option.detail)).toEqual(["2 files · +4 −2", "1 file · +2", "1 file"]);
	});

	it("counts all changes from the summary, which a capped file list would undercount", () => {
		const options = changesOptions({ selection: { branch: { kind: "checked_out" }, changes: { kind: "all" } }, manifest: manifest({}, { summary: { files: 6000, additions: 9, deletions: 0 } }), history: undefined });
		expect(options[0].detail).toBe("6000 files · +9");
	});

	it("offers no whole range on the checked-out branch when the daemon compares with HEAD", () => {
		const options = changesOptions({ selection: { branch: { kind: "checked_out" }, changes: { kind: "all" } }, manifest: manifest({}, { compareMode: "head_fallback" }), history: { ...history, view: { kind: "checked_out", rangeMode: "recent" } } });
		expect(options.map((option) => option.id)).toEqual(["all", "commit:abcdef1234"]);
	});

	it("maps a row back to the pick it names", () => {
		for (const changes of [{ kind: "all" }, { kind: "staged" }, { kind: "commits" }, { kind: "commits", sha: "abc" }] as const) {
			expect(changesOfId(changesId(changes))).toEqual(changes);
		}
	});
});
