import type {
	FilesBranchTarget,
	FilesChanges,
	FilesSelection,
	ReviewTarget,
	WorkspaceBranch,
	WorkspaceCommitSelection,
	WorkspaceFilesResponse,
	WorkspaceFileSummary,
	WorkspaceHistoryResponse,
	WorkspaceViewInfo,
	WorkspaceViewParams,
} from "../hooks/useSessionWorkspaceFiles";
import { STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID, toProjectKind, type ProjectKind, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";

// Pure rules for what the Files review shows. The stored pick is healed on
// every read and never written back, so a pick survives the data that made it
// invalid for a moment (a refresh, history still loading).

export const CHECKED_OUT_BRANCH: FilesBranchTarget = { kind: "checked_out" };
export const DEFAULT_FILES_SELECTION: FilesSelection = { branch: CHECKED_OUT_BRANCH, changes: null };
export const ALL_COMMITS: FilesChanges = { kind: "commits", shas: [] };

type ScopeKind = Exclude<FilesChanges["kind"], "commits">;
// Shared values, so a healed scope keeps its identity across reads.
const SCOPE_CHANGES: Record<ScopeKind, FilesChanges> = {
	uncommitted: { kind: "uncommitted" },
	staged: { kind: "staged" },
	unstaged: { kind: "unstaged" },
	combined: { kind: "combined" },
};

export function scopeChanges(kind: ScopeKind): FilesChanges {
	return SCOPE_CHANGES[kind];
}

/**
 * An older daemon sends no Uncommitted section, and a degraded manifest's
 * sections may be incomplete. Both keep the combined "Changes" scope, read one
 * commit at a time and offer no other branches; an older daemon would silently
 * ignore `commitShas`.
 */
export function isLegacyManifest(manifest: WorkspaceFilesResponse | undefined): boolean {
	return Boolean(manifest && (manifest.sections.uncommitted === undefined || manifest.degraded));
}

/** True while the Files review follows its default, the only case the inspector prefetches. */
export function followsDefaultSelection(selection: FilesSelection | undefined): boolean {
	return !selection || (selection.branch.kind === "checked_out" && selection.changes === null);
}

/**
 * What the review shows before the user picks: the worktree's uncommitted
 * changes, else the branch's commits, else the (empty) uncommitted changes.
 * Another branch or a PR only has commits.
 */
export function defaultChanges(branch: FilesBranchTarget, manifest: WorkspaceFilesResponse | undefined, legacy: boolean): FilesChanges {
	if (branch.kind !== "checked_out") return ALL_COMMITS;
	if (legacy) return SCOPE_CHANGES.combined;
	if (manifest?.sections.uncommitted?.length) return SCOPE_CHANGES.uncommitted;
	if (manifest?.sections.committed.length) return ALL_COMMITS;
	return SCOPE_CHANGES.uncommitted;
}

export type ReviewHistory = Pick<WorkspaceHistoryResponse, "commits" | "commitsTruncated">;

/**
 * The pick as it applies now. An empty scope stays picked; a working scope on
 * another branch or a SHA the loaded history no longer lists falls back to
 * All commits. Explicit commit picks remain selected even if they cover the
 * complete list, including when the branch has only one commit.
 */
export function normalizeChanges(selection: FilesSelection, manifest: WorkspaceFilesResponse | undefined, legacy: boolean, history?: ReviewHistory): FilesChanges {
	const picked = selection.changes ?? defaultChanges(selection.branch, manifest, legacy);
	if (picked.kind !== "commits") {
		if (selection.branch.kind !== "checked_out") return ALL_COMMITS;
		if (legacy && picked.kind === "uncommitted") return SCOPE_CHANGES.combined;
		if (!legacy && picked.kind === "combined") return SCOPE_CHANGES.uncommitted;
		return picked;
	}
	let shas: readonly string[] = picked.shas;
	if (history) {
		const ticked = new Set(shas);
		shas = history.commits.filter((commit) => ticked.has(commit.sha)).map((commit) => commit.sha);
	}
	if (legacy) shas = shas.slice(0, 1);
	if (shas.length === 0) return ALL_COMMITS;
	return sameShas(shas, picked.shas) ? picked : { kind: "commits", shas };
}

/**
 * Ticks or unticks one commit, or ticks All commits when `sha` is undefined.
 * All clears the other ticks, and unticking it alone changes nothing; a tick
 * while All is on picks just that commit; unticking the last commit returns to
 * All. An older daemon reads one commit at a time, so a tick replaces the last.
 * `listed` is the history order (newest first) the ticks are kept in.
 */
export function toggleCommit(current: FilesChanges, sha: string | undefined, listed: readonly string[], legacy: boolean): FilesChanges {
	if (sha === undefined) return ALL_COMMITS;
	const ticked = current.kind === "commits" ? current.shas : [];
	if (ticked.includes(sha)) {
		const rest = ticked.filter((entry) => entry !== sha);
		return rest.length === 0 ? ALL_COMMITS : { kind: "commits", shas: rest };
	}
	if (legacy || ticked.length === 0) return { kind: "commits", shas: [sha] };
	const next = new Set([...ticked, sha]);
	return { kind: "commits", shas: listed.filter((entry) => next.has(entry)) };
}

/** A value-equality key for picked changes. */
export function changesKey(changes: FilesChanges): string {
	return changes.kind === "commits" ? `commits:${changes.shas.join(",")}` : changes.kind;
}

/** A value-equality key for a branch target, ignoring the tip it was picked at. */
export function branchKey(branch: FilesBranchTarget): string {
	return branch.kind === "branch" ? `branch:${branch.name}` : branch.kind === "pull_request" ? `pr:${branch.url || `#${branch.number}`}` : "checked_out";
}

/** Every listed commit from the oldest tick to the newest, which always reads as one range. */
export function ticksIncludingBetween(shas: readonly string[], listed: readonly string[]): string[] {
	const indexes = shas.map((sha) => listed.indexOf(sha)).filter((index) => index >= 0);
	if (indexes.length === 0) return [...shas];
	return listed.slice(Math.min(...indexes), Math.max(...indexes) + 1);
}

/** FNV-1a of the sorted SHAs: a short, order-free name for a set of commits. */
export function shaSetHash(shas: readonly string[]): string {
	const text = [...shas].sort().join(",");
	let hash = 0x811c9dc5;
	for (let index = 0; index < text.length; index += 1) {
		hash ^= text.charCodeAt(index);
		hash = Math.imul(hash, 0x01000193);
	}
	return (hash >>> 0).toString(16).padStart(8, "0");
}

/**
 * The key the review's viewed marks are stored under. The checked-out keys
 * match the ones stored before branches could be picked.
 */
export function viewedKey(branch: FilesBranchTarget, changes: FilesChanges): string {
	if (changes.kind !== "commits") return changes.kind;
	const prefix = branch.kind === "branch" ? `branch:${branch.name}:` : branch.kind === "pull_request" ? `pr:${branch.number}:` : "";
	if (changes.shas.length === 0) return prefix ? `${prefix}all` : "committed";
	if (changes.shas.length === 1) return `${prefix}commit:${changes.shas[0]}`;
	return `${prefix}commits:${shaSetHash(changes.shas)}`;
}

/** The view a request reads, fenced to the tip and base its history listed. Undefined reads the worktree. */
export function workspaceViewParams(branch: FilesBranchTarget, view?: WorkspaceViewInfo): WorkspaceViewParams | undefined {
	if (branch.kind === "checked_out") return undefined;
	const fence = { expectedTipSha: view?.tipSha || undefined, expectedBaseSha: view?.baseSha || undefined };
	if (branch.kind === "branch") return { branch: branch.name, ...fence };
	return { prNumber: branch.number, sourceUrl: branch.url || undefined, ...fence };
}

/**
 * Names the commits a request reads in query keys: "" for the worktree's own
 * scopes and its whole range, so those keys are unchanged; otherwise the view
 * (with its tip and base) and the SHAs.
 */
export function workspaceSelectionKey(view: WorkspaceViewParams | undefined, shas: readonly string[] = []): string {
	const fence = `@${view?.expectedTipSha ?? ""}:${view?.expectedBaseSha ?? ""}`;
	const viewKey = view?.prNumber ? `pr:${view.sourceUrl ?? `#${view.prNumber}`}${fence}` : view?.branch ? `branch:${view.branch}${fence}` : "";
	return viewKey === "" && shas.length === 0 ? "" : `${viewKey}|${shas.join(",")}`;
}

export type CommitSelectionRequest = {
	view?: WorkspaceViewParams;
	commitShas: readonly string[];
	workspaceVersion?: string;
	selectionKey: string;
};

/**
 * The selection route's request when the picked changes need it: All commits
 * or several commits of another branch, or several commits of the worktree's
 * branch. One commit reads its own listed files. Undefined while another
 * branch's history (which carries the fence) is still loading.
 */
export function commitSelectionRequest(branch: FilesBranchTarget, changes: FilesChanges, manifest: WorkspaceFilesResponse | undefined, history: WorkspaceHistoryResponse | undefined): CommitSelectionRequest | undefined {
	if (changes.kind !== "commits") return undefined;
	const shas = changes.shas;
	if (branch.kind === "checked_out") {
		if (shas.length < 2 || !manifest) return undefined;
		return { commitShas: shas, workspaceVersion: manifest.workspaceVersion, selectionKey: workspaceSelectionKey(undefined, shas) };
	}
	if (shas.length === 1 || !history) return undefined;
	const view = workspaceViewParams(branch, history.view);
	return { view, commitShas: shas, selectionKey: workspaceSelectionKey(view, shas) };
}

export type CommitSelectionState = { data?: WorkspaceCommitSelection; error?: Error | null; pending: boolean };

/** Everything the review pane reads for the picked branch and changes. */
export function buildReviewTarget({
	branch,
	changes,
	manifest,
	legacy,
	history,
	historyError,
	selection,
}: {
	branch: FilesBranchTarget;
	/** Already normalized. */
	changes: FilesChanges;
	manifest: WorkspaceFilesResponse | undefined;
	legacy: boolean;
	/** The branch's history; undefined while it loads. */
	history?: WorkspaceHistoryResponse;
	historyError?: Error | null;
	/** The selection route's state, when commitSelectionRequest asks for it. */
	selection?: CommitSelectionState;
}): ReviewTarget {
	const shas = changes.kind === "commits" ? changes.shas : [];
	const view = workspaceViewParams(branch, history?.view);
	const base = {
		key: viewedKey(branch, changes),
		branch,
		changes,
		scope: changes.kind === "commits" ? "committed" as const : changes.kind,
		commitShas: shas.length > 0 ? shas : undefined,
		view,
		workspaceVersion: branch.kind === "checked_out" ? manifest?.workspaceVersion : undefined,
		selectionKey: workspaceSelectionKey(view, shas),
		openable: true,
  source: changes.kind === "commits" ? { kind: "snapshot" as const, view: { ...view, expectedTipSha: history?.view?.tipSha ?? view?.expectedTipSha, expectedBaseSha: history?.view?.baseSha || undefined }, commitShas: [...shas], label: `${branch.kind === "branch" ? branch.name.replace(/^refs\/remotes\//, "") : branch.kind === "pull_request" ? `PR #${branch.number}` : "Committed"} · ${shas.length===1 ? shas[0].slice(0,7) : shas.length>1 ? `${shas.length} commits` : history?.view?.tipSha?.slice(0,7) ?? "all"}` } : undefined,
	};
	const awaitingHistory = (): ReviewTarget => (historyError ? { ...base, state: "error", error: historyError } : { ...base, state: "pending" });

	if (changes.kind !== "commits") {
		return manifest ? { ...base, state: "ready", files: scopeFiles(manifest, changes.kind, legacy) } : { ...base, state: "pending" };
	}
	if (shas.length === 1) {
		const commit = history?.commits.find((entry) => entry.sha === shas[0]);
		return commit
			? { ...base, state: "ready", files: commit.files, sha: commit.sha, subject: commit.subject }
			: awaitingHistory();
	}
	if (branch.kind === "checked_out" && shas.length === 0) {
		return manifest ? { ...base, state: "ready", files: manifest.sections.committed } : { ...base, state: "pending" };
	}
	if (branch.kind !== "checked_out" && !history) return awaitingHistory();
	if (!selection || selection.pending) return { ...base, state: "pending" };
	if (selection.error) return { ...base, state: "error", error: selection.error };
	const data = selection.data;
	if (!data) return { ...base, state: "pending" };
	if (!data.combinable && data.conflict) {
		const subject = history?.commits.find((entry) => entry.sha === data.conflict?.commitSha)?.subject;
		return { ...base, state: "not_combinable", conflict: { ...data.conflict, subject } };
	}
	return { ...base, state: "ready", files: data.files };
}

/**
 * A working scope's files. Unstaged lists untracked files as well, as `git
 * status` does; the two sections never share a path. An older daemon diffs
 * untracked files only in its combined scope, so they stay there.
 */
export function scopeFiles(manifest: WorkspaceFilesResponse, scope: ScopeKind, legacy: boolean): WorkspaceFileSummary[] {
	switch (scope) {
		case "uncommitted":
			return manifest.sections.uncommitted ?? [];
		case "staged":
			return manifest.sections.staged;
		case "unstaged":
			return legacy
				? manifest.sections.unstaged
				: [...manifest.sections.unstaged, ...manifest.sections.untracked].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
		case "combined":
			return manifest.files.filter((file) => file.status !== "unmodified");
	}
}

/** A branch menu row's id; the checked-out row (a detached HEAD has no name) is always "checked_out". */
export function branchRowId(row: WorkspaceBranch): string {
	if (row.checkedOut) return "checked_out";
	return row.kind === "pull_request" ? `pr:${row.prUrl || `#${row.prNumber ?? 0}`}` : `${row.kind === "remote" ? "remote" : "branch"}:${row.name}`;
}

/** What picking a branch menu row reads: the worktree, a local branch, or a PR whose branch is not local. */
export function branchTargetOf(row: WorkspaceBranch): FilesBranchTarget {
	if (row.checkedOut) return CHECKED_OUT_BRANCH;
	if (row.kind === "pull_request") return { kind: "pull_request", number: row.prNumber ?? 0, url: row.prUrl ?? "", branch: row.name, tipSha: row.tipSha };
	return { kind: "branch", name: row.kind === "remote" ? `refs/remotes/${row.name}` : row.name, tipSha: row.tipSha };
}

/**
 * The listed row for a branch target; none means that branch or PR is gone.
 * A PR whose branch has become local is listed as that local branch, which
 * carries the PR.
 */
export function findBranchRow(rows: readonly WorkspaceBranch[], branch: FilesBranchTarget): WorkspaceBranch | undefined {
	switch (branch.kind) {
		case "checked_out":
			return rows.find((row) => row.checkedOut);
		case "branch":
			return rows.find((row) => (row.kind === "local" && row.name === branch.name) || (row.kind === "remote" && `refs/remotes/${row.name}` === branch.name));
		case "pull_request": {
			const isPR = (row: WorkspaceBranch) => (branch.url ? row.prUrl === branch.url : row.prNumber === branch.number);
			return rows.find((row) => row.kind === "pull_request" && isPR(row)) ?? rows.find((row) => row.kind === "local" && isPR(row));
		}
	}
}

/**
 * The project kind the Files review reads a session as, as the daemon does: a
 * standalone worker has no Git, and a standalone orchestrator's folder reads
 * as one repository. Undefined while unknown.
 */
export function filesProjectKind(session: Pick<WorkspaceSession, "workspaceId" | "kind">, workspaceKind: WorkspaceSummary["kind"]): ProjectKind | undefined {
	if (session.workspaceId === STANDALONE_WORKSPACE_ID || workspaceKind === STANDALONE_PROJECT_KIND) return session.kind === "orchestrator" ? "single_repo" : "scratch";
	return toProjectKind(workspaceKind);
}

export type ChangeStats = { additions: number; deletions: number };

export function sumChanges(files: readonly Pick<WorkspaceFileSummary, "additions" | "deletions">[]): ChangeStats {
	let additions = 0;
	let deletions = 0;
	for (const file of files) {
		additions += file.additions;
		deletions += file.deletions;
	}
	return { additions, deletions };
}

function sameShas(a: readonly string[], b: readonly string[]): boolean {
	return a.length === b.length && a.every((sha, index) => sha === b[index]);
}
