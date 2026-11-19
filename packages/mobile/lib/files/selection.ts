import type { FileRead, WorkspaceBranch, WorkspaceCommit, WorkspaceFileSummary, WorkspaceManifest, WorkspaceSection, WorkspaceView } from "./api";
import { branchName, changeGroups, type ChangeEntry, type ChangeGroup } from "./model";

// What the Files screen compares, as the desktop's branch and changes menus
// pick it: the checked-out worktree or another branch or the session's pull
// request, and which of its changes. A phone picks one commit at a time; the
// desktop's ticking of several is left to the desktop.

export type FilesBranch =
	| { kind: "checked_out" }
	/** `name` is a local branch, or `refs/remotes/<remote>/<branch>` for one that is only remote. */
	| { kind: "branch"; name: string }
	| { kind: "pull_request"; number: number; url: string; branch: string };

/**
 * `all` is base to worktree, the screen's long-standing view. The scopes split
 * the worktree's uncommitted work. `commits` with no `sha` is every commit on
 * the branch. Another branch or a pull request has only commits.
 */
export type FilesChanges =
	| { kind: "all" | "uncommitted" | "staged" | "unstaged" }
	| { kind: "commits"; sha?: string };

export type FilesSelection = { branch: FilesBranch; changes: FilesChanges };

export const CHECKED_OUT: FilesBranch = { kind: "checked_out" };
export const DEFAULT_SELECTION: FilesSelection = { branch: CHECKED_OUT, changes: { kind: "all" } };
const ALL_COMMITS: FilesChanges = { kind: "commits" };

export function viewOf(branch: FilesBranch): WorkspaceView | undefined {
	if (branch.kind === "branch") return { branch: branch.name };
	if (branch.kind === "pull_request") return { prNumber: branch.number, ...(branch.url ? { sourceUrl: branch.url } : {}) };
	return undefined;
}

/** A branch list row as the branch it reads. */
export function branchOfRow(row: WorkspaceBranch): FilesBranch {
	if (row.checkedOut) return CHECKED_OUT;
	if (row.kind === "pull_request") return { kind: "pull_request", number: row.prNumber ?? 0, url: row.prUrl ?? "", branch: row.name };
	return { kind: "branch", name: row.kind === "remote" ? `refs/remotes/${row.name}` : row.name };
}

/** One id per branch, so a list row and a pick can be matched. */
export function branchId(branch: FilesBranch): string {
	if (branch.kind === "checked_out") return "checked_out";
	if (branch.kind === "pull_request") return `pr:${branch.url || `#${branch.number}`}`;
	return `branch:${branch.name}`;
}

export function sameBranch(a: FilesBranch, b: FilesBranch): boolean {
	return branchId(a) === branchId(b);
}

