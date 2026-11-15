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

export const CHECKED_OUT_BRANCH: FilesBranchTarget = { kind: "checked_out" };
export const DEFAULT_FILES_SELECTION: FilesSelection = { branch: CHECKED_OUT_BRANCH, changes: null };
export const ALL_COMMITS: FilesChanges = { kind: "commits", shas: [] };

type ScopeKind = Exclude<FilesChanges["kind"], "commits">;
const SCOPE_CHANGES: Record<ScopeKind, FilesChanges> = {
	uncommitted: { kind: "uncommitted" },
	staged: { kind: "staged" },
	unstaged: { kind: "unstaged" },
	combined: { kind: "combined" },
};

export function scopeChanges(kind: ScopeKind): FilesChanges {
	return SCOPE_CHANGES[kind];
}

export function isLegacyManifest(manifest: WorkspaceFilesResponse | undefined): boolean {
	return Boolean(manifest && (manifest.sections.uncommitted === undefined || manifest.degraded));
}

export function followsDefaultSelection(selection: FilesSelection | undefined): boolean {
	return !selection || (selection.branch.kind === "checked_out" && selection.changes === null);
}

export function defaultChanges(branch: FilesBranchTarget, manifest: WorkspaceFilesResponse | undefined, legacy: boolean): FilesChanges {
	if (branch.kind !== "checked_out") return ALL_COMMITS;
	if (legacy) return SCOPE_CHANGES.combined;
	if (manifest?.sections.uncommitted?.length) return SCOPE_CHANGES.uncommitted;
	if (manifest?.sections.committed.length) return ALL_COMMITS;
	return SCOPE_CHANGES.uncommitted;
}

export type ReviewHistory = Pick<WorkspaceHistoryResponse, "commits" | "commitsTruncated">;

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

export function changesKey(changes: FilesChanges): string {
	return changes.kind === "commits" ? `commits:${changes.shas.join(",")}` : changes.kind;
}

export function branchKey(branch: FilesBranchTarget): string {
	return branch.kind === "branch" ? `branch:${branch.name}` : branch.kind === "pull_request" ? `pr:${branch.url || `#${branch.number}`}` : "checked_out";
}

export function ticksIncludingBetween(shas: readonly string[], listed: readonly string[]): string[] {
	const indexes = shas.map((sha) => listed.indexOf(sha)).filter((index) => index >= 0);
	if (indexes.length === 0) return [...shas];
	return listed.slice(Math.min(...indexes), Math.max(...indexes) + 1);
}

export function shaSetHash(shas: readonly string[]): string {
	const text = [...shas].sort().join(",");
	let hash = 0x811c9dc5;
	for (let index = 0; index < text.length; index += 1) {
		hash ^= text.charCodeAt(index);
		hash = Math.imul(hash, 0x01000193);
	}
	return (hash >>> 0).toString(16).padStart(8, "0");
}

export function viewedKey(branch: FilesBranchTarget, changes: FilesChanges): string {
	if (changes.kind !== "commits") return changes.kind;
	const prefix = branch.kind === "branch" ? `branch:${branch.name}:` : branch.kind === "pull_request" ? `pr:${branch.number}:` : "";
	if (changes.shas.length === 0) return prefix ? `${prefix}all` : "committed";
	if (changes.shas.length === 1) return `${prefix}commit:${changes.shas[0]}`;
	return `${prefix}commits:${shaSetHash(changes.shas)}`;
}

export function workspaceViewParams(branch: FilesBranchTarget, view?: WorkspaceViewInfo): WorkspaceViewParams | undefined {
	if (branch.kind === "checked_out") return undefined;
	const fence = { expectedTipSha: view?.tipSha || undefined, expectedBaseSha: view?.baseSha || undefined };
	if (branch.kind === "branch") return { branch: branch.name, ...fence };
	return { prNumber: branch.number, sourceUrl: branch.url || undefined, ...fence };
}

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
	changes: FilesChanges;
	manifest: WorkspaceFilesResponse | undefined;
	legacy: boolean;
	history?: WorkspaceHistoryResponse;
	historyError?: Error | null;
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

export function branchRowId(row: WorkspaceBranch): string {
	if (row.checkedOut) return "checked_out";
	return row.kind === "pull_request" ? `pr:${row.prUrl || `#${row.prNumber ?? 0}`}` : `${row.kind === "remote" ? "remote" : "branch"}:${row.name}`;
}

export function branchTargetOf(row: WorkspaceBranch): FilesBranchTarget {
	if (row.checkedOut) return CHECKED_OUT_BRANCH;
	if (row.kind === "pull_request") return { kind: "pull_request", number: row.prNumber ?? 0, url: row.prUrl ?? "", branch: row.name, tipSha: row.tipSha };
	return { kind: "branch", name: row.kind === "remote" ? `refs/remotes/${row.name}` : row.name, tipSha: row.tipSha };
}

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