/** The branch as the trigger names it. `checkedOutName` is the worktree's branch, when known. */
export function branchLabel(branch: FilesBranch, checkedOutName?: string): string {
	if (branch.kind === "pull_request") return `PR #${branch.number}`;
	if (branch.kind === "branch") return branch.name.replace(/^refs\/remotes\//, "");
	return checkedOutName || "Checked out";
}

/** Another branch or a pull request has only commits; the worktree's scopes fall back to all of them. */
export function normalizeSelection(selection: FilesSelection): FilesSelection {
	if (selection.branch.kind === "checked_out" || selection.changes.kind === "commits") return selection;
	return { branch: selection.branch, changes: ALL_COMMITS };
}

/** The pick a branch starts on: the long-standing view on the worktree, every commit elsewhere. */
export function selectionForBranch(branch: FilesBranch): FilesSelection {
	return branch.kind === "checked_out" ? DEFAULT_SELECTION : { branch, changes: ALL_COMMITS };
}

// ---- Route params ------------------------------------------------------------

/** The pick as the string params a Files route carries; the default carries none. */
export type SelectionParams = { branch?: string; pr?: string; prUrl?: string; prBranch?: string; changes?: string; commit?: string };

export function selectionParams(selection: FilesSelection): SelectionParams {
	const { branch, changes } = normalizeSelection(selection);
	const params: SelectionParams = {};
	if (branch.kind === "branch") params.branch = branch.name;
	if (branch.kind === "pull_request") {
		params.pr = String(branch.number);
		if (branch.url) params.prUrl = branch.url;
		if (branch.branch) params.prBranch = branch.branch;
	}
	if (changes.kind !== "all") params.changes = changes.kind;
	if (changes.kind === "commits" && changes.sha) params.commit = changes.sha;
	return params;
}

const SCOPES = new Set(["all", "uncommitted", "staged", "unstaged"]);

export function selectionFromParams(params: Partial<Record<keyof SelectionParams, string | string[]>>): FilesSelection {
	const one = (value: string | string[] | undefined) => (Array.isArray(value) ? value[0] : value) || undefined;
	const pr = Number(one(params.pr));
	const name = one(params.branch);
	const branch: FilesBranch = Number.isInteger(pr) && pr > 0
		? { kind: "pull_request", number: pr, url: one(params.prUrl) ?? "", branch: one(params.prBranch) ?? "" }
		: name ? { kind: "branch", name } : CHECKED_OUT;
	const kind = one(params.changes) ?? "all";
	const changes: FilesChanges = kind === "commits"
		? { kind: "commits", ...(one(params.commit) ? { sha: one(params.commit) } : {}) }
		: SCOPES.has(kind) ? { kind: kind as "all" } : { kind: "all" };
	return normalizeSelection({ branch, changes });
}

// ---- Reading -------------------------------------------------------------------

/**
 * Which of a file's diffs the pick reads. An unstaged list holds untracked
 * files too, which git reads from their own section.
 */
export function fileRead(selection: FilesSelection, entry?: Pick<ChangeEntry, "sections">): FileRead {
	const { branch, changes } = normalizeSelection(selection);
	if (changes.kind === "commits" && changes.sha) return { commitSha: changes.sha };
	if (branch.kind !== "checked_out") return {};
	switch (changes.kind) {
		case "all":
			return {};
		case "commits":
			return { section: "committed" };
		case "unstaged":
			return { section: entry?.sections.includes("untracked") && !entry.sections.includes("unstaged") ? "untracked" : "unstaged" };
		default:
			return { section: changes.kind };
	}
}

/**
 * Whether the machine can show both sides of an image for this read. The blob
 * route compares the base with the live worktree, or reads a commit's or a
 * view's snapshot; it has no worktree scopes. So Staged (HEAD → index),
 * Unstaged (index → worktree), Uncommitted (HEAD → worktree) and the branch's
 * commits (base → HEAD) would show other bytes than their diff, and are not
 * offered until the daemon can read them. An untracked file has no before,
 * and its after is the worktree, which the route reads right.
 */
export function imageComparable(read: FileRead): boolean {
	return !read.section || read.section === "untracked";
}

/** The worktree splits into Uncommitted, Staged and Unstaged only where the daemon lists Uncommitted (it is missing from older ones). */
export function scopesAvailable(manifest: Pick<WorkspaceManifest, "sections" | "degraded"> | null | undefined): boolean {
	return Boolean(manifest && manifest.sections.uncommitted !== undefined && !manifest.degraded);
}

export function shortSha(sha: string): string {
	return sha.slice(0, 7);
}

/** What the changes trigger says. `base` is the branch the worktree compares with, when it has one. */
export function changesLabel(changes: FilesChanges, base?: string): string {
	switch (changes.kind) {
		case "all":
			return base ? `All since ${base}` : "All changes";
		case "uncommitted":
			return "Uncommitted";
		case "staged":
			return "Staged";
		case "unstaged":
			return "Unstaged";
		case "commits":
			return changes.sha ? shortSha(changes.sha) : "All commits";
	}
}

/** The branch the worktree's "all" compares with; none when the daemon fell back to the last commit. */
export function compareBase(manifest: Pick<WorkspaceManifest, "compareBaseRef" | "compareMode"> | null | undefined): string | undefined {
	if (!manifest || manifest.compareMode === "head_fallback" || !manifest.compareBaseRef) return undefined;
	return branchName(manifest.compareBaseRef);
}

/** The worktree's files limited to some of its sections, each keeping those sections as badges. */
function sectionGroup(manifest: Pick<WorkspaceManifest, "sections">, keep: WorkspaceSection[], files: WorkspaceFileSummary[]): ChangeEntry[] {
	const sections = { committed: [], staged: [], unstaged: [], untracked: [] } as Record<WorkspaceSection, WorkspaceFileSummary[]>;
	for (const section of keep) sections[section] = manifest.sections[section] ?? [];
	return changeGroups({ files, sections }).flatMap((group) => group.files);
}

function plainGroup(files: readonly WorkspaceFileSummary[]): ChangeEntry[] {
	return files.map((file) => ({ ...file, sections: [] })).sort((a, b) => a.path.localeCompare(b.path));
}

function group(key: string, title: string, files: ChangeEntry[]): ChangeGroup[] {
	return files.length ? [{ key, title, files }] : [];
}

/**
 * The changed files the pick lists, grouped, or undefined while what it needs
 * is still loading: `history` for one commit, `rangeFiles` for every commit of
 * another branch or a pull request.
 */
export function selectionGroups(
	selection: FilesSelection,
	manifest: Pick<WorkspaceManifest, "files" | "sections"> | null | undefined,
	history?: { commits: readonly WorkspaceCommit[] },
	rangeFiles?: readonly WorkspaceFileSummary[],
): ChangeGroup[] | undefined {
	const { branch, changes } = normalizeSelection(selection);
	if (changes.kind === "commits" && changes.sha) {
		const commit = history?.commits.find((item) => item.sha === changes.sha);
		// Unresolved until the history lists it: a commit outside the newest ones is not an empty one.
		if (!commit) return undefined;
		return group("commit", `Changed in ${shortSha(commit.sha)}`, plainGroup(commit.files));
	}
	if (branch.kind !== "checked_out") {
		if (!rangeFiles) return undefined;
		// The trigger above names the branch; a long name would only wrap here.
		return group("range", branch.kind === "pull_request" ? "Changed in this pull request" : "Changed on this branch", plainGroup(rangeFiles));
	}
	if (!manifest) return undefined;
	const { sections } = manifest;
	switch (changes.kind) {
		case "all":
			return changeGroups(manifest);
		case "uncommitted":
			return group("uncommitted", "Uncommitted", sectionGroup(manifest, ["staged", "unstaged", "untracked"], sections.uncommitted ?? []));
		case "staged":
			return group("staged", "Staged", sectionGroup(manifest, ["staged"], []));
		case "unstaged":
			return group("unstaged", "Unstaged", sectionGroup(manifest, ["unstaged", "untracked"], []));
		case "commits":
			return group("committed", "Committed on branch", sectionGroup(manifest, ["committed"], []));
	}
}

// ---- Comments ------------------------------------------------------------------

/**
 * What a comment was made on, in the words desktop's Files review sends: the
 * source (the worktree, a branch, a pull request) and the comparison scope. The
 * shared formatter prints both, so the agent knows line 42 of a commit's diff is
 * not line 42 of its worktree file.
 */
export function annotationContext(selection: FilesSelection): { source: string; scope: string } {
	const { branch, changes } = normalizeSelection(selection);
	const source = branch.kind === "branch" ? `Branch ${branch.name.replace(/^refs\/remotes\//, "")}`
		: branch.kind === "pull_request" ? `PR #${branch.number}${branch.url ? ` (${branch.url})` : ""}`
		: "Workspace";
	const scope = changes.kind === "commits" ? (changes.sha ? `commit ${shortSha(changes.sha)}` : "committed")
		: changes.kind === "all" ? "combined"
		: changes.kind;
	return { source, scope };
}

/** Whether a comment was made in this pick. One queued before picks existed was made in the default view. */
export function inPick(target: { source?: string; scope?: string }, context: { source: string; scope: string }): boolean {
	return (target.source ?? "Workspace") === context.source && (target.scope ?? "combined") === context.scope;
}
